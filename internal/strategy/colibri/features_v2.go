package colibri

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/indicator"
)

// featuresV2 : le jeu de colibri_v1_2. FIGÉ dès publication.
//
// Ce qui change par rapport à v1, et pourquoi :
//
//   - Les écarts de prix sont exprimés en unités d'ATR ou de volatilité,
//     plus en fraction du prix. « 0,5 % sous la moyenne » ne dit pas la
//     même chose sur EURCHF et sur GBPJPY ; « 1,2 ATR sous la moyenne »
//     si, et c'est l'unité même des barrières (1,5 ATR). Pour un modèle
//     MUTUALISÉ, c'est la différence entre apprendre le marché et
//     apprendre l'instrument. Un arbre est insensible à une transformation
//     monotone d'UNE colonne, pas à une normalisation qui varie d'une
//     ligne à l'autre : le changement n'est donc pas cosmétique.
//   - `roc_5`, `roc_10` disparaissent : roc_n = exp(ret_log_n) − 1 est une
//     fonction monotone de ret_log_n. Pour un arbre, ce sont deux copies
//     de la même colonne, qui ne font que diluer le tirage des features.
//   - `days_to_friday` disparaît pour la même raison (fonction de `dow`),
//     remplacée par `hours_to_week_close` : la cible v1_2 est bornée par la
//     clôture de fin de semaine, et le temps qui reste avant cette clôture
//     décide directement de l'issue « sortie au temps ».
//   - `spread_atr` entre : le coût d'un aller-retour rapporté à la
//     barrière. La cible étant NETTE de coûts, le modèle doit voir ce coût.
//     Colonne OPTIONNELLE : sans côté ask, elle vaut NaN et la ligne reste
//     exploitable — le coût est alors inconnu, pas nul.
var featuresV2 = featureSet{
	columns:  columnsV2,
	compute:  computeV2,
	optional: map[string]bool{"spread_atr": true},
}

// spreadWindow : bougies de la médiane glissante du spread. Environ seize
// séances en H4 : assez pour ignorer les pointes d'ouverture et
// d'annonces, assez court pour suivre un changement de régime de
// liquidité. Convention, pas mesure — voir docs/colibri.md.
const spreadWindow = 100

// weekCloseHourUTC : heure UTC retenue pour la clôture hebdomadaire du
// forex (vendredi). Elle varie d'une heure avec l'heure d'été new-yorkaise ;
// la feature n'a besoin que d'un ordre de grandeur du temps restant.
const weekCloseHourUTC = 21

var columnsV2 = buildColumnsV2()

func buildColumnsV2() []string {
	cols := make([]string, 0, 33)
	for _, n := range returnWindows {
		cols = append(cols, fmt.Sprintf("ret_z_%d", n))
	}
	cols = append(cols, "range_pos_20", "gap_atr")
	for _, n := range smaWindows {
		cols = append(cols, fmt.Sprintf("sma_dev_atr_%d", n))
	}
	cols = append(cols, "sma_slope_atr_20")
	for _, n := range emaWindows {
		cols = append(cols, fmt.Sprintf("ema_dev_atr_%d", n))
	}
	cols = append(cols, "macd_atr", "macd_signal_atr", "macd_hist_atr", "adx_14")
	for _, n := range rsiWindows {
		cols = append(cols, fmt.Sprintf("rsi_%d", n))
	}
	cols = append(cols, "stoch_k_14", "stoch_d_14")
	cols = append(cols, "atr_norm_14", "realized_vol_20", "bb_width_20", "vol_ratio_10_50")
	cols = append(cols, "vol_rel_20", "vol_spike_20", "obv_z_20")
	cols = append(cols, "spread_atr")
	cols = append(cols, "dow", "hour_utc", "session", "hours_to_week_close")
	return cols
}

