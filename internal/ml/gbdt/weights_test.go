package gbdt

import (
	"context"
	"math"
	"math/rand"
	"testing"
)

// TestWeightsDownplayCorruptedRows : un tiers des lignes a un label
// INVERSÉ. Pondérées à presque rien, elles ne doivent plus rien enseigner :
// le modèle pondéré retrouve le signal que le modèle non pondéré brouille.
func TestWeightsDownplayCorruptedRows(t *testing.T) {
	train, err := makeProblem(4000, 3, 21, false)
	if err != nil {
		t.Fatal(err)
	}
	test, err := makeProblem(2000, 3, 22, false)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(5))
	w := make([]float64, train.Rows)
	for i := range w {
		w[i] = 1
		if rng.Float64() < 0.33 {
			train.Y[i] = 1 - train.Y[i]
			w[i] = 1e-6
		}
	}
	p := DefaultParams()
	p.NumRounds = 120
	p.LearningRate = 0.1
	p.MinDataInLeaf = 20
	p.Threads = 1

	plain, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := train.SetWeights(w); err != nil {
		t.Fatal(err)
	}
	weighted, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	aucOf := func(m *Model) float64 {
		probs, err := m.PredictBatch(test.X, test.Cols)
		if err != nil {
			t.Fatal(err)
		}
		return AUC(probs, test.Y)
	}
	a, b := aucOf(plain), aucOf(weighted)
	if b <= a {
		t.Fatalf("pondéré %.3f ≤ non pondéré %.3f : les poids n'ont rien changé", b, a)
	}
}

func TestSetWeightsNormalisesAndRejectsNonsense(t *testing.T) {
	d, err := NewDataset([]float64{1, 2, 3, 4}, []float64{0, 1, 0, 1}, []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetWeights([]float64{2, 2, 2, 2}); err != nil {
		t.Fatal(err)
	}
	for _, v := range d.W {
		if v != 1 {
			t.Fatalf("poids renormalisés à une moyenne de 1 attendus : %v", d.W)
		}
	}
	for _, bad := range [][]float64{{1, 1, 1}, {1, -1, 1, 1}, {0, 0, 0, 0}, {1, math.NaN(), 1, 1}} {
		if err := d.SetWeights(bad); err == nil {
			t.Fatalf("poids %v acceptés", bad)
		}
	}
}

// TestShrinkageRemovesOverconfidenceButNeverAddsAny : un score dont la
// vraie pente vaut 0,5 est rétréci vers 0,5 ; un score deux fois trop
// prudent n'est PAS amplifié (A plafonné à 1) ; un score sans rapport avec
// la cible retombe sur le centre.
func TestShrinkageRemovesOverconfidenceButNeverAddsAny(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	sample := func(slope float64) ([]float64, []float64) {
		s := make([]float64, 20000)
		y := make([]float64, len(s))
		for i := range s {
			s[i] = rng.NormFloat64() * 2
			if rng.Float64() < sigmoid(slope*s[i]) {
				y[i] = 1
			}
		}
		return s, y
	}
	s, y := sample(0.5)
	c, ok := FitShrinkage(s, y, nil, 0)
	if !ok || math.Abs(c.A-0.5) > 0.05 {
		t.Fatalf("pente vraie 0,5, ajustée %v", c.A)
	}
	s, y = sample(2)
	if c, _ := FitShrinkage(s, y, nil, 0); c.A != 1 {
		t.Fatalf("A plafonné à 1 attendu, %v", c.A)
	}
	s, y = sample(0)
	if c, _ := FitShrinkage(s, y, nil, 0); c.A > 0.05 {
		t.Fatalf("score sans rapport : A = %v, ≈ 0 attendu", c.A)
	}
	if _, ok := FitShrinkage([]float64{1, 2}, []float64{1, 1}, nil, 0); ok {
		t.Fatal("une seule classe : aucun calibrage ne doit être annoncé")
	}
}

// TestShrinkageNeverShiftsTheCentre : le rétrécissement ne peut pas
// ajouter d'opinion. Un score égal au centre reste au centre, quelle que
// soit la validation — contrairement à une ordonnée libre, qui absorbait
// la tendance récente de la validation.
func TestShrinkageNeverShiftsTheCentre(t *testing.T) {
	scores := []float64{-1, 0, 1, 2, 3}
	y := []float64{1, 1, 1, 1, 0} // validation « haussière » malgré les scores
	c, ok := FitShrinkage(scores, y, nil, 0.4)
	if !ok {
		t.Fatal("ajustement attendu")
	}
	if got, want := c.Apply(0.4), sigmoid(0.4); math.Abs(got-want) > 1e-15 {
		t.Fatalf("le centre a bougé : %v au lieu de %v", got, want)
	}
}
