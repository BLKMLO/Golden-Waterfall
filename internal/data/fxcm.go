package data

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// FXCMBaseURL : bougies M1 publiques de FXCM, un fichier gzip par semaine
// et par paire : <base>/<SYMBOLE>/<année>/<semaine>.csv.gz.
//
// Constaté le 27/09/2026 (et rien de plus) :
//   - colonnes `DateTime,BidOpen,BidHigh,BidLow,BidClose,AskOpen,AskHigh,
//     AskLow,AskClose`, date `MM/JJ/AAAA hh:mm:ss.000` ; AUCUN volume ;
//   - horodatage UTC : les semaines ouvrent le dimanche à 22 h en hiver et
//     21 h en été, ferment le vendredi à 21 h 59 / 20 h 59 — l'heure de
//     New York (17 h) exprimée en UTC ;
//   - première année : 2012 ; 25 paires sur les 31 du modèle de
//     configuration (ni EURCAD, GBPAUD, CHFJPY, ni métaux ni indices) ;
//   - la NUMÉROTATION des semaines n'est pas stable d'une année à l'autre
//     (la semaine du 29/12/2019 est « 2019/53 », celle du 04/01/2026 est
//     « 2026/1 ») ;
//   - des semaines entières manquent (2026 : semaines 18 à 31) ;
//   - les heures du DIMANCHE soir sont publiées. Elles sont ÉCARTÉES (cf.
//     IsWeekend) : le backtest clôt la semaine sur la dernière bougie de
//     la semaine ISO, qui serait sinon celle du dimanche, APRÈS le
//     week-end.
//
// D'où la méthode : on demande toutes les semaines candidates (52 et 53
// de l'année précédente, 1 à 53 de l'année), on garde les bougies de
// l'année, et on COMPTE les semaines de marché restées vides — c'est ce
// compte qui rend l'année incomplète, pas un 404.
const FXCMBaseURL = "https://candledata.fxcorporate.com/m1"

// fxcmFirstYear : première année servie (2011 : 404 ; 2012 : 200).
const fxcmFirstYear = 2012

// fxcmSymbols : paires constatées servies (fichier 2023/20 en 200 ; les
// absentes l'étaient aussi sur 2013, 2017, 2019, 2022, 2024 et 2026).
var fxcmSymbols = map[string]bool{
	"EURUSD": true, "GBPUSD": true, "USDJPY": true, "USDCHF": true, "USDCAD": true,
	"AUDUSD": true, "NZDUSD": true, "EURGBP": true, "EURJPY": true, "EURCHF": true,
	"EURAUD": true, "EURNZD": true, "GBPJPY": true, "GBPCHF": true, "GBPCAD": true,
	"GBPNZD": true, "AUDJPY": true, "AUDCHF": true, "AUDCAD": true, "AUDNZD": true,
	"NZDJPY": true, "NZDCHF": true, "NZDCAD": true, "CADJPY": true, "CADCHF": true,
}

// fxcmTimeLayout : format de la colonne DateTime.
const fxcmTimeLayout = "01/02/2006 15:04:05.000"

type fxcmSource struct {
	opts SourceOptions
	base string
}

func init() {
	RegisterSource("fxcm", func(o SourceOptions) Source {
		base := o.BaseURL
		if base == "" {
			base = FXCMBaseURL
		}
		return &fxcmSource{opts: o, base: strings.TrimRight(base, "/")}
	})
}

func (s *fxcmSource) Info() SourceInfo {
	return SourceInfo{
		Name: "fxcm", Label: "FXCM", FirstYear: fxcmFirstYear, Unit: "semaines", HasVolume: false,
		Note: "M1 bid ET ask depuis 2012, 25 paires forex (ni métaux, ni indices, ni EURCAD, " +
			"GBPAUD, CHFJPY). AUCUN volume : les features de volume de Colibri sont absentes (NaN). " +
			"Des semaines manquent chez FXCM : elles sont comptées, l'année reste incomplète. " +
			"Les heures du dimanche soir sont écartées, comme chez Dukascopy.",
	}
}

