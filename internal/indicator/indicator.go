// Package indicator fournit les indicateurs techniques du projet, tous
// STRICTEMENT CAUSAUX : la valeur à l'indice t n'utilise que des données
// d'indice <= t. Aucune fenêtre centrée, aucun décalage négatif.
//
// C'est la propriété qui rend l'entraînement honnête : une feature calculée
// au close de t doit être connue au moment de décider à ce close. Seul le
// LABEL a le droit de regarder vers l'avant (cf. package label).
//
// Convention de sortie : un vecteur de MÊME longueur que l'entrée, avec
// NaN pendant la période de chauffe — jamais de zéro (un zéro se confond
// avec une valeur légitime et contamine silencieusement l'apprentissage).
//
// Convention de chauffe : une fenêtre glissante de n exige n observations
// avant de produire une valeur ; une moyenne exponentielle démarre sa
// récursion dès la première observation puis masque ses n-1 premières
// sorties. C'est la sémantique usuelle des bibliothèques d'analyse de
// séries, et elle est tenue à l'identique ici.
package indicator

import "math"

// NaN est la valeur « indisponible » unique du projet.
var NaN = math.NaN()

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = NaN
	}
	return out
}

// Diff renvoie x[t] - x[t-n].
func Diff(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n; i < len(x); i++ {
		out[i] = x[i] - x[i-n]
	}
	return out
}

// PctChange renvoie (x[t] - x[t-n]) / x[t-n].
func PctChange(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n; i < len(x); i++ {
		if x[i-n] == 0 {
			continue
		}
		out[i] = x[i]/x[i-n] - 1.0
	}
	return out
}

// Log applique le logarithme naturel terme à terme (NaN si <= 0).
func Log(x []float64) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		if v <= 0 || math.IsNaN(v) {
			out[i] = NaN
			continue
		}
		out[i] = math.Log(v)
	}
	return out
}

// resyncInterval : toutes les N fenêtres, les accumulateurs glissants sont
// RECALCULÉS depuis les valeurs de la fenêtre.
//
// Une somme entretenue par ajouts et retraits successifs dérive : sur cinq
// millions de bougies M1, les erreurs d'arrondi s'accumulent en marche
// aléatoire. Le recalcul périodique borne cette dérive une fois pour
// toutes, pour un coût négligeable (une fenêtre tous les 4096 points).
const resyncInterval = 4096

// moments entretient somme et somme des carrés d'une fenêtre glissante.
//
// Les valeurs sont accumulées DÉCALÉES d'une référence (shift) proche de
// la moyenne. Sans ce décalage, la variance se calcule par
// `sumSq - sum²/n`, une soustraction de deux grands nombres presque égaux
// qui perd presque tous ses chiffres significatifs sur une série non
// centrée — l'OBV, par exemple, dont le cumul atteint plusieurs millions.
type moments struct {
	sum   float64
	sumSq float64
	shift float64
	nans  int
}

func (m *moments) add(v float64) {
	if math.IsNaN(v) {
		m.nans++
		return
	}
	d := v - m.shift
	m.sum += d
	m.sumSq += d * d
}

func (m *moments) remove(v float64) {
	if math.IsNaN(v) {
		m.nans--
		return
	}
	d := v - m.shift
	m.sum -= d
	m.sumSq -= d * d
}

// resync recalcule tout depuis la fenêtre `w`, en recentrant la référence.
func (m *moments) resync(w []float64) {
	m.sum, m.sumSq, m.nans = 0, 0, 0
	var total float64
	var count int
	for _, v := range w {
		if math.IsNaN(v) {
			m.nans++
			continue
		}
		total += v
		count++
	}
	if count > 0 {
		m.shift = total / float64(count)
	}
	for _, v := range w {
		if math.IsNaN(v) {
			continue
		}
		d := v - m.shift
		m.sum += d
		m.sumSq += d * d
	}
}

// variance d'échantillon (ddof = 1), avec garde contre les valeurs
// légèrement négatives dues à l'arrondi.
func (m *moments) variance(window int) float64 {
	n := float64(window)
	v := (m.sumSq - m.sum*m.sum/n) / (n - 1)
	if v < 0 {
		return 0
	}
	return v
}

