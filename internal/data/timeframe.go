package data

import (
	"fmt"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Timeframe est une unité de temps de bougie.
type Timeframe string

const (
	M1  Timeframe = "M1"
	M5  Timeframe = "M5"
	M15 Timeframe = "M15"
	M30 Timeframe = "M30"
	H1  Timeframe = "H1"
	H4  Timeframe = "H4"
	D1  Timeframe = "D1"
	W1  Timeframe = "W1"
	MN1 Timeframe = "MN1"
)

// Timeframes est la liste ordonnée des unités supportées.
var Timeframes = []Timeframe{M1, M5, M15, M30, H1, H4, D1, W1, MN1}

// ParseTimeframe convertit une chaîne en Timeframe. Une valeur inconnue
// est une ERREUR : on ne devine pas l'unité de temps d'un backtest.
func ParseTimeframe(s string) (Timeframe, error) {
	up := Timeframe(strings.ToUpper(strings.TrimSpace(s)))
	for _, tf := range Timeframes {
		if tf == up {
			return tf, nil
		}
	}
	names := make([]string, len(Timeframes))
	for i, tf := range Timeframes {
		names[i] = string(tf)
	}
	return "", fmt.Errorf("unité de temps inconnue %q (%s)", s, strings.Join(names, ", "))
}

// Duration renvoie la durée d'une bougie pour les unités RÉGULIÈRES.
// D1, W1 et MN1 ne sont pas des durées fixes au sens calendaire : Floor
// les traite à part, et Duration renvoie alors leur durée nominale (utile
// pour un affichage, jamais pour un calcul de bucket).
func (t Timeframe) Duration() time.Duration {
	switch t {
	case M1:
		return time.Minute
	case M5:
		return 5 * time.Minute
	case M15:
		return 15 * time.Minute
	case M30:
		return 30 * time.Minute
	case H1:
		return time.Hour
	case H4:
		return 4 * time.Hour
	case D1:
		return 24 * time.Hour
	case W1:
		return 7 * 24 * time.Hour
	case MN1:
		return 30 * 24 * time.Hour
	}
	return 0
}

// Floor ramène un instant au début de son bucket, en UTC.
//
// Les unités infra-journalières sont alignées sur l'époque Unix (H4 tombe
// donc à 0, 4, 8, 12, 16, 20 h UTC) ; D1 sur minuit UTC ; W1 sur le LUNDI
// (convention ISO, celle des semaines de marché) ; MN1 sur le 1er du mois.
func (t Timeframe) Floor(ts time.Time) time.Time {
	u := ts.UTC()
	switch t {
	case D1:
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	case W1:
		day := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
		// time.Weekday : dimanche = 0. On recule jusqu'au lundi.
		back := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -back)
	case MN1:
		return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		d := t.Duration()
		if d <= 0 {
			return u
		}
		return u.Truncate(d)
	}
}

// Resample agrège une série M1 vers une unité supérieure.
//
// OHLC : premier / max / min / dernier ; volume : somme. Les côtés bid et
// ask sont agrégés SÉPARÉMENT, ce qui préserve la mesure du spread après
// conversion. Les buckets sans aucune bougie M1 (week-ends, fériés) ne
// sont PAS créés : un marché fermé n'a pas de bougie, et en fabriquer une
// plate inventerait des données.
func Resample(series core.Series, tf Timeframe) core.Series {
	if tf == M1 || len(series) == 0 {
		return series
	}
	out := make(core.Series, 0, len(series)/4+1)
	var cur core.Bar
	var bucket time.Time
	open := false

	flush := func() {
		if open {
			out = append(out, cur)
			open = false
		}
	}
	for _, bar := range series {
		b := tf.Floor(bar.Time)
		if !open || !b.Equal(bucket) {
			flush()
			bucket = b
			cur = core.Bar{
				Time:     b,
				BidOpen:  bar.BidOpen,
				BidHigh:  bar.BidHigh,
				BidLow:   bar.BidLow,
				BidClose: bar.BidClose,
				AskOpen:  bar.AskOpen,
				AskHigh:  bar.AskHigh,
				AskLow:   bar.AskLow,
				AskClose: bar.AskClose,
				Volume:   bar.Volume,
			}
			open = true
			continue
		}
		if bar.BidHigh > cur.BidHigh {
			cur.BidHigh = bar.BidHigh
		}
		if bar.BidLow < cur.BidLow {
			cur.BidLow = bar.BidLow
		}
		cur.BidClose = bar.BidClose
		if bar.HasAsk() {
			if !cur.HasAsk() {
				// Premier côté ask du bucket : on l'ouvre ici plutôt que
				// de laisser des zéros se mêler aux extrema.
				cur.AskOpen, cur.AskHigh, cur.AskLow = bar.AskOpen, bar.AskHigh, bar.AskLow
			} else {
				if bar.AskHigh > cur.AskHigh {
					cur.AskHigh = bar.AskHigh
				}
				if bar.AskLow < cur.AskLow {
					cur.AskLow = bar.AskLow
				}
			}
			cur.AskClose = bar.AskClose
		}
		cur.Volume += bar.Volume
	}
	flush()
	return out
}
