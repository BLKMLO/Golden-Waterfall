package strategy

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/label"
)

// makeSeries fabrique une série H4 au comportement APPRENABLE : un régime
// de retour à la moyenne autour d'une lente oscillation, plus du bruit.
// Un modèle qui n'apprend rien y reste à 0,50 d'AUC ; un modèle correct
// dépasse nettement. C'est ce qui rend le test discriminant.
//
// Ces données ne servent QU'aux tests : elles ne sont jamais écrites dans
// le dossier de données de l'utilisateur.
func makeSeries(n int, seed int64, base float64) core.Series {
	rng := rand.New(rand.NewSource(seed))
	start := time.Date(2020, 1, 6, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, 0, n)
	price := base
	for i := 0; i < n; i++ {
		anchor := base + 0.02*base*math.Sin(float64(i)/90.0)
		pull := (anchor - price) * 0.05
		noise := rng.NormFloat64() * base * 0.0012
		open := price
		price = math.Max(price+pull+noise, base*0.5)
		high := math.Max(open, price) + math.Abs(rng.NormFloat64())*base*0.0004
		low := math.Min(open, price) - math.Abs(rng.NormFloat64())*base*0.0004
		spread := base * 0.0001
		out = append(out, core.Bar{
			Time:    start.Add(time.Duration(i) * 4 * time.Hour),
			BidOpen: open, BidHigh: high, BidLow: low, BidClose: price,
			AskOpen: open + spread, AskHigh: high + spread,
			AskLow: low + spread, AskClose: price + spread,
			Volume: 80 + rng.Float64()*60,
		})
	}
	return out
}

func TestRegistryHasBothVersions(t *testing.T) {
	names := List()
	want := map[string]bool{"colibri_v1_0": false, "colibri_v1_1": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Fatalf("stratégie %q absente du registre : %v", n, names)
		}
	}
	if _, err := New("inexistante"); err == nil {
		t.Fatal("une stratégie inconnue doit être refusée")
	}
}

func TestStrategyWithoutModelStaysMute(t *testing.T) {
	s, err := New("colibri_v1_1")
	if err != nil {
		t.Fatal(err)
	}
	series := makeSeries(500, 1, 1.10)
	if err := s.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: series, Timeframe: data.H4,
	}); err != nil {
		t.Fatalf("une chauffe sans modèle n'est PAS une erreur : %v", err)
	}
	ready, reason := s.Ready()
	if ready {
		t.Fatal("sans modèle, la stratégie ne peut pas être prête")
	}
	if reason == "" {
		t.Fatal("la stratégie doit DIRE pourquoi elle est muette")
	}
	sig, err := s.OnBar(context.Background(), "EURUSD", series, len(series)-1)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Action != core.Hold {
		t.Fatal("sans modèle, aucun signal ne doit être improvisé")
	}
}

