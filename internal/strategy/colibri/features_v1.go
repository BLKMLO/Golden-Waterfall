package colibri

import (
	"fmt"
	"math"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/indicator"
)

// featuresV1 : les 34 features de colibri_v1_0 et colibri_v1_1. FIGÉ.
var featuresV1 = featureSet{columns: columnsV1, compute: computeV1}

// --- Fenêtres : elles font partie de la DÉFINITION du modèle Colibri ---
//
// Ce ne sont PAS des réglages runtime. Les changer change le modèle, donc
// sa version. Elles n'ont rien à faire dans config.yaml.
//
// ⚠ Elles sont PARTAGÉES par les jeux v1 et v2 : en modifier une changerait
// des révisions publiées. Une révision future qui veut d'autres fenêtres
// déclare les siennes.
var (
	returnWindows = []int{1, 3, 5, 10, 20}
	smaWindows    = []int{10, 20, 50}
	emaWindows    = []int{12, 26}
	rsiWindows    = []int{7, 14}
	rocWindows    = []int{5, 10}
)

const (
	rangeWindow   = 20
	atrPeriod     = 14
	adxPeriod     = 14
	stochPeriod   = 14
	stochSmooth   = 3
	bbWindow      = 20
	rvWindow      = 20
	volRatioShort = 10
	volRatioLong  = 50
	volumeWindow  = 20
	macdFast      = 12
	macdSlow      = 26
	macdSignal    = 9

	// warmupBars : historique minimum avant la première ligne pleinement
	// valide (la plus longue fenêtre vaut 50 ; les lissages de Wilder se
	// stabilisent au-delà). En deçà, les lignes contiennent des NaN.
	warmupBars = 60

	// contextBars : bougies de contexte à fournir avant un bloc évalué,
	// confortablement au-dessus de warmupBars pour que les indicateurs
	// RÉCURSIFS (EMA, Wilder), sans fenêtre finie, soient stabilisés dès
	// la première bougie décidée.
	contextBars = 300
)

// columnsV1 est l'ordre CANONIQUE et FIGÉ des colonnes. Entraînement et
// inférence doivent partager exactement cette liste et cet ordre — c'est
// la seule chose qui garantit qu'un modèle rechargé voit les mêmes
// colonnes qu'au fit.
var columnsV1 = buildColumnsV1()

func buildColumnsV1() []string {
	cols := make([]string, 0, 34)
	for _, n := range returnWindows {
		cols = append(cols, fmt.Sprintf("ret_log_%d", n))
	}
	cols = append(cols, "range_pos_20", "gap_open")
	for _, n := range smaWindows {
		cols = append(cols, fmt.Sprintf("sma_dev_%d", n))
	}
	cols = append(cols, "sma_slope_20")
	for _, n := range emaWindows {
		cols = append(cols, fmt.Sprintf("ema_dev_%d", n))
	}
	cols = append(cols, "macd", "macd_signal", "macd_hist", "adx_14")
	for _, n := range rsiWindows {
		cols = append(cols, fmt.Sprintf("rsi_%d", n))
	}
	cols = append(cols, "stoch_k_14", "stoch_d_14")
	for _, n := range rocWindows {
		cols = append(cols, fmt.Sprintf("roc_%d", n))
	}
	cols = append(cols, "atr_norm_14", "realized_vol_20", "bb_width_20", "vol_ratio_10_50")
	cols = append(cols, "vol_rel_20", "vol_spike_20", "obv_z_20")
	cols = append(cols, "dow", "days_to_friday", "hour_utc", "session")
	return cols
}