// RollingMean : moyenne glissante sur `window` observations.
//
// Somme simple entretenue par ajouts et retraits, recalculée
// périodiquement pour borner la dérive d'arrondi. Pas de recentrage ici,
// contrairement à RollingStd : une moyenne ne soustrait pas deux grands
// nombres presque égaux, elle n'a donc rien à y gagner — et le recentrage
// lui coûterait près du double de son temps de calcul.
func RollingMean(x []float64, window int) []float64 {
	out := nanSlice(len(x))
	if window < 1 {
		return out
	}
	var sum float64
	var nans, since int
	for i := range x {
		if v := x[i]; math.IsNaN(v) {
			nans++
		} else {
			sum += v
		}
		if i >= window {
			if v := x[i-window]; math.IsNaN(v) {
				nans--
			} else {
				sum -= v
			}
		}
		if i < window-1 {
			continue
		}
		if since++; since >= resyncInterval {
			sum, nans, since = 0, 0, 0
			for _, v := range x[i-window+1 : i+1] {
				if math.IsNaN(v) {
					nans++
				} else {
					sum += v
				}
			}
		}
		if nans == 0 {
			out[i] = sum / float64(window)
		}
	}
	return out
}

// RollingStd : écart-type glissant, ddof=1.
func RollingStd(x []float64, window int) []float64 {
	out := nanSlice(len(x))
	if window < 2 {
		return out
	}
	var m moments
	since := resyncInterval // cf. RollingMean : recentrage dès la première fenêtre
	for i := range x {
		m.add(x[i])
		if i >= window {
			m.remove(x[i-window])
		}
		if i < window-1 {
			continue
		}
		if since++; since >= resyncInterval {
			m.resync(x[i-window+1 : i+1])
			since = 0
		}
		if m.nans == 0 {
			out[i] = math.Sqrt(m.variance(window))
		}
	}
	return out
}

// RollingMin / RollingMax : extrema glissants en temps CONSTANT amorti.
//
// La version naïve rebalaye la fenêtre à chaque point : O(n × fenêtre).
// Sur une année de M1 avec une fenêtre de 50, c'est vingt fois le coût
// d'une moyenne glissante — et ces extrema sont appelés quatre fois par
// calcul de features (range, stochastique).
//
// La file monotone ci-dessous ne garde que les candidats qui peuvent
// encore devenir l'extremum : chaque indice y entre et en sort au plus une
// fois, donc le coût total est linéaire.
func RollingMin(x []float64, window int) []float64 { return rollingExtrema(x, window, true) }
func RollingMax(x []float64, window int) []float64 { return rollingExtrema(x, window, false) }

func rollingExtrema(x []float64, window int, min bool) []float64 {
	out := nanSlice(len(x))
	if window < 1 {
		return out
	}
	// Tampon circulaire d'indices, de taille fixe : aucune allocation
	// pendant le parcours.
	ring := make([]int32, window+1)
	head, size := 0, 0
	push := func(i int) {
		ring[(head+size)%len(ring)] = int32(i)
		size++
	}
	back := func() int { return int(ring[(head+size-1)%len(ring)]) }
	popBack := func() { size-- }
	front := func() int { return int(ring[head]) }
	popFront := func() {
		head = (head + 1) % len(ring)
		size--
	}

	// lastNaN : indice du NaN le plus récent. Tant qu'il est DANS la
	// fenêtre, la sortie reste NaN — la convention de chauffe du projet.
	lastNaN := -1
	for i := range x {
		if math.IsNaN(x[i]) {
			lastNaN = i
		} else {
			for size > 0 {
				b := x[back()]
				if (min && b >= x[i]) || (!min && b <= x[i]) {
					popBack()
					continue
				}
				break
			}
			push(i)
		}
		for size > 0 && front() <= i-window {
			popFront()
		}
		if i >= window-1 && lastNaN <= i-window && size > 0 {
			out[i] = x[front()]
		}
	}
	return out
}

