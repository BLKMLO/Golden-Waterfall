package gbdt

import (
	"context"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
)

// makeProblem fabrique un problème séparable mais bruité : le label suit
// une règle XOR-like sur deux features informatives, noyées dans du bruit.
// Un modèle qui n'apprend rien reste à 0,5 d'AUC.
func makeProblem(n, noiseCols int, seed int64, withMissing bool) (*Dataset, error) {
	rng := rand.New(rand.NewSource(seed))
	cols := 2 + noiseCols
	names := make([]string, cols)
	names[0], names[1] = "a", "b"
	for i := 2; i < cols; i++ {
		names[i] = "noise"
	}
	x := make([]float64, n*cols)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		a := rng.NormFloat64()
		b := rng.NormFloat64()
		x[i*cols] = a
		x[i*cols+1] = b
		for c := 2; c < cols; c++ {
			x[i*cols+c] = rng.NormFloat64()
		}
		if withMissing && rng.Float64() < 0.1 {
			x[i*cols] = math.NaN()
		}
		logit := 2.5 * a * b
		p := 1 / (1 + math.Exp(-logit))
		if rng.Float64() < p {
			y[i] = 1
		}
	}
	return NewDataset(x, y, names)
}

func TestTrainLearnsSignal(t *testing.T) {
	train, err := makeProblem(4000, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	test, err := makeProblem(2000, 5, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.NumRounds = 120
	p.LearningRate = 0.1
	p.MinDataInLeaf = 20
	model, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	probs, err := model.PredictBatch(test.X, test.Cols)
	if err != nil {
		t.Fatal(err)
	}
	auc := AUC(probs, test.Y)
	if auc < 0.75 {
		t.Fatalf("AUC out-of-sample trop faible : %.3f (un signal net doit se retrouver)", auc)
	}
	if ll := LogLoss(probs, test.Y); ll > 0.62 {
		t.Fatalf("perte logistique out-of-sample trop élevée : %.3f", ll)
	}
}

func TestMissingValuesAreLearned(t *testing.T) {
	train, err := makeProblem(4000, 3, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.NumRounds = 60
	p.LearningRate = 0.1
	p.MinDataInLeaf = 20
	model, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatalf("les NaN doivent être gérés nativement, pas faire échouer : %v", err)
	}
	row := make([]float64, train.Cols)
	for i := range row {
		row[i] = math.NaN()
	}
	got := model.Predict(row)
	if math.IsNaN(got) || got < 0 || got > 1 {
		t.Fatalf("une ligne entièrement manquante doit rester une probabilité valide, reçu %v", got)
	}
}

func TestEarlyStoppingKeepsBestIteration(t *testing.T) {
	train, _ := makeProblem(2000, 4, 4, false)
	valid, _ := makeProblem(800, 4, 5, false)
	p := DefaultParams()
	p.NumRounds = 400
	p.LearningRate = 0.2
	p.EarlyStoppingRounds = 10
	p.MinDataInLeaf = 20
	model, err := Train(context.Background(), train, valid, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.BestIteration > len(model.Trees) {
		t.Fatalf("BestIteration (%d) dépasse le nombre d'arbres (%d)", model.BestIteration, len(model.Trees))
	}
	if len(model.Trees) >= p.NumRounds {
		t.Fatalf("l'arrêt anticipé n'a rien coupé (%d arbres pour %d tours)", len(model.Trees), p.NumRounds)
	}
}

func TestDeterminismSameSeed(t *testing.T) {
	train, _ := makeProblem(1500, 4, 6, false)
	p := DefaultParams()
	p.NumRounds = 25
	p.MinDataInLeaf = 20
	a, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := train.Row(7)
	if a.Predict(row) != b.Predict(row) {
		t.Fatal("à graine égale, deux entraînements doivent donner exactement le même modèle")
	}
}

func TestCategoricalSplit(t *testing.T) {
	// Le label dépend UNIQUEMENT de la catégorie : sans split catégoriel
	// correct, le modèle ne peut pas dépasser le hasard.
	rng := rand.New(rand.NewSource(9))
	n := 3000
	names := []string{"cat", "noise"}
	x := make([]float64, n*2)
	y := make([]float64, n)
	good := map[int]bool{1: true, 4: true, 7: true}
	for i := 0; i < n; i++ {
		c := rng.Intn(8)
		x[i*2] = float64(c)
		x[i*2+1] = rng.NormFloat64()
		p := 0.2
		if good[c] {
			p = 0.8
		}
		if rng.Float64() < p {
			y[i] = 1
		}
	}
	ds, err := NewDataset(x, y, names)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.NumRounds = 60
	p.LearningRate = 0.2
	p.MinDataInLeaf = 20
	p.CategoricalFeatures = []int{0}
	model, err := Train(context.Background(), ds, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	probs, _ := model.PredictBatch(ds.X, ds.Cols)
	if auc := AUC(probs, ds.Y); auc < 0.7 {
		t.Fatalf("le split catégoriel n'a pas capté la règle (AUC %.3f)", auc)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	train, _ := makeProblem(1200, 3, 8, true)
	p := DefaultParams()
	p.NumRounds = 20
	p.MinDataInLeaf = 20
	model, err := Train(context.Background(), train, nil, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if err := model.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadModel(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		row := train.Row(i)
		if got, want := reloaded.Predict(row), model.Predict(row); math.Abs(got-want) > 1e-12 {
			t.Fatalf("ligne %d : modèle rechargé %.15f contre %.15f", i, got, want)
		}
	}
}

func TestSingleClassIsRefused(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5, 6}
	y := []float64{1, 1, 1}
	ds, err := NewDataset(x, y, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Train(context.Background(), ds, nil, DefaultParams(), nil); err == nil {
		t.Fatal("un jeu à classe unique doit être refusé, pas produire un modèle constant déguisé")
	}
}

func TestAUCTiesUseAverageRanks(t *testing.T) {
	// Deux scores identiques pour un positif et un négatif : l'AUC doit
	// valoir exactement 0,5, pas 0 ou 1 selon l'ordre de tri.
	if got := AUC([]float64{0.5, 0.5}, []float64{1, 0}); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("AUC avec ex æquo = %v, attendu 0.5", got)
	}
	if got := AUC([]float64{0.9, 0.1}, []float64{1, 0}); got != 1 {
		t.Fatalf("classement parfait = %v, attendu 1", got)
	}
	if got := AUC([]float64{0.9, 0.1}, []float64{1, 1}); !math.IsNaN(got) {
		t.Fatalf("une seule classe : AUC indéfinie attendue, reçu %v", got)
	}
}