// computeV1 calcule les 34 features causales de colibri_v1_0 et v1_1 sur une série.
//
// Le côté BID sert de référence (le côté ask n'entre que dans la mesure du
// spread, côté backtest). Les lignes de chauffe contiennent des NaN, et les
// ±Inf sont ramenés à NaN : une division par un dénominateur nul ne doit
// pas produire une valeur « énorme » que l'arbre prendrait pour un signal.
func computeV1(series core.Series) *feature.Matrix {
	n := len(series)
	m := feature.NewMatrix(n, columnsV1)
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
			// Impossible par construction : columnsV1 et les écritures
			// ci-dessous sont dans le même fichier. Paniquer ici signale
			// une incohérence de code, pas une donnée fautive.
			panic(err)
		}
		return i
	}

	// --- Prix et retours ---------------------------------------------------
	for _, w := range returnWindows {
		m.SetColumn(col(fmt.Sprintf("ret_log_%d", w)), indicator.Diff(logClose, w))
	}
	lowR := indicator.RollingMin(low, rangeWindow)
	highR := indicator.RollingMax(high, rangeWindow)
	rangePos := make([]float64, n)
	gapOpen := make([]float64, n)
	for i := 0; i < n; i++ {
		span := highR[i] - lowR[i]
		if math.IsNaN(span) || span == 0 {
			rangePos[i] = math.NaN()
		} else {
			rangePos[i] = (closes[i] - lowR[i]) / span
		}
		if i == 0 || closes[i-1] <= 0 || open[i] <= 0 {
			gapOpen[i] = math.NaN()
		} else {
			gapOpen[i] = math.Log(open[i] / closes[i-1])
		}
	}
	m.SetColumn(col("range_pos_20"), rangePos)
	m.SetColumn(col("gap_open"), gapOpen)

	// --- Tendance ----------------------------------------------------------
	for _, w := range smaWindows {
		sma := indicator.RollingMean(closes, w)
		dev := make([]float64, n)
		for i := 0; i < n; i++ {
			if math.IsNaN(sma[i]) || sma[i] == 0 {
				dev[i] = math.NaN()
				continue
			}
			dev[i] = closes[i]/sma[i] - 1.0
		}
		m.SetColumn(col(fmt.Sprintf("sma_dev_%d", w)), dev)
	}
	sma20 := indicator.RollingMean(closes, 20)
	slope := make([]float64, n)
	for i := 0; i < n; i++ {
		if i < 5 || math.IsNaN(sma20[i]) || math.IsNaN(sma20[i-5]) || sma20[i] == 0 {
			slope[i] = math.NaN()
			continue
		}
		slope[i] = (sma20[i] - sma20[i-5]) / sma20[i]
	}
	m.SetColumn(col("sma_slope_20"), slope)

	for _, w := range emaWindows {
		ema := indicator.EWMSpan(closes, w, w)
		dev := make([]float64, n)
		for i := 0; i < n; i++ {
			if math.IsNaN(ema[i]) || ema[i] == 0 {
				dev[i] = math.NaN()
				continue
			}
			dev[i] = closes[i]/ema[i] - 1.0
		}
		m.SetColumn(col(fmt.Sprintf("ema_dev_%d", w)), dev)
	}

	macd, macdSig, macdHist := indicator.MACD(closes, macdFast, macdSlow, macdSignal)
	// Normalisation par le prix : sans elle, un MACD d'EURUSD (1,08) et
	// d'USDJPY (150) ne vivent pas sur la même échelle, et un modèle poolé
	// apprendrait l'instrument au lieu du marché.
	m.SetColumn(col("macd"), divideBy(macd, closes))
	m.SetColumn(col("macd_signal"), divideBy(macdSig, closes))
	m.SetColumn(col("macd_hist"), divideBy(macdHist, closes))
	m.SetColumn(col("adx_14"), indicator.ADX(high, low, closes, adxPeriod))

	// --- Momentum ----------------------------------------------------------
	for _, w := range rsiWindows {
		m.SetColumn(col(fmt.Sprintf("rsi_%d", w)), indicator.RSI(closes, w))
	}
	k, d := indicator.Stochastic(high, low, closes, stochPeriod, stochSmooth)
	m.SetColumn(col("stoch_k_14"), k)
	m.SetColumn(col("stoch_d_14"), d)
	for _, w := range rocWindows {
		roc := indicator.PctChange(closes, w)
		for i := range roc {
			roc[i] *= 100.0
		}
		m.SetColumn(col(fmt.Sprintf("roc_%d", w)), roc)
	}

	// --- Volatilité --------------------------------------------------------
	atr := indicator.ATR(high, low, closes, atrPeriod)
	m.SetColumn(col("atr_norm_14"), divideBy(atr, closes))
	ret1 := indicator.Diff(logClose, 1)
	m.SetColumn(col("realized_vol_20"), indicator.RollingStd(ret1, rvWindow))
	m.SetColumn(col("bb_width_20"), indicator.BollingerWidth(closes, bbWindow))
	rvShort := indicator.RollingStd(ret1, volRatioShort)
	rvLong := indicator.RollingStd(ret1, volRatioLong)
	m.SetColumn(col("vol_ratio_10_50"), divide(rvShort, rvLong))

	// --- Volume (⚠ Dukascopy forex = volume de TICKS, pas réel) ------------
	volMean := indicator.RollingMean(volume, volumeWindow)
	volStd := indicator.RollingStd(volume, volumeWindow)
	volRel := divide(volume, volMean)
	spike := make([]float64, n)
	for i := 0; i < n; i++ {
		switch {
		case math.IsNaN(volMean[i]) || math.IsNaN(volStd[i]):
			spike[i] = math.NaN()
		case volStd[i] == 0:
			// Volume à variance nulle (flux de test, certains indices) :
			// pic = 0, jamais NaN — sinon une colonne entièrement NaN
			// ferait tout sauter au filtrage du jeu d'entraînement.
			spike[i] = 0
		default:
			spike[i] = (volume[i] - volMean[i]) / volStd[i]
		}
	}
	m.SetColumn(col("vol_rel_20"), volRel)
	m.SetColumn(col("vol_spike_20"), spike)
	m.SetColumn(col("obv_z_20"), indicator.OBVZScore(closes, volume, volumeWindow))

	// --- Calendrier / saisonnalité ----------------------------------------
	dow := make([]float64, n)
	daysToFriday := make([]float64, n)
	hour := make([]float64, n)
	session := make([]float64, n)
	for i, bar := range series {
		t := bar.Time.UTC()
		// time.Weekday : dimanche = 0. On réaligne sur la convention
		// ISO/pandas (lundi = 0, vendredi = 4) attendue par days_to_friday.
		wd := (int(t.Weekday()) + 6) % 7
		dow[i] = float64(wd)
		daysToFriday[i] = math.Max(4.0-float64(wd), 0)
		h := t.Hour()
		hour[i] = float64(h)
		// Sessions FX par heure UTC : 0 Asie, 1 Europe, 2 US, 3 creux.
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
	}
	m.SetColumn(col("dow"), dow)
	m.SetColumn(col("days_to_friday"), daysToFriday)
	m.SetColumn(col("hour_utc"), hour)
	m.SetColumn(col("session"), session)

	sanitize(m)
	return m
}

