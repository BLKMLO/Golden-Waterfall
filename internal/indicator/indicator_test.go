package indicator

import (
	"math"
	"math/rand"
	"testing"
)

func nearly(t *testing.T, got, want float64, tol float64, what string) {
	t.Helper()
	if math.IsNaN(want) {
		if !math.IsNaN(got) {
			t.Fatalf("%s : NaN attendu, reçu %v", what, got)
		}
		return
	}
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Fatalf("%s : %v attendu, reçu %v", what, want, got)
	}
}

func TestRollingMeanWarmup(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	got := RollingMean(x, 3)
	for i := 0; i < 2; i++ {
		if !math.IsNaN(got[i]) {
			t.Fatalf("les %d premières valeurs doivent être NaN (chauffe), reçu %v à %d", 2, got[i], i)
		}
	}
	nearly(t, got[2], 2, 1e-12, "moyenne 1,2,3")
	nearly(t, got[4], 4, 1e-12, "moyenne 3,4,5")
}

func TestRollingStdMatchesDdof1(t *testing.T) {
	// Écart-type d'échantillon (ddof = 1) de 2,4,4,4,5,5,7,9 = 2,13809…
	x := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	got := RollingStd(x, 8)
	nearly(t, got[7], 2.1380899352993947, 1e-12, "écart-type ddof=1")
}

func TestEWMAlphaIsRecursive(t *testing.T) {
	x := []float64{10, 20, 30}
	got := EWMAlpha(x, 0.5, 1)
	nearly(t, got[0], 10, 1e-12, "premier point = observation")
	nearly(t, got[1], 15, 1e-12, "0,5·10 + 0,5·20")
	nearly(t, got[2], 22.5, 1e-12, "0,5·15 + 0,5·30")
}

func TestEWMAlphaSkipsLeadingNaN(t *testing.T) {
	x := []float64{math.NaN(), 10, 20}
	got := EWMAlpha(x, 0.5, 1)
	if !math.IsNaN(got[0]) {
		t.Fatal("un NaN d'entrée ne peut pas produire une sortie")
	}
	nearly(t, got[1], 10, 1e-12, "la récursion démarre à la première observation")
	nearly(t, got[2], 15, 1e-12, "puis continue normalement")
}

func TestRSIExtremes(t *testing.T) {
	// Série strictement croissante : aucune baisse → RSI saturé à 100.
	up := make([]float64, 40)
	for i := range up {
		up[i] = float64(i + 1)
	}
	got := RSI(up, 14)
	nearly(t, got[39], 100, 1e-9, "RSI d'une hausse continue")

	// Série constante : ni hausse ni baisse → 50, jamais NaN.
	flat := make([]float64, 40)
	for i := range flat {
		flat[i] = 7
	}
	nearly(t, RSI(flat, 14)[39], 50, 1e-9, "RSI d'un marché plat")
}

func TestRSIWarmupLength(t *testing.T) {
	x := make([]float64, 30)
	for i := range x {
		x[i] = float64(i%5) + 1
	}
	got := RSI(x, 14)
	for i := 0; i < 14; i++ {
		if !math.IsNaN(got[i]) {
			t.Fatalf("RSI défini trop tôt : indice %d", i)
		}
	}
	if math.IsNaN(got[14]) {
		t.Fatal("RSI doit être défini à l'indice = période (le diff consomme la première bougie)")
	}
}

func TestTrueRangeFirstBarUsesHighLow(t *testing.T) {
	high := []float64{10, 12}
	low := []float64{8, 9}
	closes := []float64{9, 11}
	got := TrueRange(high, low, closes)
	nearly(t, got[0], 2, 1e-12, "TR de la première bougie = H-L")
	nearly(t, got[1], 3, 1e-12, "TR = max(3, |12-9|, |9-9|)")
}

func TestStochasticFlatMarketIsNeutral(t *testing.T) {
	n := 30
	h := make([]float64, n)
	l := make([]float64, n)
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		h[i], l[i], c[i] = 5, 5, 5
	}
	k, _ := Stochastic(h, l, c, 14, 3)
	nearly(t, k[20], 50, 1e-12, "%K d'un canal d'amplitude nulle")
}

func TestMedianIgnoresNaN(t *testing.T) {
	nearly(t, Median([]float64{3, math.NaN(), 1, 2}), 2, 1e-12, "médiane de 1,2,3")
	nearly(t, Median([]float64{4, 1, 3, 2}), 2.5, 1e-12, "médiane paire")
	if !math.IsNaN(Median(nil)) {
		t.Fatal("médiane d'un échantillon vide : NaN attendu")
	}
}

func TestOBVZScoreIsStationary(t *testing.T) {
	n := 100
	closes := make([]float64, n)
	volume := make([]float64, n)
	for i := range closes {
		closes[i] = 1 + float64(i)*0.01 // hausse continue → OBV explose
		volume[i] = 100
	}
	got := OBVZScore(closes, volume, 20)
	last := got[n-1]
	if math.IsNaN(last) {
		t.Fatal("z-score de l'OBV indéfini en fin de série")
	}
	// Le z-score borne l'OBV : sans normalisation, la valeur brute
	// croîtrait sans limite et le modèle apprendrait la DATE.
	if math.Abs(last) > 10 {
		t.Fatalf("z-score non borné : %v", last)
	}
}