func (s *fxcmSource) Serves(symbol string) bool {
	return fxcmSymbols[strings.ToUpper(strings.TrimSpace(symbol))]
}

type fxcmWeek struct{ year, week int }

// fxcmCandidates : les fichiers susceptibles de contenir des bougies de
// l'année, sans ceux qui commenceraient après `now`.
//
// Quelle que soit la numérotation observée, la semaine n d'une année ne
// commence pas avant le 1er janvier + 7n − 13 jours : au-delà de `now`,
// la demander ne renverrait qu'un 404.
func fxcmCandidates(year int, now time.Time) []fxcmWeek {
	out := []fxcmWeek{{year - 1, 52}, {year - 1, 53}}
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	for w := 1; w <= 53; w++ {
		if jan1.AddDate(0, 0, 7*w-13).After(now) {
			break
		}
		out = append(out, fxcmWeek{year, w})
	}
	return out
}

func (s *fxcmSource) FetchYear(ctx context.Context, inst Instrument, symbol string, year int,
	now time.Time, step func(Step)) (Fetched, error) {

	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	weeks := fxcmCandidates(year, now)
	type weekResult struct {
		bars []core.Bar
		err  error
	}
	results := make([]weekResult, len(weeks))

	sem := make(chan struct{}, s.opts.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done, skipped, failures, bars := 0, 0, 0, 0

	for i, w := range weeks {
		wg.Add(1)
		go func(i int, w fxcmWeek) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = weekResult{err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			url := fmt.Sprintf("%s/%s/%d/%d.csv.gz", s.base, symbol, w.year, w.week)
			payload, err := httpGet(ctx, s.opts.HTTP, url, "FXCM", s.opts.MaxRetries)
			none := errors.Is(err, errNoData)
			var parsed []core.Bar
			if err == nil {
				parsed, err = DecodeFXCM(payload)
				if err != nil {
					err = fmt.Errorf("%s : %w", url, err)
				}
			}
			if none {
				err = nil
			}
			// Les bougies du dimanche soir (réouverture) sont écartées :
			// voir IsWeekend.
			kept := parsed[:0]
			for _, b := range parsed {
				if b.Time.Year() == year && !b.Time.After(now) && !IsWeekend(b.Time) {
					kept = append(kept, b)
				}
			}
			results[i] = weekResult{bars: kept, err: err}

			mu.Lock()
			done++
			switch {
			case err != nil:
				failures++
			case none:
				skipped++
			default:
				bars += len(kept)
			}
			step(Step{Done: done, Total: len(weeks), Bars: bars, Skipped: skipped,
				Failures: failures, Current: fmt.Sprintf("semaine %d/%d", w.year, w.week)})
			mu.Unlock()
		}(i, w)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Fetched{}, err
	}

	var out Fetched
	for _, r := range results {
		if r.err != nil && out.FirstErr == nil {
			out.FirstErr = r.err
		}
		out.Bars = append(out.Bars, r.bars...)
	}
	out.Missing = len(missingMarketWeeks(out.Bars, year, now))
	return out, nil
}

// DecodeFXCM lit un fichier hebdomadaire FXCM (gzip ou déjà décompressé).
//
// Les colonnes sont trouvées PAR LEUR NOM : un fournisseur qui en ajoute
// une ou en change l'ordre ne décale pas les prix. Une ligne illisible
// rend le fichier illisible — une semaine comptée manquante vaut mieux
// qu'une semaine à moitié lue et déclarée complète.
func DecodeFXCM(payload []byte) ([]core.Bar, error) {
	raw := payload
	if len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("gzip illisible : %w", err)
		}
		out, err := readLimited(zr, maxPayload)
		if err != nil {
			return nil, fmt.Errorf("décompression interrompue : %w", err)
		}
		raw = out
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	if !sc.Scan() {
		return nil, nil
	}
	cols := map[string]int{}
	for i, name := range strings.Split(strings.TrimSpace(sc.Text()), ",") {
		cols[strings.ToLower(strings.TrimSpace(name))] = i
	}
	idx := func(name string) int {
		if i, ok := cols[name]; ok {
			return i
		}
		return -1
	}
	iTime := idx("datetime")
	bid := [4]int{idx("bidopen"), idx("bidhigh"), idx("bidlow"), idx("bidclose")}
	ask := [4]int{idx("askopen"), idx("askhigh"), idx("asklow"), idx("askclose")}
	if iTime < 0 || bid[0] < 0 || bid[1] < 0 || bid[2] < 0 || bid[3] < 0 {
		return nil, fmt.Errorf("entête inattendue %q (DateTime et Bid* requis)", sc.Text())
	}
	hasAsk := ask[0] >= 0 && ask[1] >= 0 && ask[2] >= 0 && ask[3] >= 0

	var out []core.Bar
	line := 1
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		f := strings.Split(text, ",")
		get := func(i int) (float64, error) {
			if i >= len(f) {
				return 0, fmt.Errorf("ligne %d : %d champs", line, len(f))
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
			if err != nil || !(v > 0) || math.IsInf(v, 0) { // NaN compris
				return 0, fmt.Errorf("ligne %d : prix invalide %q", line, f[i])
			}
			return v, nil
		}
		if iTime >= len(f) {
			return nil, fmt.Errorf("ligne %d : %d champs", line, len(f))
		}
		t, err := time.ParseInLocation(fxcmTimeLayout, strings.TrimSpace(f[iTime]), time.UTC)
		if err != nil {
			return nil, fmt.Errorf("ligne %d : date illisible %q", line, f[iTime])
		}
		bar := core.Bar{Time: t, Volume: math.NaN()}
		var p [4]float64
		for k, c := range bid {
			if p[k], err = get(c); err != nil {
				return nil, err
			}
		}
		bar.BidOpen, bar.BidHigh, bar.BidLow, bar.BidClose = p[0], p[1], p[2], p[3]
		if hasAsk {
			for k, c := range ask {
				if p[k], err = get(c); err != nil {
					return nil, err
				}
			}
			bar.AskOpen, bar.AskHigh, bar.AskLow, bar.AskClose = p[0], p[1], p[2], p[3]
		}
		out = append(out, bar)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// missingMarketWeeks renvoie les lundis des semaines de marché de l'année
// (terminées avant `now`) où aucune bougie n'a été reçue.
//
// Une semaine « de marché » compte au moins deux jours ouvrés dans
// l'année, hors 1er janvier et 25 décembre (marché fermé) : une semaine
// dont seul le 1er janvier tombe dans l'année n'a rien à y livrer. Une
// bougie appartient à la semaine qui suit si elle tombe le dimanche soir
// (ouverture) : d'où le décalage de quatre heures avant de chercher son
// lundi.
func missingMarketWeeks(bars core.Series, year int, now time.Time) []time.Time {
	seen := map[time.Time]bool{}
	for _, b := range bars {
		seen[mondayOf(b.Time.Add(4*time.Hour))] = true
	}
	var missing []time.Time
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	for m := mondayOf(jan1); m.Year() <= year; m = m.AddDate(0, 0, 7) {
		if m.AddDate(0, 0, 5).After(now) {
			break // semaine pas encore terminée
		}
		open := 0
		for d := 0; d < 5; d++ {
			day := m.AddDate(0, 0, d)
			if day.Year() != year {
				continue
			}
			if (day.Month() == time.January && day.Day() == 1) ||
				(day.Month() == time.December && day.Day() == 25) {
				continue
			}
			open++
		}
		if open >= 2 && !seen[m] {
			missing = append(missing, m)
		}
	}
	return missing
}

// mondayOf : le lundi 0 h UTC de la semaine ISO de t.
func mondayOf(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -offset)
}
