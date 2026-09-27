package data

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
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

// dukascopyIDs : identifiants Dukascopy qui diffèrent du symbole du
// projet. Les autres symboles s'écrivent à l'identique.
var dukascopyIDs = map[string]string{
	"US500":  "USA500IDXUSD",
	"US30":   "USA30IDXUSD",
	"NAS100": "USATECHIDXUSD",
	"DE40":   "DEUIDXEUR",
	"UK100":  "GBRIDXGBP",
	"JP225":  "JPNIDXJPY",
}

// DukascopyID renvoie l'identifiant Dukascopy d'un symbole du projet.
func DukascopyID(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if id, ok := dukascopyIDs[symbol]; ok {
		return id
	}
	return symbol
}

// CandleURL construit l'URL d'un fichier M1 d'un jour.
// ⚠ Le MOIS est 0-based chez Dukascopy (janvier = 00).
func CandleURL(symbol string, day time.Time, side Side) string {
	return candleURL(BaseURL, symbol, day, side)
}

func candleURL(base, symbol string, day time.Time, side Side) string {
	d := day.UTC()
	return fmt.Sprintf("%s/%s/%04d/%02d/%02d/%s_candles_min_1.bi5",
		base, DukascopyID(symbol), d.Year(), int(d.Month())-1, d.Day(), side)
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
		return nil, fmt.Errorf("fichier bi5 corrompu pour le %s : %d octets (multiple de %d attendu)",
			day.Format("2006-01-02"), len(raw), candleRecordSize)
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

// dukascopySource : fichiers bi5 journaliers, bid et ask séparés.
type dukascopySource struct {
	opts SourceOptions
	base string
}

func init() {
	RegisterSource("dukascopy", func(o SourceOptions) Source {
		base := o.BaseURL
		if base == "" {
			base = BaseURL
		}
		return &dukascopySource{opts: o, base: strings.TrimRight(base, "/")}
	})
}

func (s *dukascopySource) Info() SourceInfo {
	return SourceInfo{
		Name: "dukascopy", Label: "Dukascopy", FirstYear: 2003, Unit: "jours", HasVolume: true,
		Note: "M1 bid ET ask (le côté ask permet de MESURER le spread), volume de ticks, " +
			"tout le catalogue. Concurrence basse volontaire : au-delà de 3-4 requêtes simultanées, " +
			"Dukascopy répond 429.",
	}
}

// Serves : Dukascopy publie tout le catalogue du projet.
func (s *dukascopySource) Serves(symbol string) bool {
	_, err := LookupInstrument(symbol)
	return err == nil
}

// FetchYear récupère les jours ouvrés de l'année, en parallèle.
//
// Les deux côtés (bid et ask) sont récupérés : c'est ce qui permettra de
// MESURER le spread au lieu de l'inventer. Un jour dont l'ask manque garde
// son bid — la bougie existe, seul le spread sera indisponible.
func (s *dukascopySource) FetchYear(ctx context.Context, inst Instrument, symbol string, year int,
	now time.Time, step func(Step)) (Fetched, error) {

	days := daysOfYear(year, now)
	type dayResult struct {
		bars []core.Bar
		err  error
	}
	results := make([]dayResult, len(days))

	sem := make(chan struct{}, s.opts.Concurrency)
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

			merged, none, err := s.fetchDayBoth(ctx, inst, symbol, day)
			results[i] = dayResult{bars: merged, err: err}

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
			step(Step{Done: done, Total: len(days), Bars: bars, Skipped: skipped,
				Failures: failures, Current: day.Format("2006-01-02")})
			mu.Unlock()
		}(i, day)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Fetched{}, err
	}

	out := Fetched{Missing: failures}
	for _, r := range results {
		if r.err != nil {
			if out.FirstErr == nil {
				out.FirstErr = r.err
			}
			continue
		}
		out.Bars = append(out.Bars, r.bars...)
	}
	return out, nil
}

func (s *dukascopySource) fetchDay(ctx context.Context, symbol string, day time.Time, side Side) ([]byte, error) {
	return httpGet(ctx, s.opts.HTTP, candleURL(s.base, symbol, day, side), "Dukascopy", s.opts.MaxRetries)
}

// fetchDayBoth récupère bid et ask d'un jour et les fusionne par minute.
func (s *dukascopySource) fetchDayBoth(ctx context.Context, inst Instrument, symbol string, day time.Time) ([]core.Bar, bool, error) {
	bidRaw, err := s.fetchDay(ctx, symbol, day, SideBid)
	if errors.Is(err, errNoData) {
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
	if askRaw, err := s.fetchDay(ctx, symbol, day, SideAsk); err == nil {
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