func TestPooledStrategyTrainsAndPredicts(t *testing.T) {
	s, err := New("colibri_v1_1")
	if err != nil {
		t.Fatal(err)
	}
	trainable, ok := s.(Trainable)
	if !ok {
		t.Fatal("colibri_v1_1 doit être entraînable")
	}
	dir := t.TempDir()
	datasets := map[string]core.Series{
		"EURUSD": makeSeries(2200, 21, 1.10),
		"GBPUSD": makeSeries(2200, 22, 1.27),
	}
	report, err := trainable.Train(context.Background(), TrainRequest{
		Datasets: datasets, Timeframe: data.H4, OutputDir: dir, Seed: 42, Threads: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples < minTrainSamples {
		t.Fatalf("%d exemples seulement", report.Samples)
	}
	if report.Features != len(feature.Columns)+1 {
		t.Fatalf("%d features, %d attendues (34 causales + symbol)",
			report.Features, len(feature.Columns)+1)
	}
	for _, f := range []string{"model.json", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("artefact manquant : %s", f)
		}
	}

	// L'AUC in-sample mesure la MÉMORISATION : elle doit au moins montrer
	// que l'apprentissage a eu lieu.
	if auc := report.Metrics["train_auc"]; auc <= 0.55 {
		t.Fatalf("AUC d'entraînement %.3f : le modèle n'a rien appris du tout", auc)
	}

	// Rechargement dans une instance NEUVE : c'est le chemin réel du live.
	fresh, _ := New("colibri_v1_1")
	series := datasets["EURUSD"]
	if err := fresh.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: series, Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	if ready, why := fresh.Ready(); !ready {
		t.Fatalf("modèle rechargé mais stratégie non prête : %s", why)
	}
	sig, err := fresh.OnBar(context.Background(), "EURUSD", series, len(series)-1)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Confidence < 0 || sig.Confidence > 1 {
		t.Fatalf("la confiance doit être une probabilité : %v", sig.Confidence)
	}
	if sig.Strategy != "colibri_v1_1" {
		t.Fatalf("signal non attribué : %q", sig.Strategy)
	}
}

func TestBarriersMatchLabelingMultiple(t *testing.T) {
	s, _ := New("colibri_v1_1")
	trainable := s.(Trainable)
	dir := t.TempDir()
	series := makeSeries(2200, 31, 1.10)
	if _, err := trainable.Train(context.Background(), TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": series},
		Timeframe: data.H4, OutputDir: dir, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: series, Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		t.Fatal(err)
	}

	atr := feature.ATR(series)
	found := false
	for i := feature.ContextBars; i < len(series); i++ {
		sig, err := s.OnBar(context.Background(), "EURUSD", series, i)
		if err != nil {
			t.Fatal(err)
		}
		if !sig.Action.IsEntry() {
			continue
		}
		found = true
		want := label.BarrierATRMult * atr[i]
		gotTP := math.Abs(sig.TakeProfit - series[i].Close())
		gotSL := math.Abs(sig.StopLoss - series[i].Close())
		if math.Abs(gotTP-want) > 1e-9 || math.Abs(gotSL-want) > 1e-9 {
			t.Fatalf("barrières dimensionnées à %v/%v au lieu de %v (même multiple qu'au labeling)",
				gotTP, gotSL, want)
		}
		// Sens : un long vise plus haut, un short plus bas.
		if sig.Action == core.EnterLong && sig.TakeProfit <= series[i].Close() {
			t.Fatal("un long doit viser au-dessus du close")
		}
		if sig.Action == core.EnterShort && sig.TakeProfit >= series[i].Close() {
			t.Fatal("un short doit viser en dessous du close")
		}
		break
	}
	if !found {
		t.Skip("aucune entrée sur ce jeu synthétique : rien à vérifier ici")
	}
}

func TestPerSymbolStrategyRefusesPooledTraining(t *testing.T) {
	s, _ := New("colibri_v1_0")
	trainable := s.(Trainable)
	_, err := trainable.Train(context.Background(), TrainRequest{
		Datasets: map[string]core.Series{
			"EURUSD": makeSeries(1500, 41, 1.10),
			"GBPUSD": makeSeries(1500, 42, 1.27),
		},
		Timeframe: data.H4, OutputDir: t.TempDir(), Seed: 1,
	})
	if err == nil {
		t.Fatal("colibri_v1_0 entraîne UN modèle par actif : plusieurs symboles doivent être refusés")
	}
}

func TestTinyDatasetIsRefused(t *testing.T) {
	s, _ := New("colibri_v1_1")
	trainable := s.(Trainable)
	_, err := trainable.Train(context.Background(), TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": makeSeries(200, 51, 1.10)},
		Timeframe: data.H4, OutputDir: t.TempDir(), Seed: 1,
	})
	if err == nil {
		t.Fatal("un jeu trop petit doit être REFUSÉ, pas produire un modèle décoratif")
	}
}

func TestModelOfAnotherStrategyIsRefused(t *testing.T) {
	// Un modèle v1.1 ne doit pas pouvoir être chargé par v1.0 : les
	// colonnes et les seuils diffèrent, les probabilités seraient
	// crédibles et fausses.
	s2, _ := New("colibri_v1_1")
	dir := t.TempDir()
	if _, err := s2.(Trainable).Train(context.Background(), TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": makeSeries(2200, 61, 1.10)},
		Timeframe: data.H4, OutputDir: dir, Seed: 3,
	}); err != nil {
		t.Fatal(err)
	}
	s1, _ := New("colibri_v1_0")
	err := s1.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: makeSeries(500, 62, 1.10), Timeframe: data.H4, ModelDir: dir,
	})
	if err == nil {
		t.Fatal("charger un modèle d'une AUTRE stratégie doit être refusé")
	}
}