// computeV2 calcule les features causales de colibri_v1_2.
func computeV2(series core.Series) *feature.Matrix {
	n := len(series)
	m := feature.NewMatrix(n, columnsV2)
	if n == 0 {
		return m
	}
	open := series.Opens()
	high := series.Highs()
	low := series.Lows()
	closes := series.Closes()
	volume := series.Volumes()
	logClose := indicator.Log(closes)

	col := func(name string) int {
		i, err := m.ColumnIndex(name)
		if err != nil {
			panic(err) // incohérence de code : colonnes et écritures sont ici
		}
		return i
	}

	atr := indicator.ATR(high, low, closes, atrPeriod)
	ret1 := indicator.Diff(logClose, 1)
	rv := indicator.RollingStd(ret1, rvWindow)

	// --- Retours en unités de volatilité -----------------------------------
	// ret_z_n = ln(C_t / C_{t−n}) / (σ_20 · √n) : sous l'hypothèse d'une
	// marche au hasard, c'est une variable de variance ≈ 1 quel que soit
	// l'instrument ou le régime.
	for _, w := range returnWindows {
		r := indicator.Diff(logClose, w)
		z := make([]float64, n)
		for i := range z {
			if math.IsNaN(r[i]) || math.IsNaN(rv[i]) || rv[i] == 0 {
				z[i] = math.NaN()
				continue
			}
			z[i] = r[i] / (rv[i] * math.Sqrt(float64(w)))
		}
		m.SetColumn(col(fmt.Sprintf("ret_z_%d", w)), z)
	}

	lowR := indicator.RollingMin(low, rangeWindow)
	highR := indicator.RollingMax(high, rangeWindow)
	rangePos := make([]float64, n)
	gap := make([]float64, n)
	for i := 0; i < n; i++ {
		span := highR[i] - lowR[i]
		if math.IsNaN(span) || span == 0 {
			rangePos[i] = math.NaN()
		} else {
			rangePos[i] = (closes[i] - lowR[i]) / span
		}
		if i == 0 {
			gap[i] = math.NaN()
		} else {
			gap[i] = perATR(open[i]-closes[i-1], atr[i])
		}
	}
	m.SetColumn(col("range_pos_20"), rangePos)
	m.SetColumn(col("gap_atr"), gap)

	// --- Tendance, en ATR --------------------------------------------------
	for _, w := range smaWindows {
		sma := indicator.RollingMean(closes, w)
		m.SetColumn(col(fmt.Sprintf("sma_dev_atr_%d", w)), deviationATR(closes, sma, atr))
	}
	sma20 := indicator.RollingMean(closes, 20)
	slope := make([]float64, n)
	for i := 0; i < n; i++ {
		if i < 5 {
			slope[i] = math.NaN()
			continue
		}
		slope[i] = perATR(sma20[i]-sma20[i-5], atr[i])
	}
	m.SetColumn(col("sma_slope_atr_20"), slope)
	for _, w := range emaWindows {
		ema := indicator.EWMSpan(closes, w, w)
		m.SetColumn(col(fmt.Sprintf("ema_dev_atr_%d", w)), deviationATR(closes, ema, atr))
	}
	macd, macdSig, macdHist := indicator.MACD(closes, macdFast, macdSlow, macdSignal)
	m.SetColumn(col("macd_atr"), divide(macd, atr))
	m.SetColumn(col("macd_signal_atr"), divide(macdSig, atr))
	m.SetColumn(col("macd_hist_atr"), divide(macdHist, atr))
	m.SetColumn(col("adx_14"), indicator.ADX(high, low, closes, adxPeriod))

	// --- Momentum (bornés par construction, inchangés) ---------------------
	for _, w := range rsiWindows {
		m.SetColumn(col(fmt.Sprintf("rsi_%d", w)), indicator.RSI(closes, w))
	}
	k, d := indicator.Stochastic(high, low, closes, stochPeriod, stochSmooth)
	m.SetColumn(col("stoch_k_14"), k)
	m.SetColumn(col("stoch_d_14"), d)

	// --- Volatilité : le RÉGIME, donc en niveau ----------------------------
	m.SetColumn(col("atr_norm_14"), divideBy(atr, closes))
	m.SetColumn(col("realized_vol_20"), rv)
	m.SetColumn(col("bb_width_20"), indicator.BollingerWidth(closes, bbWindow))
	m.SetColumn(col("vol_ratio_10_50"), divide(indicator.RollingStd(ret1, volRatioShort),
		indicator.RollingStd(ret1, volRatioLong)))

	// --- Volume (⚠ Dukascopy forex = volume de TICKS) ----------------------
	volMean := indicator.RollingMean(volume, volumeWindow)
	volStd := indicator.RollingStd(volume, volumeWindow)
	spike := make([]float64, n)
	for i := 0; i < n; i++ {
		switch {
		case math.IsNaN(volMean[i]) || math.IsNaN(volStd[i]):
			spike[i] = math.NaN()
		case volStd[i] == 0:
			spike[i] = 0 // variance nulle : pas de pic, jamais NaN
		default:
			spike[i] = (volume[i] - volMean[i]) / volStd[i]
		}
	}
	m.SetColumn(col("vol_rel_20"), divide(volume, volMean))
	m.SetColumn(col("vol_spike_20"), spike)
	m.SetColumn(col("obv_z_20"), indicator.OBVZScore(closes, volume, volumeWindow))

	// --- Coût ----------------------------------------------------------------
	m.SetColumn(col("spread_atr"), divide(spreadCost(series), atr))

	// --- Calendrier ----------------------------------------------------------
	dow := make([]float64, n)
	hour := make([]float64, n)
	session := make([]float64, n)
	toClose := make([]float64, n)
	for i, bar := range series {
		t := bar.Time.UTC()
		wd := (int(t.Weekday()) + 6) % 7 // lundi = 0 (ISO)
		dow[i] = float64(wd)
		h := t.Hour()
		hour[i] = float64(h)
		switch {
		case h < 7:
			session[i] = 0
		case h < 13:
			session[i] = 1
		case h < 21:
			session[i] = 2
		default:
			session[i] = 3
		}
		toClose[i] = hoursToWeekClose(t)
	}
	m.SetColumn(col("dow"), dow)
	m.SetColumn(col("hour_utc"), hour)
	m.SetColumn(col("session"), session)
	m.SetColumn(col("hours_to_week_close"), toClose)

	sanitize(m)
	return m
}

