package data

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ulikunitz/xz/lzma"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// BaseURL du flux historique Dukascopy.
const BaseURL = "https://datafeed.dukascopy.com/datafeed"

// candleRecordSize : une bougie bi5 tient sur 24 octets BIG-endian :
// (offset en secondes depuis minuit, open, close, low, high, volume).
// ⚠ l'ordre n'est PAS OHLC mais O-C-L-H : c'est la source d'erreur
// classique sur ce format.
const candleRecordSize = 24

// Side : côté du carnet téléchargé.
type Side string

const (
	SideBid Side = "BID"
	SideAsk Side = "ASK"
)

// CandleURL construit l'URL d'un fichier M1 d'un jour.
// ⚠ Le MOIS est 0-based chez Dukascopy (janvier = 00).
func CandleURL(inst Instrument, day time.Time, side Side) string {
	d := day.UTC()
	return fmt.Sprintf("%s/%s/%04d/%02d/%02d/%s_candles_min_1.bi5",
		BaseURL, inst.DukascopyID, d.Year(), int(d.Month())-1, d.Day(), side)
}

// DecodeCandles décode un fichier bi5 (LZMA) en bougies d'un côté.
func DecodeCandles(payload []byte, inst Instrument, day time.Time) ([]rawCandle, error) {
	if len(payload) == 0 {
		return nil, nil // jour sans donnée (week-end, férié)
	}
	r, err := lzma.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("flux LZMA illisible : %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("décompression interrompue : %w", err)
	}
	if len(raw)%candleRecordSize != 0 {
		return nil, fmt.Errorf("fichier bi5 corrompu pour %s %s : %d octets (multiple de %d attendu)",
			inst.DukascopyID, day.Format("2006-01-02"), len(raw), candleRecordSize)
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	divisor := math.Pow10(inst.Decimals)
	out := make([]rawCandle, 0, len(raw)/candleRecordSize)
	for off := 0; off < len(raw); off += candleRecordSize {
		rec := raw[off : off+candleRecordSize]
		seconds := binary.BigEndian.Uint32(rec[0:])
		open := float64(binary.BigEndian.Uint32(rec[4:])) / divisor
		closeP := float64(binary.BigEndian.Uint32(rec[8:])) / divisor
		low := float64(binary.BigEndian.Uint32(rec[12:])) / divisor
		high := float64(binary.BigEndian.Uint32(rec[16:])) / divisor
		volume := float64(math.Float32frombits(binary.BigEndian.Uint32(rec[20:])))
		out = append(out, rawCandle{
			Time:   dayStart.Add(time.Duration(seconds) * time.Second),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closeP,
			Volume: volume,
		})
	}
	return out, nil
}

type rawCandle struct {
	Time                   time.Time
	Open, High, Low, Close float64
	Volume                 float64
}

// DownloadProgress décrit l'avancement d'un téléchargement, publié vers la
// TUI. Les chiffres sont RÉELS : rien n'est extrapolé pour faire joli.
type DownloadProgress struct {
	Symbol      string
	Year        int
	DaysDone    int
	DaysTotal   int
	Bars        int
	Skipped     int // jours sans donnée (week-ends, fériés) : normal
	Failures    int
	CurrentStep string
	Done        bool
	Err         error
}

// Downloader télécharge l'historique M1 Dukascopy.
type Downloader struct {
	HTTP        *http.Client
	HistoryDir  string
	Concurrency int
	MaxRetries  int
	Logger      *slog.Logger
}

// NewDownloader règle un client HTTP avec des délais explicites : sans
// timeout, une connexion qui ne répond jamais bloque un worker pour
// toujours et le téléchargement « avance » sans jamais finir.
func NewDownloader(historyDir string, concurrency int, logger *slog.Logger) *Downloader {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Downloader{
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		HistoryDir:  historyDir,
		Concurrency: concurrency,
		MaxRetries:  5,
		Logger:      logger,
	}
}

// errNoData signale un 404 : chez Dukascopy, c'est un jour de marché
// fermé, pas une panne. Le distinguer évite de compter des « échecs » là
// où il n'y a simplement rien à télécharger.
var errNoData = fmt.Errorf("aucune donnée pour ce jour")

// fetchDay récupère un fichier bi5, avec retries et backoff.
//
// Deux régimes de backoff, volontairement différents :
//   - 429 (limite de débit) : backoff LONG, Retry-After honoré s'il est
//     fourni, sinon 5 s × tentative. Un backoff court ne fait qu'aggraver
//     la limite ;
//   - autres erreurs réseau / 5xx : backoff exponentiel court (1-2-4-8 s).
func (d *Downloader) fetchDay(ctx context.Context, inst Instrument, day time.Time, side Side) ([]byte, error) {
	url := CandleURL(inst, day, side)
	var lastErr error
	for attempt := 1; attempt <= d.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "GoldenWaterfall/1.0 (+historique M1)")
		resp, err := d.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusNotFound:
			return nil, errNoData
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(resp.Header.Get("Retry-After"), time.Duration(attempt)*5*time.Second)
			lastErr = fmt.Errorf("limite de débit Dukascopy (429)")
			if waitErr := sleepCtx(ctx, wait); waitErr != nil {
				return nil, waitErr
			}
			continue
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("erreur serveur %d", resp.StatusCode)
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("réponse inattendue %d pour %s", resp.StatusCode, url)
		case readErr != nil:
			lastErr = readErr
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("%s : %w (après %d tentatives)", url, lastErr, d.MaxRetries)
}

