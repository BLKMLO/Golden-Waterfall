package troglodyte

import (
	"math"
	"math/rand"
	"testing"
)

// simulate tire une trajectoire du modèle à tendance locale linéaire.
func simulate(n int, p params, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	y := make([]float64, n)
	mu, beta := math.Log(1.1), 0.0
	for t := range y {
		y[t] = mu + rng.NormFloat64()*math.Sqrt(p.Eps)
		mu += beta + rng.NormFloat64()*math.Sqrt(p.Eta)
		beta += rng.NormFloat64() * math.Sqrt(p.Zeta)
	}
	return y
}

// Ordres de grandeur d'un logarithme de prix en H4 : bruit et niveau de
// l'ordre du millième, pente qui dérive lentement.
var truth = params{Eps: 1e-6, Eta: 4e-6, Zeta: 1e-10}

func TestEstimateFindsAtLeastTheTrueLikelihood(t *testing.T) {
	y := simulate(4000, truth, 1)
	f, err := estimate(y)
	if err != nil {
		t.Fatal(err)
	}
	trueL, _ := concentrated(y, truth.Eps/truth.Eta, truth.Zeta/truth.Eta)
	// Critère qui ne dépend d'aucune tolérance choisie : le maximum trouvé
	// ne peut pas être plus bas que la vraisemblance au VRAI point.
	if f.LogL < trueL-1e-6 {
		t.Fatalf("optimum %v inférieur à la vraisemblance au vrai point %v", f.LogL, trueL)
	}
	t.Logf("vrai : %+v", truth)
	t.Logf("estimé : %+v (itérations %d)", f.Params, f.Iterations)
	if f.NegligibleEps || f.NegligibleZeta || f.AtUpperBound {
		t.Fatalf("aucune variance n'est nulle ici, aucun bord ne doit être atteint : %+v", f)
	}
	for name, pair := range map[string][2]float64{
		"σ²_ε": {f.Params.Eps, truth.Eps},
		"σ²_η": {f.Params.Eta, truth.Eta},
		"σ²_ζ": {f.Params.Zeta, truth.Zeta},
	} {
		ratio := pair[0] / pair[1]
		t.Logf("%s estimé / vrai = %.3f", name, ratio)
		// Mesuré sur cette graine : 1,066 / 1,058 / 1,132. La tolérance
		// [0,5 ; 2] laisse de la marge à une autre plateforme sans laisser
		// passer une estimation fausse d'un ordre de grandeur.
		if ratio < 0.5 || ratio > 2 {
			t.Errorf("%s : rapport estimé/vrai %.3f hors de [0,5 ; 2]", name, ratio)
		}
	}
}

func TestEstimateIsDeterministic(t *testing.T) {
	y := simulate(1500, truth, 2)
	a, err := estimate(y)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := estimate(y)
	if a != b {
		t.Fatalf("deux estimations sur les mêmes données diffèrent :\n%+v\n%+v", a, b)
	}
}

func TestEstimateRefusesDegenerateSeries(t *testing.T) {
	if _, err := estimate([]float64{1, 2}); err == nil {
		t.Fatal("deux observations ne suffisent pas")
	}
	flat := make([]float64, 100)
	if _, err := estimate(flat); err == nil {
		t.Fatal("une série constante n'a pas de vraisemblance calculable : il faut le dire")
	}
}

// Une marche aléatoire SANS bruit d'observation (σ²_ε = 0), à dérive qui
// varie : le cas attendu d'un taux de change. L'estimation doit le
// reconnaître comme une solution au bord — et estimer quand même σ²_η et
// σ²_ζ, qui n'en dépendent pas. C'est ce qu'a révélé le premier essai du
// binaire : rapportées à σ²_ε, les variances butaient sur les bornes, et
// l'estimation ne tenait plus qu'à une compensation de l'optimiseur.
func TestEstimateRecognisesAPriceWithoutObservationNoise(t *testing.T) {
	noNoise := params{Eps: 0, Eta: 4e-6, Zeta: 1e-10}
	y := simulate(6000, noNoise, 3)
	f, err := estimate(y)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("estimé : %+v", f)
	if !f.NegligibleEps || f.AtUpperBound {
		t.Fatalf("σ²_ε nul doit être reconnu comme négligeable, sans borne haute : %+v", f)
	}
	// Mesuré sur cette graine : σ²_η 0,979, σ²_ζ 0,589. La variance de la
	// pente est la moins identifiable des trois (la pente ne se voit qu'à
	// travers l'accumulation de ses chocs) : tolérance plus large, dite.
	for name, c := range map[string]struct{ got, want, tol float64 }{
		"σ²_η": {f.Params.Eta, noNoise.Eta, 2},
		"σ²_ζ": {f.Params.Zeta, noNoise.Zeta, 4},
	} {
		ratio := c.got / c.want
		t.Logf("%s estimé / vrai = %.3f", name, ratio)
		if ratio < 1/c.tol || ratio > c.tol {
			t.Errorf("%s : rapport estimé/vrai %.3f hors de [1/%g ; %g]", name, ratio, c.tol, c.tol)
		}
	}
}
