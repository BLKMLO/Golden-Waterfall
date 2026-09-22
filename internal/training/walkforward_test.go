package training

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// writeHistory écrit un historique synthétique dans le dossier de données
// d'un test. Les bougies sont espacées de 4 heures : le
// ré-échantillonnage en H4 est alors un pour un, ce qui rend le test
// rapide sans rien changer au chemin de code exercé.
func writeHistory(t *testing.T, dir, symbol string, base float64, seed int64, years []int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	price := base
	for _, year := range years {
		start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		var series core.Series
		for ts := start; ts.Year() == year; ts = ts.Add(4 * time.Hour) {
			if ts.Weekday() == time.Saturday || ts.Weekday() == time.Sunday {
				continue
			}
			anchor := base + 0.02*base*math.Sin(float64(ts.YearDay())/28.0)
			open := price
			price = math.Max(price+(anchor-price)*0.05+rng.NormFloat64()*base*0.0012, base*0.5)
			high := math.Max(open, price) + math.Abs(rng.NormFloat64())*base*0.0004
			low := math.Min(open, price) - math.Abs(rng.NormFloat64())*base*0.0004
			spread := base * 0.0001
			series = append(series, core.Bar{
				Time:    ts,
				BidOpen: open, BidHigh: high, BidLow: low, BidClose: price,
				AskOpen: open + spread, AskHigh: high + spread,
				AskLow: low + spread, AskClose: price + spread,
				Volume: 60 + rng.Float64()*80,
			})
		}
		path := data.FilePath(dir, symbol, year)
		header := data.FileHeader{Symbol: symbol, Year: year, Scale: 100000}
		if err := data.WriteSeries(path, header, series); err != nil {
			t.Fatal(err)
		}
	}
}

func testSetup(t *testing.T) (config.Config, *Runner) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths = config.Paths{ConfigDir: filepath.Join(root, "cfg"), DataDir: filepath.Join(root, "data")}
	if err := cfg.Paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg.Risk.RiskPerTradePct = 0
	cfg.Risk.MaxPositionSize = 1000
	cfg.Risk.FixedPositionSize = 1000
	cfg.Risk.MaxOpenPositions = 2
	cfg.Backtest.InitialCapital = 10000
	rm := risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, slog.New(slog.DiscardHandler))
	return cfg, NewRunner(cfg, rm)
}

