package troglodyte

import (
	"fmt"
	"math"
	"sort"
)

// Estimation des variances par MAXIMUM DE VRAISEMBLANCE.
//
// L'optimisation porte sur (a, b) = (ln qε, ln qζ), rapports à la
// variance du niveau σ²_η : ils sont positifs, leur logarithme ne l'est
// pas, et l'échelle logarithmique est celle où la vraisemblance varie
// régulièrement — qζ vaut couramment un millionième.
//
// Déterministe de bout en bout (grille fixe, puis simplexe de
// Nelder-Mead, sans tirage) : deux entraînements sur les mêmes données
// donnent le même modèle au bit près, comme le reste du projet.

// Bornes de recherche, en logarithme népérien.
//
// Près de la borne BASSE, une variance est négligeable devant celle du
// niveau : σ²_ε nul (le prix est sa propre tendance, sans bruit
// d'observation — le cas attendu d'un taux de change) ou σ²_ζ nul (pente
// constante). Ce sont des solutions au bord, légitimes ; le manifeste dit
// si la vraisemblance distingue la variance de zéro. La borne HAUTE, elle,
// dit que le niveau ne bouge presque pas devant le bruit ou la pente : le
// modèle décrit mal la série, et c'est signalé comme tel.
var (
	boundA = [2]float64{-30, 10}
	boundB = [2]float64{-40, 5}
)

// Grille de départ : le simplexe part du meilleur de ces points, ce qui
// évite de s'arrêter sur un optimum local loin du bon bassin.
var (
	gridA = span(-28, 8, 3)
	gridB = span(-36, 4, 4)
)

const (
	maxIterations = 500
	// tolérances d'arrêt du simplexe : écart relatif des valeurs et taille
	// du simplexe, dans l'espace (a, b).
	valueTol = 1e-10
	sizeTol  = 1e-7
	// boundMargin : distance, en logarithme, en deçà de laquelle un
	// optimum est déclaré collé à une borne HAUTE.
	boundMargin = 0.5
	// zeroLR : valeur critique du test du rapport de vraisemblance d'une
	// variance NULLE, au seuil de 5 %. Sous l'hypothèse nulle, le
	// paramètre est au bord de son domaine et la statistique 2 (ln L* −
	// ln L₀) suit le mélange ½ χ²₀ + ½ χ²₁ (Self et Liang, 1987,
	// « Asymptotic Properties of Maximum Likelihood Estimators and
	// Likelihood Ratio Tests Under Nonstandard Conditions », JASA 82(398)) :
	// P(χ²₁ > 2,71) = 0,10, dont la moitié fait 5 %.
	zeroLR = 2.71
)

// fit : résultat d'une estimation.
type fit struct {
	Params params
	// LogL : log-vraisemblance maximale (diffuse, sur Obs innovations).
	LogL       float64
	Obs        int
	Iterations int
	// NegligibleEps, NegligibleZeta : la vraisemblance ne distingue pas
	// σ²_ε (resp. σ²_ζ) de ZÉRO au seuil de 5 % (test du rapport de
	// vraisemblance, zeroLR). Constat légitime, pas une panne : un taux de
	// change n'a presque pas de bruit d'observation.
	NegligibleEps, NegligibleZeta bool
	// AtUpperBound : l'optimum touche une borne HAUTE — le modèle décrit
	// mal la série. Écrit dans le manifeste, jamais maquillé.
	AtUpperBound bool
}

