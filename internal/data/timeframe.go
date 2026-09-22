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

// nextBucket renvoie le DÉBUT du bucket suivant.
//
// Séparé de Floor parce que c'est lui qui permet à Resample de ne pas
// recalculer un plancher par bougie : tant que l'horodatage reste dans
// [bucket, nextBucket[, il appartient au bucket courant.
func (t Timeframe) nextBucket(bucket time.Time) time.Time {
	switch t {
	case D1:
		return bucket.AddDate(0, 0, 1)
	case W1:
		return bucket.AddDate(0, 0, 7)
	case MN1:
		return bucket.AddDate(0, 1, 0)
	default:
		d := t.Duration()
		if d <= 0 {
			// Unité sans durée : chaque bougie devient son propre bucket,
			// ce que l'intervalle vide obtient naturellement.
			return bucket
		}
		return bucket.Add(d)
	}
}

// estimateBuckets borne la capacité initiale de la tranche de sortie.
//
// Elle n'est PAS déduite de len(series)/4 : cette hypothèse ne vaut que
// pour M1→M5. En M1→H4 elle réservait 93 000 bougies pour en produire
// 1 560, soit 8,9 Mo mis à zéro à chaque appel — l'essentiel du coût
// mesuré. La durée couverte par la série est la seule estimation qui
// suive l'unité demandée ; elle reste plafonnée par le nombre de bougies
// d'entrée, une agrégation ne pouvant jamais en produire davantage.
func estimateBuckets(series core.Series, tf Timeframe) int {
	est := len(series)
	d := tf.Duration()
	if d <= 0 || len(series) < 2 {
		return est
	}
	span := series[len(series)-1].Time.Sub(series[0].Time)
	if span <= 0 {
		return 1
	}
	if n := int(span/d) + 2; n < est {
		return n
	}
	return est
}

// Resample agrège une série M1 vers une unité supérieure.
//
// OHLC : premier / max / min / dernier ; volume : somme. Les côtés bid et
// ask sont agrégés SÉPARÉMENT, ce qui préserve la mesure du spread après
// conversion. Les buckets sans aucune bougie M1 (week-ends, fériés) ne
// sont PAS créés : un marché fermé n'a pas de bougie, et en fabriquer une
// plate inventerait des données.
//
// Le plancher n'est calculé qu'au CHANGEMENT de bucket, pas à chaque
// bougie : le test d'appartenance bucket ≤ t < suivant est exactement
// équivalent à Floor(t) == bucket pour toutes les unités livrées, y
// compris hors d'ordre, et coûte deux comparaisons au lieu d'une division
// sur 64 bits.
func Resample(series core.Series, tf Timeframe) core.Series {
	if tf == M1 || len(series) == 0 {
		return series
	}
	out := make(core.Series, 0, estimateBuckets(series, tf))
	var cur core.Bar
	var bucket, next time.Time
	open := false

	flush := func() {
		if open {
			out = append(out, cur)
			open = false
		}
	}
	// Parcours par indice : une core.Bar fait une centaine d'octets, et la
	// copier par valeur à chaque tour pesait 15 % du temps mesuré.
	for i := range series {
		bar := &series[i]
		if !open || bar.Time.Before(bucket) || !bar.Time.Before(next) {
			flush()
			bucket = tf.Floor(bar.Time)
			next = tf.nextBucket(bucket)
			cur = core.Bar{
				Time:     bucket,
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
