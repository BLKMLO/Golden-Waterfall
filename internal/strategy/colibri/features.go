package colibri

import (
	"math"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/indicator"
)

// --- Fenêtres : elles font partie de la DÉFINITION du modèle Colibri ---
//
// Ce ne sont PAS des réglages runtime. Les changer change le modèle, donc
// sa version. Elles n'ont rien à faire dans config.yaml.
//
// ⚠ Elles appartiennent au jeu de colibri_v1_2 : en modifier une
// changerait une révision publiée. Une révision future qui veut d'autres
// fenêtres déclare les siennes.
var (
	returnWindows = []int{1, 3, 5, 10, 20}
	smaWindows    = []int{10, 20, 50}
	emaWindows    = []int{12, 26}
	rsiWindows    = []int{7, 14}
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

// atrOf expose l'ATR de Wilder BRUT (non normalisé) de la série.
//
// Source de vérité UNIQUE de l'ATR pour tout Colibri : le labeling
// par côté (placement des barrières sur l'historique) ET l'inférence
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
