package colibri

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy/strategytest"
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

func TestRegistryHasEveryRevision(t *testing.T) {
	names := strategy.List()
	want := map[string]bool{"colibri_v1_0": false, "colibri_v1_1": false, "colibri_v1_2": false}
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
	if _, err := strategy.New("inexistante"); err == nil {
		t.Fatal("une stratégie inconnue doit être refusée")
	}
}

func TestStrategyWithoutModelStaysMute(t *testing.T) {
	s, err := strategy.New("colibri_v1_1")
	if err != nil {
		t.Fatal(err)
	}
	series := makeSeries(500, 1, 1.10)
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{
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
	s, err := strategy.New("colibri_v1_1")
	if err != nil {
		t.Fatal(err)
	}
	trainable, ok := s.(strategy.Trainable)
	if !ok {
		t.Fatal("colibri_v1_1 doit être entraînable")
	}
	dir := t.TempDir()
	datasets := map[string]core.Series{
		"EURUSD": makeSeries(2200, 21, 1.10),
		"GBPUSD": makeSeries(2200, 22, 1.27),
	}
	report, err := trainable.Train(context.Background(), strategy.TrainRequest{
		Datasets: datasets, Timeframe: data.H4, OutputDir: dir, Seed: 42, Threads: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples < minTrainSamples {
		t.Fatalf("%d exemples seulement", report.Samples)
	}
	if report.Features != len(columnsV1)+1 {
		t.Fatalf("%d features, %d attendues (34 causales + symbol)",
			report.Features, len(columnsV1)+1)
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
	fresh, _ := strategy.New("colibri_v1_1")
	series := datasets["EURUSD"]
	if err := fresh.Warmup(context.Background(), strategy.WarmupRequest{
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
	s, _ := strategy.New("colibri_v1_1")
	trainable := s.(strategy.Trainable)
	dir := t.TempDir()
	series := makeSeries(2200, 31, 1.10)
	if _, err := trainable.Train(context.Background(), strategy.TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": series},
		Timeframe: data.H4, OutputDir: dir, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Series: series, Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		t.Fatal(err)
	}

	atr := atrOf(series)
	found := false
	for i := contextBars; i < len(series); i++ {
		sig, err := s.OnBar(context.Background(), "EURUSD", series, i)
		if err != nil {
			t.Fatal(err)
		}
		if !sig.Action.IsEntry() {
			continue
		}
		found = true
		want := revV11.barrierATRMult * atr[i]
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
	s, _ := strategy.New("colibri_v1_0")
	trainable := s.(strategy.Trainable)
	_, err := trainable.Train(context.Background(), strategy.TrainRequest{
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
	s, _ := strategy.New("colibri_v1_1")
	trainable := s.(strategy.Trainable)
	_, err := trainable.Train(context.Background(), strategy.TrainRequest{
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
	s2, _ := strategy.New("colibri_v1_1")
	dir := t.TempDir()
	if _, err := s2.(strategy.Trainable).Train(context.Background(), strategy.TrainRequest{
		Datasets:  map[string]core.Series{"EURUSD": makeSeries(2200, 61, 1.10)},
		Timeframe: data.H4, OutputDir: dir, Seed: 3,
	}); err != nil {
		t.Fatal(err)
	}
	s1, _ := strategy.New("colibri_v1_0")
	err := s1.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Series: makeSeries(500, 62, 1.10), Timeframe: data.H4, ModelDir: dir,
	})
	if err == nil {
		t.Fatal("charger un modèle d'une AUTRE stratégie doit être refusé")
	}
}

func TestDescribeExposesFrozenDefinition(t *testing.T) {
	s, _ := strategy.New("colibri_v1_1")
	d := s.Describe()
	if d.Definition == nil {
		t.Fatal("la définition du modèle doit être exposée (source unique de vérité)")
	}
	if d.Definition["barriere_atr"] != revV11.barrierATRMult {
		t.Fatal("la définition doit citer la VRAIE constante, pas une copie")
	}
	if d.Definition["nb_features"] != len(columnsV1)+1 {
		t.Fatalf("nombre de features annoncé incohérent : %v", d.Definition["nb_features"])
	}
	if d.Definition["seuil_long"] != 0.60 {
		t.Fatalf("seuil long v1.1 = 0,60, annoncé %v", d.Definition["seuil_long"])
	}
}

func TestScoreOOSReportsUnavailableHonestly(t *testing.T) {
	s, _ := strategy.New("colibri_v1_1")
	trainable := s.(strategy.Trainable)
	if _, ok := trainable.ScoreOOS("EURUSD", makeSeries(500, 71, 1.10), 0); ok {
		t.Fatal("sans modèle, l'AUC out-of-sample n'est PAS calculable : il faut le dire")
	}
}

func TestTrainingIsReproducible(t *testing.T) {
	series := makeSeries(2200, 81, 1.10)
	run := func() float64 {
		s, _ := strategy.New("colibri_v1_1")
		dir := t.TempDir()
		if _, err := s.(strategy.Trainable).Train(context.Background(), strategy.TrainRequest{
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
	s, _ := strategy.New("colibri_v1_1")
	if _, err := s.(strategy.Trainable).Train(context.Background(), strategy.TrainRequest{
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
	fresh, _ := strategy.New("colibri_v1_1")
	err := fresh.Warmup(context.Background(), strategy.WarmupRequest{
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
	s, _ := strategy.New("colibri_v1_1")
	if _, err := s.(strategy.Trainable).Train(context.Background(), strategy.TrainRequest{
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

	fresh, _ := strategy.New("colibri_v1_1")
	if err := fresh.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Series: makeSeries(500, 94, 1.10), Timeframe: data.H4, ModelDir: dir,
	}); err == nil {
		t.Fatal("des métadonnées incohérentes avec le modèle doivent être refusées")
	}
}

// --- colibri_v1_2 --------------------------------------------------------

// scripted : entre dans le sens demandé à UNE bougie, avec les barrières
// de la révision. Sert à confronter la cible au moteur.
type scripted struct {
	at     int
	action core.SignalAction
	stop   float64
	target float64
	hold   time.Duration
}

func (s *scripted) Describe() strategy.Description {
	return strategy.Description{Name: "scripte", MaxHold: s.hold}
}
func (s *scripted) Warmup(context.Context, strategy.WarmupRequest) error { return nil }
func (s *scripted) Shutdown() error                                      { return nil }
func (s *scripted) Ready() (bool, string)                                { return true, "" }
func (s *scripted) OnBar(_ context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	if i != s.at {
		return strategy.NoSignal(symbol), nil
	}
	return core.Signal{Symbol: symbol, Action: s.action, Price: series[i].Close(),
		StopLoss: s.stop, TakeProfit: s.target}, nil
}

// TestSidedTargetIsWhatTheEngineDelivers : pour des dizaines d'entrées
// tirées au hasard, l'issue apprise par v1_2 (gagnant NET ou non, bougie
// de sortie) est exactement celle que le moteur de backtest produit. C'est
// la promesse centrale de la révision : la cible décrit ce que le
// programme obtiendra vraiment.
func TestSidedTargetIsWhatTheEngineDelivers(t *testing.T) {
	series := strategytest.Series(1200, 5, 1.10) // spread constant, sans week-end
	c := newColibri(revV12)
	tg := c.targetOf(series, data.H4.Duration())
	atr := atrOf(series)

	cfg := config.Default()
	cfg.Risk.RiskPerTradePct = 0
	cfg.Risk.FixedPositionSize = 1000
	cfg.Risk.MaxPositionSize = 1_000_000
	cfg.Costs.CommissionPerUnit = 0
	cfg.Backtest.AccountCurrency = "USD"
	engine := backtest.NewEngine(cfg, risk.New(cfg.Risk, "USD", slog.New(slog.DiscardHandler)))

	rng := rand.New(rand.NewSource(3))
	checked := 0
	for k := 0; k < 60; k++ {
		i := contextBars + rng.Intn(len(series)-contextBars-1)
		for h, action := range []core.SignalAction{core.EnterLong, core.EnterShort} {
			if math.IsNaN(tg.value[h][i]) {
				continue
			}
			d := revV12.barrierATRMult * atr[i]
			close := series[i].Close()
			s := &scripted{at: i, action: action, stop: close - d, target: close + d, hold: revV12.maxHold()}
			if action == core.EnterShort {
				s.stop, s.target = close+d, close-d
			}
			res, err := engine.Run(context.Background(), backtest.Request{
				Symbol: "EURUSD", Series: series, From: i, Strategy: s, Timeframe: data.H4,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Trades) != 1 {
				t.Fatalf("bougie %d, %s : %d trade(s)", i, action, len(res.Trades))
			}
			tr := res.Trades[0]
			won := 0.0
			if tr.IsWin() {
				won = 1
			}
			if won != tg.value[h][i] || !tr.ExitTime.Equal(series[tg.exit[h][i]].Time) {
				t.Fatalf("bougie %d, %s : cible %v sortie %s, moteur %v sortie %s (%s)",
					i, action, tg.value[h][i], series[tg.exit[h][i]].Time, won, tr.ExitTime, tr.ExitReason)
			}
			checked++
		}
	}
	if checked < 50 {
		t.Fatalf("seulement %d entrées vérifiées", checked)
	}
}

// TestPerSymbolModelsDoNotOverwriteEachOther : une instance mono-actif
// chauffée sur deux paires garde LEUR modèle à chacune. Avant, la seconde
// chauffe remplaçait le modèle de la première.
func TestPerSymbolModelsDoNotOverwriteEachOther(t *testing.T) {
	ctx := context.Background()
	eur, gbp := makeSeries(2200, 1, 1.10), makeSeries(2200, 2, 1.27)
	dirs := map[string]string{"EURUSD": t.TempDir(), "GBPUSD": t.TempDir()}
	for sym, s := range map[string]core.Series{"EURUSD": eur, "GBPUSD": gbp} {
		tr, _ := strategy.New("colibri_v1_0")
		if _, err := tr.(strategy.Trainable).Train(ctx, strategy.TrainRequest{
			Datasets: map[string]core.Series{sym: s}, Timeframe: data.H4, OutputDir: dirs[sym], Seed: 1, Threads: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	alone, _ := strategy.New("colibri_v1_0")
	both, _ := strategy.New("colibri_v1_0")
	alone.Warmup(ctx, strategy.WarmupRequest{Symbol: "EURUSD", Series: eur, Timeframe: data.H4, ModelDir: dirs["EURUSD"]})
	both.Warmup(ctx, strategy.WarmupRequest{Symbol: "EURUSD", Series: eur, Timeframe: data.H4, ModelDir: dirs["EURUSD"]})
	both.Warmup(ctx, strategy.WarmupRequest{Symbol: "GBPUSD", Series: gbp, Timeframe: data.H4, ModelDir: dirs["GBPUSD"]})
	for i := contextBars; i < len(eur); i += 97 {
		a, _ := alone.OnBar(ctx, "EURUSD", eur, i)
		b, _ := both.OnBar(ctx, "EURUSD", eur, i)
		if a != b {
			t.Fatalf("bougie %d : la chauffe de GBPUSD a changé les décisions d'EURUSD", i)
		}
	}
}

func TestV12WritesBothHeadsAndTheirMeasurements(t *testing.T) {
	dir := t.TempDir()
	s, _ := strategy.New("colibri_v1_2")
	if _, err := s.(strategy.Trainable).Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{
			"EURUSD": strategytest.Series(2400, 1, 1.10), "GBPUSD": strategytest.Series(2400, 2, 1.27),
		},
		Timeframe: data.H4, OutputDir: dir, Seed: 1, Threads: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"model_long.json", "model_short.json", strategy.ModelManifest} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("artefact manquant : %s", f)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, strategy.ModelManifest))
	var meta modelMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if !meta.CostsModelled || meta.MinEdgeR != revV12.minEdgeR {
		t.Fatalf("manifeste incomplet : %+v", meta)
	}
	for _, h := range []string{headLong, headShort} {
		st, ok := meta.HeadStats[h]
		if !ok || st.WinR <= 0 || st.LossR <= 0 || !st.Calibrated || st.Calibration.A < 0 || st.Calibration.A > 1 {
			t.Fatalf("tête %s : mesures absentes ou hors bornes %+v", h, st)
		}
	}

	// Un manifeste sans les mesures d'une tête est refusé : la règle de
	// décision ne pourrait que les inventer.
	delete(meta.HeadStats, headShort)
	out, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(dir, strategy.ModelManifest), out, 0o644)
	fresh, _ := strategy.New("colibri_v1_2")
	if err := fresh.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Series: strategytest.Series(500, 3, 1.10), Timeframe: data.H4, ModelDir: dir,
	}); err == nil {
		t.Fatal("un modèle v1_2 sans mesures de tête doit être refusé")
	}
}

// TestExpectedValueRule : l'entrée dépend de l'espérance NETTE, donc du
// coût du moment — pas d'un seuil de probabilité fixe.
func TestExpectedValueRule(t *testing.T) {
	c := newColibri(revV12)
	stats := map[string]headStats{
		headLong:  {WinR: 1, LossR: 1},
		headShort: {WinR: 1, LossR: 1},
	}
	p := &prepared{
		model: &loaded{meta: &modelMeta{HeadStats: stats, MinEdgeR: 0.15}},
		atr:   []float64{1, 1, 1},
		// Coût aller-retour en prix : 0, puis 0,15 (= 0,1 R à 1,5 ATR).
		cost:  []float64{0, 0.15, 0},
		probs: [][]float64{{0.60, 0.60, 0.40}, {0.40, 0.40, 0.60}},
	}
	if a, conf := c.decideExpectedValue(p, 0); a != core.EnterLong || conf != 0.60 {
		t.Fatalf("E = 0,2 R ≥ 0,15 : long attendu, %s %v", a, conf)
	}
	if a, _ := c.decideExpectedValue(p, 1); a != core.Hold {
		t.Fatalf("E = 0,2 − 0,1 = 0,1 R < 0,15 : abstention attendue, %s", a)
	}
	if a, _ := c.decideExpectedValue(p, 2); a != core.EnterShort {
		t.Fatalf("short attendu, %s", a)
	}
}