// atrOf expose l'ATR de Wilder BRUT (non normalisé) de la série.
//
// Source de vérité UNIQUE de l'ATR pour tout Colibri : le labeling
// triple-barrier (placement des barrières sur l'historique) ET l'inférence
// (dimensionnement des TP/SL à l'entrée) l'appellent — mêmes valeurs des
// deux côtés, aucune divergence entraînement/exécution possible.
func atrOf(series core.Series) []float64 {
	return indicator.ATR(series.Highs(), series.Lows(), series.Closes(), atrPeriod)
}

func divideBy(num, den []float64) []float64 {
	out := make([]float64, len(num))
	for i := range num {
		if math.IsNaN(num[i]) || i >= len(den) || den[i] == 0 {
			out[i] = math.NaN()
			continue
		}
		out[i] = num[i] / den[i]
	}
	return out
}

func divide(num, den []float64) []float64 {
	out := make([]float64, len(num))
	for i := range num {
		if math.IsNaN(num[i]) || math.IsNaN(den[i]) || den[i] == 0 {
			out[i] = math.NaN()
			continue
		}
		out[i] = num[i] / den[i]
	}
	return out
}

// sanitize ramène tout ±Inf à NaN : une valeur infinie serait traitée par
// l'arbre comme un extrême légitime et créerait un split absurde.
func sanitize(m *feature.Matrix) {
	for i, v := range m.Data {
		if math.IsInf(v, 0) {
			m.Data[i] = math.NaN()
		}
	}
}