func backoffShort(attempt int) time.Duration {
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func retryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DownloadYear télécharge une année complète pour un symbole et l'écrit.
//
// Les deux côtés (bid et ask) sont récupérés : c'est ce qui permettra de
// MESURER le spread au lieu de l'inventer. Un jour dont l'ask manque garde
// son bid — la bougie existe, seul le spread sera indisponible.
func (d *Downloader) DownloadYear(ctx context.Context, symbol string, year int,
	progress func(DownloadProgress)) (int, error) {

	inst, err := LookupInstrument(symbol)
	if err != nil {
		return 0, err
	}
	days := daysOfYear(year, time.Now().UTC())
	if len(days) == 0 {
		return 0, nil
	}

	type dayResult struct {
		bars []core.Bar
		err  error
		none bool
	}
	results := make([]dayResult, len(days))

	sem := make(chan struct{}, d.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done, skipped, failures, bars := 0, 0, 0, 0

	for i, day := range days {
		wg.Add(1)
		go func(i int, day time.Time) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = dayResult{err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			merged, none, err := d.fetchDayBoth(ctx, inst, day)
			results[i] = dayResult{bars: merged, err: err, none: none}

			mu.Lock()
			done++
			switch {
			case err != nil:
				failures++
			case none:
				skipped++
			default:
				bars += len(merged)
			}
			if progress != nil {
				progress(DownloadProgress{
					Symbol: symbol, Year: year,
					DaysDone: done, DaysTotal: len(days),
					Bars: bars, Skipped: skipped, Failures: failures,
					CurrentStep: day.Format("2006-01-02"),
				})
			}
			mu.Unlock()
		}(i, day)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var all core.Series
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		all = append(all, r.bars...)
	}
	if len(all) == 0 {
		if firstErr != nil {
			return 0, firstErr
		}
		return 0, nil // année sans donnée (instrument plus récent)
	}
	all = Sanitize(all)

	path := FilePath(d.HistoryDir, symbol, year)
	header := FileHeader{
		Symbol: symbol, Year: year, Scale: inst.Scale(),
		Failures: int32(failures),
	}
	if err := WriteSeries(path, header, all); err != nil {
		return 0, err
	}
	if failures > 0 && d.Logger != nil {
		// Une année partiellement téléchargée est ÉCRITE, mais son en-tête
		// porte le nombre de jours manquants : la prochaine demande la
		// refera au lieu de la sauter, et l'interface l'affiche comme
		// incomplète plutôt que comme faite.
		d.Logger.Warn("année incomplète, jours manquants",
			"symbole", symbol, "annee", year, "echecs", failures, "premiere_erreur", firstErr)
	}
	return len(all), nil
}

// fetchDayBoth récupère bid et ask d'un jour et les fusionne par minute.
func (d *Downloader) fetchDayBoth(ctx context.Context, inst Instrument, day time.Time) ([]core.Bar, bool, error) {
	bidRaw, err := d.fetchDay(ctx, inst, day, SideBid)
	if err == errNoData {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	bid, err := DecodeCandles(bidRaw, inst, day)
	if err != nil {
		return nil, false, err
	}
	if len(bid) == 0 {
		return nil, true, nil
	}

	// L'ask est un BONUS : son absence ne doit pas faire perdre le bid.
	// Sans ask, le spread ne sera pas mesurable et le backtest le dira.
	var ask []rawCandle
	if askRaw, err := d.fetchDay(ctx, inst, day, SideAsk); err == nil {
		if decoded, err := DecodeCandles(askRaw, inst, day); err == nil {
			ask = decoded
		}
	}
	askByTime := make(map[int64]rawCandle, len(ask))
	for _, c := range ask {
		askByTime[c.Time.Unix()] = c
	}

	out := make([]core.Bar, 0, len(bid))
	for _, c := range bid {
		bar := core.Bar{
			Time:     c.Time,
			BidOpen:  c.Open,
			BidHigh:  c.High,
			BidLow:   c.Low,
			BidClose: c.Close,
			Volume:   c.Volume,
		}
		if a, ok := askByTime[c.Time.Unix()]; ok {
			bar.AskOpen, bar.AskHigh, bar.AskLow, bar.AskClose = a.Open, a.High, a.Low, a.Close
		}
		out = append(out, bar)
	}
	return out, false, nil
}

// daysOfYear liste les jours d'une année jusqu'à aujourd'hui, samedis et
// dimanches exclus (le forex Dukascopy n'y publie rien : les demander
// coûterait 104 requêtes 404 par an et par symbole).
func daysOfYear(year int, today time.Time) []time.Time {
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	if end.After(today) {
		end = today
	}
	var out []time.Time
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		out = append(out, day)
	}
	return out
}

// NeedsDownload dit si une année doit être (re)téléchargée.
//
// Trois cas la rendent nécessaire : le fichier est absent, il est
// illisible, ou il est INCOMPLET. Le troisième est le piège : sans lui,
// une année écrite avec des trous passait pour faite et ses jours
// manquants le restaient définitivement.
//
// L'année EN COURS est toujours à refaire — elle est incomplète par
// nature, puisque le marché n'a pas fini de la produire.
func NeedsDownload(historyDir, symbol string, year, currentYear int) bool {
	if year >= currentYear {
		return true
	}
	h, err := ReadHeader(FilePath(historyDir, symbol, year))
	if err != nil {
		return true
	}
	return !h.Complete()
}

// MissingYears renvoie les années à (re)télécharger pour un symbole.
func (d *Downloader) MissingYears(symbol string, from, to int) []int {
	current := time.Now().UTC().Year()
	var out []int
	for y := from; y <= to; y++ {
		if NeedsDownload(d.HistoryDir, symbol, y, current) {
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}