// EWMAlpha : moyenne exponentielle récursive y_t = (1-α)·y_{t-1} + α·x_t,
// équivalente à `pandas.ewm(alpha=α, adjust=False, min_periods=p)`.
//
// Les NaN de TÊTE sont ignorés : la récursion démarre à la première
// observation valide (c'est exactement le cas rencontré — un `diff` crée
// un NaN initial). Un NaN au milieu conserve l'état sans le compter, ce qui
// ne devrait pas arriver sur des données assainies.
func EWMAlpha(x []float64, alpha float64, minPeriods int) []float64 {
	out := nanSlice(len(x))
	var state float64
	started := false
	seen := 0
	for i, v := range x {
		if math.IsNaN(v) {
			continue
		}
		if !started {
			state, started = v, true
		} else {
			state = (1-alpha)*state + alpha*v
		}
		seen++
		if seen >= minPeriods {
			out[i] = state
		}
	}
	return out
}

// EWMSpan : même chose paramétrée par un « span » (α = 2/(span+1)).
func EWMSpan(x []float64, span int, minPeriods int) []float64 {
	return EWMAlpha(x, 2.0/float64(span+1), minPeriods)
}

// WilderEMA : lissage de Wilder (α = 1/period), base de RSI, ATR et ADX.
func WilderEMA(x []float64, period int, minPeriods int) []float64 {
	return EWMAlpha(x, 1.0/float64(period), minPeriods)
}

// RSI de Wilder sur `period` observations. Premier point non-NaN à
// l'indice `period` (le diff consomme la première bougie).
func RSI(closes []float64, period int) []float64 {
	delta := Diff(closes, 1)
	gain := make([]float64, len(delta))
	loss := make([]float64, len(delta))
	for i, d := range delta {
		if math.IsNaN(d) {
			gain[i], loss[i] = NaN, NaN
			continue
		}
		gain[i] = math.Max(d, 0)
		loss[i] = math.Max(-d, 0)
	}
	avgGain := WilderEMA(gain, period, period)
	avgLoss := WilderEMA(loss, period, period)
	out := nanSlice(len(closes))
	for i := range out {
		if math.IsNaN(avgGain[i]) || math.IsNaN(avgLoss[i]) {
			continue
		}
		if avgLoss[i] == 0 {
			// Aucune baisse sur la fenêtre : RSI saturé à 100 (limite de
			// 100 - 100/(1+rs) quand rs → +∞). NaN serait faux.
			if avgGain[i] == 0 {
				out[i] = 50 // ni hausse ni baisse : neutre.
			} else {
				out[i] = 100
			}
			continue
		}
		rs := avgGain[i] / avgLoss[i]
		out[i] = 100.0 - 100.0/(1.0+rs)
	}
	return out
}

// TrueRange de Wilder : max(H-L, |H-C_{t-1}|, |L-C_{t-1}|).
func TrueRange(high, low, closes []float64) []float64 {
	out := nanSlice(len(closes))
	for i := range closes {
		if i == 0 {
			// Sans close précédent, le TR se réduit à H-L (convention
			// Wilder). Renvoyer NaN décalerait tout l'ATR d'une bougie.
			out[i] = high[i] - low[i]
			continue
		}
		prev := closes[i-1]
		tr := high[i] - low[i]
		if v := math.Abs(high[i] - prev); v > tr {
			tr = v
		}
		if v := math.Abs(low[i] - prev); v > tr {
			tr = v
		}
		out[i] = tr
	}
	return out
}

// ATR de Wilder, en unités de PRIX (non normalisé).
func ATR(high, low, closes []float64, period int) []float64 {
	return WilderEMA(TrueRange(high, low, closes), period, period)
}