func TestDescribeExposesFrozenDefinition(t *testing.T) {
	s, _ := New("colibri_v1_1")
	d := s.Describe()
	if d.Definition == nil {
		t.Fatal("la définition du modèle doit être exposée (source unique de vérité)")
	}
	if d.Definition["barriere_atr"] != label.BarrierATRMult {
		t.Fatal("la définition doit citer la VRAIE constante, pas une copie")
	}
	if d.Definition["nb_features"] != len(feature.Columns)+1 {
		t.Fatalf("nombre de features annoncé incohérent : %v", d.Definition["nb_features"])
	}
	if d.Definition["seuil_long"] != 0.60 {
		t.Fatalf("seuil long v1.1 = 0,60, annoncé %v", d.Definition["seuil_long"])
	}
}

func TestScoreOOSReportsUnavailableHonestly(t *testing.T) {
	s, _ := New("colibri_v1_1")
	trainable := s.(Trainable)
	if _, ok := trainable.ScoreOOS("EURUSD", makeSeries(500, 71, 1.10), 0); ok {
		t.Fatal("sans modèle, l'AUC out-of-sample n'est PAS calculable : il faut le dire")
	}
}

func TestTrainingIsReproducible(t *testing.T) {
	series := makeSeries(2200, 81, 1.10)
	run := func() float64 {
		s, _ := New("colibri_v1_1")
		dir := t.TempDir()
		if _, err := s.(Trainable).Train(context.Background(), TrainRequest{
			Datasets:  map[string]core.Series{"EURUSD": series},
			Timeframe: data.H4, OutputDir: dir, Seed: 1234, Threads: 1,
		}); err != nil {
			t.Fatal(err)
		}
		sig, err := s.OnBar(context.Background(), "EURUSD", series, len(series)-1)
		if err != nil {
			t.Fatal(err)
		}
		return sig.Confidence
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("à graine égale, l'entraînement doit être REPRODUCTIBLE : %.15f contre %.15f", a, b)
	}
}

// TestModelWithReorderedColumnsIsRefused : le pire mode de défaillance de
// tout le projet serait un modèle appliqué à des colonnes décalées — la
// prédiction lirait le RSI là où le modèle attend l'ATR et sortirait des
// probabilités crédibles et fausses. Vérifier le NOMBRE de colonnes ne
// suffit pas : il faut les noms, et dans l'ordre.
func TestModelWithReorderedColumnsIsRefused(t *testing.T) {
	dir := t.TempDir()
	s, _ := New("colibri_v1_1")
	if _, err := s.(Trainable).Train(context.Background(), TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": makeSeries(2200, 91, 1.10)},
		Timeframe: data.H4, OutputDir: dir, Seed: 3,
	}); err != nil {
		t.Fatal(err)
	}

	swapColumns := func(path string, field string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		cols, ok := doc[field].([]any)
		if !ok || len(cols) < 2 {
			t.Fatalf("%s : champ %q introuvable", path, field)
		}
		cols[0], cols[1] = cols[1], cols[0]
		doc[field] = cols
		out, _ := json.Marshal(doc)
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	swapColumns(filepath.Join(dir, "model.json"), "feature_names")
	fresh, _ := New("colibri_v1_1")
	err := fresh.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: makeSeries(500, 92, 1.10), Timeframe: data.H4, ModelDir: dir,
	})
	if err == nil {
		t.Fatal("un modèle aux colonnes permutées doit être REFUSÉ, pas appliqué")
	}
	if !strings.Contains(err.Error(), "colonne") {
		t.Fatalf("le message doit nommer la colonne fautive : %v", err)
	}
}

func TestMetadataColumnsMustMatchToo(t *testing.T) {
	dir := t.TempDir()
	s, _ := New("colibri_v1_1")
	if _, err := s.(Trainable).Train(context.Background(), TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": makeSeries(2200, 93, 1.10)},
		Timeframe: data.H4, OutputDir: dir, Seed: 3,
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "metadata.json")
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	doc["feature_columns"] = []string{"une_seule_colonne"}
	out, _ := json.Marshal(doc)
	os.WriteFile(path, out, 0o644)

	fresh, _ := New("colibri_v1_1")
	if err := fresh.Warmup(context.Background(), WarmupRequest{
		Symbol: "EURUSD", Series: makeSeries(500, 94, 1.10), Timeframe: data.H4, ModelDir: dir,
	}); err == nil {
		t.Fatal("des métadonnées incohérentes avec le modèle doivent être refusées")
	}
}