func TestWalkForwardEndToEnd(t *testing.T) {
	cfg, runner := testSetup(t)
	years := []int{2020, 2021, 2022}
	writeHistory(t, cfg.Paths.HistoryDir(), "EURUSD", 1.10, 101, years)
	writeHistory(t, cfg.Paths.HistoryDir(), "GBPUSD", 1.27, 102, years)

	var lastRatio float64
	res, err := runner.Run(context.Background(), Request{
		Strategy:   "colibri_v1_1",
		Symbols:    []string{"EURUSD", "GBPUSD"},
		Timeframe:  data.H4,
		Folds:      3,
		Seed:       42,
		Workers:    2,
		TrainFinal: true,
		Progress: func(p Progress) {
			if p.Ratio < lastRatio-1e-9 {
				t.Errorf("l'avancement doit être monotone : %v après %v", p.Ratio, lastRatio)
			}
			lastRatio = p.Ratio
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Folds) != 3 {
		t.Fatalf("%d plis, 3 demandés", len(res.Folds))
	}
	for _, f := range res.Folds {
		if f.Err != "" {
			t.Fatalf("pli %d en échec : %s", f.Index, f.Err)
		}
		// Blocs de test strictement consécutifs et disjoints du bloc
		// d'entraînement : c'est toute la valeur du walk-forward.
		if !f.TrainEnd.Equal(f.TestStart) {
			t.Fatalf("pli %d : l'entraînement doit s'arrêter EXACTEMENT au début du test (%s / %s)",
				f.Index, f.TrainEnd, f.TestStart)
		}
		if !f.TestEnd.After(f.TestStart) {
			t.Fatalf("pli %d : bloc de test vide", f.Index)
		}
		if f.TrainBars == 0 {
			t.Fatalf("pli %d : aucune bougie d'entraînement", f.Index)
		}
	}
	for i := 1; i < len(res.Folds); i++ {
		if res.Folds[i].TestStart.Before(res.Folds[i-1].TestEnd) {
			t.Fatalf("les blocs de test %d et %d se chevauchent", i, i+1)
		}
		if !res.Folds[i].TrainEnd.After(res.Folds[i-1].TrainEnd) {
			t.Fatal("la fenêtre d'entraînement doit s'ÉLARGIR d'un pli à l'autre")
		}
	}

	if res.FinalDir == "" {
		t.Fatal("un modèle de production doit être écrit")
	}
	for _, f := range []string{"model.json", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(res.FinalDir, f)); err != nil {
			t.Fatalf("artefact de production manquant : %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.ModelsDir(), "colibri_v1_1", res.RunID, "run.json")); err != nil {
		t.Fatal("le résumé run.json doit être écrit")
	}
	if len(res.PerSymbol) != 2 {
		t.Fatalf("%d actifs dans les agrégats", len(res.PerSymbol))
	}
	if res.Aggregate.Trades != tradesOf(res) {
		t.Fatal("l'agrégat doit compter EXACTEMENT les trades des plis")
	}
}

func tradesOf(res *Result) int {
	n := 0
	for _, f := range res.Folds {
		n += f.Stats.Trades
	}
	return n
}

func TestWalkForwardRefusesSingleFold(t *testing.T) {
	_, runner := testSetup(t)
	_, err := runner.Run(context.Background(), Request{
		Strategy: "colibri_v1_1", Symbols: []string{"EURUSD"},
		Timeframe: data.H4, Folds: 1,
	})
	if err == nil {
		t.Fatal("un seul pli n'est pas un walk-forward : ce doit être refusé")
	}
}

func TestWalkForwardRefusesMissingHistory(t *testing.T) {
	_, runner := testSetup(t)
	_, err := runner.Run(context.Background(), Request{
		Strategy: "colibri_v1_1", Symbols: []string{"EURUSD"},
		Timeframe: data.H4, Folds: 3,
	})
	if err == nil {
		t.Fatal("sans historique, le walk-forward doit échouer clairement")
	}
}

func TestWalkForwardHonoursCancellation(t *testing.T) {
	cfg, runner := testSetup(t)
	writeHistory(t, cfg.Paths.HistoryDir(), "EURUSD", 1.10, 111, []int{2020, 2021, 2022})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // annulé AVANT de démarrer
	_, err := runner.Run(ctx, Request{
		Strategy: "colibri_v1_1", Symbols: []string{"EURUSD"},
		Timeframe: data.H4, Folds: 3,
	})
	if err == nil {
		t.Fatal("un contexte annulé doit interrompre le walk-forward")
	}
}

func TestSplitFoldsCoverSecondHalf(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	folds := splitFolds(start, end, 4)
	if len(folds) != 4 {
		t.Fatalf("%d plis", len(folds))
	}
	mid := start.Add(end.Sub(start) / 2)
	if !folds[0].testStart.Equal(mid) {
		t.Fatalf("le premier bloc de test doit commencer à la moitié : %s", folds[0].testStart)
	}
	if !folds[len(folds)-1].testEnd.Equal(end) {
		t.Fatalf("le dernier bloc doit finir à la fin des données : %s", folds[len(folds)-1].testEnd)
	}
	for i := 1; i < len(folds); i++ {
		if !folds[i].testStart.Equal(folds[i-1].testEnd) {
			t.Fatal("les blocs de test doivent être strictement consécutifs")
		}
	}
}

func TestCommonRangeIsIntersection(t *testing.T) {
	a := core.Series{
		{Time: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Time: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	b := core.Series{
		{Time: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	start, end := commonRange(map[string]core.Series{"A": a, "B": b})
	if !start.Equal(b[0].Time) || !end.Equal(a[1].Time) {
		t.Fatalf("la plage commune doit être l'INTERSECTION : %s → %s", start, end)
	}
}

func TestCatalogAndSelectModel(t *testing.T) {
	cfg, runner := testSetup(t)
	writeHistory(t, cfg.Paths.HistoryDir(), "EURUSD", 1.10, 121, []int{2020, 2021, 2022})
	res, err := runner.Run(context.Background(), Request{
		Strategy: "colibri_v1_1", Symbols: []string{"EURUSD"},
		Timeframe: data.H4, Folds: 2, Seed: 5, TrainFinal: true, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(cfg.Paths.ModelsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != res.RunID {
		t.Fatalf("inventaire des runs incorrect : %+v", runs)
	}

	dir, why := SelectModel(cfg.Paths.ModelsDir(), "colibri_v1_1", "EURUSD")
	if dir == "" {
		t.Fatalf("le modèle de production doit être sélectionnable : %s", why)
	}

	// Symbole jamais entraîné : pas de modèle, et une explication.
	dir, why = SelectModel(cfg.Paths.ModelsDir(), "colibri_v1_1", "USDJPY")
	if dir != "" {
		t.Fatal("aucun modèle ne couvre USDJPY")
	}
	if why == "" {
		t.Fatal("l'absence de modèle doit être EXPLIQUÉE, pas silencieuse")
	}

	// Stratégie sans aucun entraînement archivé.
	if _, why := SelectModel(cfg.Paths.ModelsDir(), "colibri_v1_0", "EURUSD"); why == "" {
		t.Fatal("une stratégie sans run doit être signalée")
	}
}

func TestDeleteRunRefusesPathOutsideModelsDir(t *testing.T) {
	cfg, _ := testSetup(t)
	if err := DeleteRun(cfg.Paths.ModelsDir(), "/etc"); err == nil {
		t.Fatal("supprimer hors du dossier des modèles doit être REFUSÉ")
	}
	if err := DeleteRun(cfg.Paths.ModelsDir(), cfg.Paths.ModelsDir()); err == nil {
		t.Fatal("supprimer le dossier des modèles lui-même doit être refusé")
	}
}