// ADX de Wilder : force de la tendance, indépendante de son sens.
func ADX(high, low, closes []float64, period int) []float64 {
	n := len(closes)
	plusDM := make([]float64, n)
	minusDM := make([]float64, n)
	for i := 1; i < n; i++ {
		up := high[i] - high[i-1]
		down := low[i-1] - low[i]
		if up > down && up > 0 {
			plusDM[i] = up
		}
		if down > up && down > 0 {
			minusDM[i] = down
		}
	}
	atr := ATR(high, low, closes, period)
	alpha := 1.0 / float64(period)
	plusSm := EWMAlpha(plusDM, alpha, 1)
	minusSm := EWMAlpha(minusDM, alpha, 1)

	dx := nanSlice(n)
	for i := 0; i < n; i++ {
		if math.IsNaN(atr[i]) || atr[i] == 0 {
			continue
		}
		plusDI := 100.0 * plusSm[i] / atr[i]
		minusDI := 100.0 * minusSm[i] / atr[i]
		sum := plusDI + minusDI
		if sum == 0 {
			continue
		}
		dx[i] = 100.0 * math.Abs(plusDI-minusDI) / sum
	}
	return EWMAlpha(dx, alpha, period)
}

// MACD renvoie (macd, signal, histogramme).
func MACD(closes []float64, fast, slow, signal int) (macd, sig, hist []float64) {
	emaFast := EWMSpan(closes, fast, fast)
	emaSlow := EWMSpan(closes, slow, slow)
	macd = nanSlice(len(closes))
	for i := range closes {
		if math.IsNaN(emaFast[i]) || math.IsNaN(emaSlow[i]) {
			continue
		}
		macd[i] = emaFast[i] - emaSlow[i]
	}
	sig = EWMSpan(macd, signal, signal)
	hist = nanSlice(len(closes))
	for i := range closes {
		if math.IsNaN(macd[i]) || math.IsNaN(sig[i]) {
			continue
		}
		hist[i] = macd[i] - sig[i]
	}
	return macd, sig, hist
}

// Stochastic renvoie %K (brut) et %D (moyenne de %K sur `smooth`).
func Stochastic(high, low, closes []float64, period, smooth int) (k, d []float64) {
	lowN := RollingMin(low, period)
	highN := RollingMax(high, period)
	k = nanSlice(len(closes))
	for i := range closes {
		if math.IsNaN(lowN[i]) || math.IsNaN(highN[i]) {
			continue
		}
		span := highN[i] - lowN[i]
		if span == 0 {
			// Marché plat sur la fenêtre : %K conventionnellement à 50
			// (milieu du canal), plutôt qu'une division par zéro.
			k[i] = 50
			continue
		}
		k[i] = 100.0 * (closes[i] - lowN[i]) / span
	}
	return k, RollingMean(k, smooth)
}

// BollingerWidth : (bande haute - bande basse) / moyenne, bandes à ±2σ.
func BollingerWidth(closes []float64, window int) []float64 {
	mean := RollingMean(closes, window)
	std := RollingStd(closes, window)
	out := nanSlice(len(closes))
	for i := range closes {
		if math.IsNaN(mean[i]) || math.IsNaN(std[i]) || mean[i] == 0 {
			continue
		}
		out[i] = 4.0 * std[i] / mean[i]
	}
	return out
}

// OBVZScore : On-Balance Volume ramené à un z-score glissant.
// L'OBV brut est non stationnaire (cumul sans borne) : tel quel, il fait
// apprendre au modèle la DATE plutôt que le marché.
func OBVZScore(closes, volume []float64, window int) []float64 {
	obv := make([]float64, len(closes))
	var acc float64
	for i := range closes {
		if i > 0 {
			switch {
			case closes[i] > closes[i-1]:
				acc += volume[i]
			case closes[i] < closes[i-1]:
				acc -= volume[i]
			}
		}
		obv[i] = acc
	}
	mean := RollingMean(obv, window)
	std := RollingStd(obv, window)
	out := nanSlice(len(closes))
	for i := range closes {
		if math.IsNaN(mean[i]) || math.IsNaN(std[i]) || std[i] == 0 {
			continue
		}
		out[i] = (obv[i] - mean[i]) / std[i]
	}
	return out
}

// Median renvoie la médiane d'un échantillon (les NaN sont écartés).
// Utilisée pour mesurer le spread : la moyenne serait tirée par les
// élargissements d'ouverture et d'annonce, et surestimerait le coût du
// régime normal.
func Median(values []float64) float64 {
	clean := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return NaN
	}
	quickSelectSort(clean)
	mid := len(clean) / 2
	if len(clean)%2 == 1 {
		return clean[mid]
	}
	return (clean[mid-1] + clean[mid]) / 2
}