// estimate ajuste le modèle sur y (logarithmes des prix).
func estimate(y []float64) (fit, error) {
	if len(y) < 3 {
		return fit{}, fmt.Errorf("%d observations : il en faut au moins 3", len(y))
	}
	objective := func(x [2]float64) float64 {
		if x[0] < boundA[0] || x[0] > boundA[1] || x[1] < boundB[0] || x[1] > boundB[1] {
			return math.Inf(1)
		}
		logL, _ := concentrated(y, math.Exp(x[0]), math.Exp(x[1]))
		if math.IsNaN(logL) {
			return math.Inf(1)
		}
		return -logL
	}

	best, bestVal := [2]float64{}, math.Inf(1)
	for _, a := range gridA {
		for _, b := range gridB {
			if v := objective([2]float64{a, b}); v < bestVal {
				best, bestVal = [2]float64{a, b}, v
			}
		}
	}
	if math.IsInf(bestVal, 1) {
		return fit{}, fmt.Errorf("vraisemblance non calculable sur cette série (prix constants ?)")
	}

	x, iterations := nelderMead(objective, best, 1.0)
	logL, sigma2Eta := concentrated(y, math.Exp(x[0]), math.Exp(x[1]))
	// Variance « nulle » = rapport à sa borne basse (e⁻³⁰ ou e⁻⁴⁰ fois
	// σ²_η), l'autre rapport restant à son optimum. Pour un vrai test, il
	// faudrait réoptimiser l'autre rapport sous la contrainte ; le garder
	// fixe donne un ln L₀ plus bas, donc un test qui déclare « nul » moins
	// souvent — prudent dans le bon sens.
	zeroEps, _ := concentrated(y, math.Exp(boundA[0]), math.Exp(x[1]))
	zeroZeta, _ := concentrated(y, math.Exp(x[0]), math.Exp(boundB[0]))
	return fit{
		Params: params{
			Eps:  math.Exp(x[0]) * sigma2Eta,
			Eta:  sigma2Eta,
			Zeta: math.Exp(x[1]) * sigma2Eta,
		},
		LogL:           logL,
		Obs:            len(y) - 2,
		Iterations:     iterations,
		NegligibleEps:  2*(logL-zeroEps) < zeroLR,
		NegligibleZeta: 2*(logL-zeroZeta) < zeroLR,
		AtUpperBound:   boundA[1]-x[0] < boundMargin || boundB[1]-x[1] < boundMargin,
	}, nil
}

// nelderMead minimise f en deux dimensions (Nelder et Mead, 1965, « A
// Simplex Method for Function Minimization », The Computer Journal 7(4)),
// avec les coefficients usuels : réflexion 1, expansion 2, contraction ½,
// rétrécissement ½.
func nelderMead(f func([2]float64) float64, start [2]float64, step float64) ([2]float64, int) {
	type vertex struct {
		x [2]float64
		v float64
	}
	simplex := []vertex{
		{start, f(start)},
		{[2]float64{start[0] + step, start[1]}, 0},
		{[2]float64{start[0], start[1] + step}, 0},
	}
	simplex[1].v = f(simplex[1].x)
	simplex[2].v = f(simplex[2].x)

	lerp := func(a, b [2]float64, t float64) [2]float64 {
		return [2]float64{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])}
	}
	it := 0
	for ; it < maxIterations; it++ {
		sort.SliceStable(simplex, func(i, j int) bool { return simplex[i].v < simplex[j].v })
		bestV, worstV := simplex[0].v, simplex[2].v
		size := math.Max(dist(simplex[0].x, simplex[1].x), dist(simplex[0].x, simplex[2].x))
		if !math.IsInf(worstV, 1) && worstV-bestV <= valueTol*(1+math.Abs(bestV)) && size < sizeTol {
			break
		}
		centroid := lerp(simplex[0].x, simplex[1].x, 0.5)
		worst := simplex[2]

		reflected := lerp(worst.x, centroid, 2) // centroïde + (centroïde − pire)
		rv := f(reflected)
		switch {
		case rv < simplex[0].v:
			expanded := lerp(worst.x, centroid, 3) // centroïde + 2 (centroïde − pire)
			if ev := f(expanded); ev < rv {
				simplex[2] = vertex{expanded, ev}
			} else {
				simplex[2] = vertex{reflected, rv}
			}
		case rv < simplex[1].v:
			simplex[2] = vertex{reflected, rv}
		default:
			// Contraction : du côté du réfléchi s'il bat le pire, sinon
			// vers l'intérieur.
			var contracted [2]float64
			if rv < worst.v {
				contracted = lerp(centroid, reflected, 0.5)
			} else {
				contracted = lerp(centroid, worst.x, 0.5)
			}
			if cv := f(contracted); cv < math.Min(rv, worst.v) {
				simplex[2] = vertex{contracted, cv}
				continue
			}
			for i := 1; i < 3; i++ {
				simplex[i].x = lerp(simplex[0].x, simplex[i].x, 0.5)
				simplex[i].v = f(simplex[i].x)
			}
		}
	}
	sort.SliceStable(simplex, func(i, j int) bool { return simplex[i].v < simplex[j].v })
	return simplex[0].x, it
}

func dist(a, b [2]float64) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

func span(from, to, step float64) []float64 {
	var out []float64
	for v := from; v <= to+1e-9; v += step {
		out = append(out, v)
	}
	return out
}