func TestBollingerWidthIsZeroOnFlatMarket(t *testing.T) {
	x := make([]float64, 40)
	for i := range x {
		x[i] = 2
	}
	nearly(t, BollingerWidth(x, 20)[30], 0, 1e-12, "largeur des bandes sur un marché plat")
}

// --- Équivalence avec une référence naïve --------------------------------
//
// Les versions optimisées (file monotone pour les extrema, accumulateurs
// pour moyenne et écart-type) doivent donner EXACTEMENT les mêmes valeurs
// que le calcul direct. Sans cette comparaison, une optimisation qui
// change discrètement un résultat passerait inaperçue : les tests
// fonctionnels ne regardent que quelques points.

func naiveExtrema(x []float64, window int, min bool) []float64 {
	out := nanSlice(len(x))
	for i := window - 1; i < len(x); i++ {
		best, bad := NaN, false
		for j := i - window + 1; j <= i; j++ {
			if math.IsNaN(x[j]) {
				bad = true
				break
			}
			if math.IsNaN(best) || (min && x[j] < best) || (!min && x[j] > best) {
				best = x[j]
			}
		}
		if !bad {
			out[i] = best
		}
	}
	return out
}

func naiveMeanStd(x []float64, window int) (means, stds []float64) {
	means, stds = nanSlice(len(x)), nanSlice(len(x))
	for i := window - 1; i < len(x); i++ {
		w := x[i-window+1 : i+1]
		bad := false
		var sum float64
		for _, v := range w {
			if math.IsNaN(v) {
				bad = true
				break
			}
			sum += v
		}
		if bad {
			continue
		}
		mean := sum / float64(window)
		means[i] = mean
		var ss float64
		for _, v := range w {
			d := v - mean
			ss += d * d
		}
		stds[i] = math.Sqrt(ss / float64(window-1))
	}
	return means, stds
}

func sameSeries(t *testing.T, name string, got, want []float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s : %d valeurs contre %d", name, len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if math.IsNaN(g) != math.IsNaN(w) {
			t.Fatalf("%s à l'indice %d : %v contre %v (présence différente)", name, i, g, w)
		}
		if math.IsNaN(w) {
			continue
		}
		if math.Abs(g-w) > tol*math.Max(1, math.Abs(w)) {
			t.Fatalf("%s à l'indice %d : %.15g contre %.15g", name, i, g, w)
		}
	}
}

func TestRollingExtremaMatchNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 5, 50, 4000} {
		x := make([]float64, n)
		for i := range x {
			// Des NaN dispersés, y compris en fin de série, pour éprouver
			// la fenêtre de chauffe autant que le régime établi.
			if rng.Float64() < 0.07 {
				x[i] = math.NaN()
				continue
			}
			x[i] = rng.NormFloat64() * 10
		}
		for _, w := range []int{1, 2, 3, 20, 50} {
			if w > n {
				continue
			}
			sameSeries(t, "RollingMin", RollingMin(x, w), naiveExtrema(x, w, true), 0)
			sameSeries(t, "RollingMax", RollingMax(x, w), naiveExtrema(x, w, false), 0)
		}
	}
}

func TestRollingMeanStdMatchNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	// Deux régimes : des valeurs centrées, et des valeurs très décalées
	// (comme un OBV cumulé) où la soustraction de grands nombres presque
	// égaux détruit la précision si elle n'est pas recentrée.
	for _, offset := range []float64{0, 1e8} {
		x := make([]float64, 20000)
		for i := range x {
			if rng.Float64() < 0.03 {
				x[i] = math.NaN()
				continue
			}
			x[i] = offset + rng.NormFloat64()*3
		}
		for _, w := range []int{2, 20, 50} {
			means, stds := naiveMeanStd(x, w)
			sameSeries(t, "RollingMean", RollingMean(x, w), means, 1e-12)
			sameSeries(t, "RollingStd", RollingStd(x, w), stds, 1e-9)
		}
	}
}

// TestRollingStdSurvivesLargeOffsets : sur une série fortement décalée,
// la formule « somme des carrés moins carré de la somme » perd tous ses
// chiffres significatifs. Le recentrage doit rendre le résultat correct.
func TestRollingStdSurvivesLargeOffsets(t *testing.T) {
	const offset = 1e9
	x := make([]float64, 500)
	for i := range x {
		x[i] = offset + float64(i%4)
	}
	got := RollingStd(x, 4)
	_, want := naiveMeanStd(x, 4)
	for i := 400; i < len(x); i++ {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			t.Fatalf("indice %d : écart-type %.9g, attendu %.9g", i, got[i], want[i])
		}
	}
}