// hoursToWeekClose : heures restant jusqu'au vendredi weekCloseHourUTC de
// la semaine ISO de t, jamais négatif (0 au-delà : week-end).
func hoursToWeekClose(t time.Time) float64 {
	wd := (int(t.Weekday()) + 6) % 7
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	friday := day.AddDate(0, 0, 4-wd).Add(weekCloseHourUTC * time.Hour)
	h := friday.Sub(t).Hours()
	if h < 0 {
		return 0
	}
	return h
}

// spreadCost : coût d'un aller-retour, en unités de prix, estimé par la
// médiane GLISSANTE et CAUSALE du spread (ask_close − bid_close) sur les
// spreadWindow dernières bougies — moins au début de série. NaN tant
// qu'aucune bougie de la fenêtre n'a de côté ask.
//
// C'est la même estimation qui fixe le coût de la cible à l'entraînement
// et le coût de la décision à l'inférence. Causale, elle garde la
// stabilité par préfixe ; médiane, elle ignore les pointes d'ouverture.
func spreadCost(series core.Series) []float64 {
	n := len(series)
	out := make([]float64, n)
	window := make([]float64, 0, spreadWindow)
	for i := 0; i < n; i++ {
		window = window[:0]
		from := i - spreadWindow + 1
		if from < 0 {
			from = 0
		}
		for _, b := range series[from : i+1] {
			if !b.HasAsk() {
				continue
			}
			if s := b.AskClose - b.BidClose; s > 0 {
				window = append(window, s)
			}
		}
		if len(window) == 0 {
			out[i] = math.NaN()
			continue
		}
		sort.Float64s(window)
		k := len(window)
		if k%2 == 1 {
			out[i] = window[k/2]
		} else {
			out[i] = (window[k/2-1] + window[k/2]) / 2
		}
	}
	return out
}

func perATR(v, atr float64) float64 {
	if math.IsNaN(v) || math.IsNaN(atr) || atr <= 0 {
		return math.NaN()
	}
	return v / atr
}

func deviationATR(closes, ref, atr []float64) []float64 {
	out := make([]float64, len(closes))
	for i := range closes {
		out[i] = perATR(closes[i]-ref[i], atr[i])
	}
	return out
}
