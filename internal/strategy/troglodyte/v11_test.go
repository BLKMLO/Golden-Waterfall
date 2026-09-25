package troglodyte

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

func v11() revision {
	for _, r := range revisions {
		if r.name == "troglodyte_v1_1" {
			return r
		}
	}
	panic("troglodyte_v1_1 absente")
}

// La normalisation rend z indépendant de l'échelle de volatilité : deux
// séries dont les rendements ne diffèrent que d'un facteur ont le même
// chemin normalisé.
func TestNormalizedPathIsScaleFree(t *testing.T) {
	calm := driftSeries(800, 0.0001, 5)
	y1, _ := logCloses(calm)
	y2 := make([]float64, len(y1))
	for i := range y1 {
		y2[i] = 3 * y1[i] // rendements triplés
	}
	x1, ok1 := normalizedPath(y1, 30)
	x2, ok2 := normalizedPath(y2, 30)
	if !ok1 || !ok2 {
		t.Fatal("normalisation impossible")
	}
	for i := range x1 {
		if math.Abs(x1[i]-x2[i]) > 1e-9 {
			t.Fatalf("bougie %d : %v contre %v — le chemin dépend de l'échelle", i, x1[i], x2[i])
		}
	}
	if _, ok := normalizedPath(make([]float64, 100), 30); ok {
		t.Fatal("des prix figés n'ont pas d'échelle : refus attendu")
	}
}

func TestChandelierRule(t *testing.T) {
	ru := rule{enterZ: 1.5, exitZ: 0.5, stopATR: 3, trail: true}
	// hh 110, ATR 1 : le stop suiveur long est à 107.
	base := barState{z: 2, close: 109, atr: 1, hh: 110, ll: 100}
	if a, stop := decide(base, ru); a != core.EnterLong || stop != 106 {
		t.Fatalf("pente nette, prix au-dessus du suiveur : entrée longue, stop 106 attendus ; reçu %s %v", a, stop)
	}
	// Prix sous le suiveur long (107) alors que la pente reste positive :
	// la condition de sortie courte est vraie elle aussi (z > −s_out), la
	// sortie vaut pour les deux sens — la position longue sort.
	fell := base
	fell.close = 106.5
	if a, _ := decide(fell, ru); a != core.Exit || !a.Closes(1) {
		t.Fatalf("prix sous le suiveur long : sortie attendue, reçu %s", a)
	}
	// Pente négative mais au-dessus de −s_in (pas d'entrée courte), prix
	// sous le suiveur court (103) : une position courte reste justifiée,
	// seule une position LONGUE doit sortir.
	down := barState{z: -1.0, close: 102, atr: 1, hh: 110, ll: 100}
	if a, _ := decide(down, ru); a != core.ExitLong {
		t.Fatalf("pente négative sous le seuil d'entrée, prix bas : ExitLong seule attendue, reçu %s", a)
	}
	flat := base
	flat.z = 0.1
	if a, _ := decide(flat, ru); a != core.Exit {
		t.Fatalf("pente nulle : sortie des deux sens attendue, reçu %s", a)
	}
	mid := base
	mid.z = 1.0
	if a, _ := decide(mid, ru); a != core.ExitShort {
		t.Fatalf("pente positive mais sous le seuil d'entrée : seule une position COURTE sort, reçu %s", a)
	}
}

func trainV11(t *testing.T, series core.Series) (*troglodyte, *modelMeta) {
	t.Helper()
	s := newTroglodyte(v11())
	dir := t.TempDir()
	if _, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series}, Timeframe: data.H4, OutputDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{Symbol: "EURUSD", Timeframe: data.H4, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	return s, s.modelFor("EURUSD")
}

func TestCalibrationPicksAGridPointAndRecordsTheGrid(t *testing.T) {
	_, m := trainV11(t, driftSeries(3000, 0.0003, 21))
	r := v11()
	if m.Calibration == nil || len(m.Calibration.Grid) != len(r.enterGrid)*len(r.stopGrid) {
		t.Fatalf("la grille entière doit être archivée : %+v", m.Calibration)
	}
	if !r.sameDefinition(m) {
		t.Fatalf("le modèle calibré doit être reconnu par sa révision : %+v", m)
	}
	if m.ExitZ != m.EnterZ*r.exitRatio {
		t.Fatalf("s_out = s_in / 3 attendu : %v / %v", m.ExitZ, m.EnterZ)
	}
	t.Logf("retenu : s_in %v, k %v, repli %v", m.EnterZ, m.StopATR, m.Calibration.Fallback)
	for _, gp := range m.Calibration.Grid {
		score := "—"
		if gp.Score != nil {
			score = formatFloat(*gp.Score)
		}
		t.Logf("  s_in %.1f k %.0f : %d trades, score %s", gp.EnterZ, gp.StopATR, gp.Trades, score)
	}
	// Un seuil hors grille est une autre définition : refusé.
	forged := *m
	forged.EnterZ = 1.7
	if r.sameDefinition(&forged) {
		t.Fatal("un seuil hors de la grille ne doit pas être accepté")
	}
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// Le calibrage rejoue la règle « comme le moteur » : ce test le vérifie
// contre le VRAI moteur de backtest, trade par trade — mêmes entrées, mêmes
// sorties, mêmes prix.
func TestCalibrationSimulationMatchesTheBacktestEngine(t *testing.T) {
	series := driftSeries(2500, 0.0003, 33)
	s, m := trainV11(t, series[:1500])

	states, _, err := s.decisionInputs(context.Background(), series, m.params)
	if err != nil {
		t.Fatal(err)
	}
	sim := simulateTrades(series, states, m.rule())

	cfg := config.Default()
	cfg.Risk.RiskPerTradePct = 0
	cfg.Backtest.AccountCurrency = "USD"
	engine := backtest.NewEngine(cfg, risk.New(cfg.Risk, "USD", slog.New(slog.DiscardHandler)))
	res, err := engine.Run(context.Background(), backtest.Request{
		Symbol: "EURUSD", Series: series, From: 0, Strategy: s, Timeframe: data.H4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sim) == 0 || len(sim) != len(res.Trades) {
		t.Fatalf("%d trades simulés, %d au moteur", len(sim), len(res.Trades))
	}
	for k, st := range sim {
		tr := res.Trades[k]
		side := core.Buy
		if st.side < 0 {
			side = core.Sell
		}
		if tr.Side != side || !tr.EntryTime.Equal(series[st.entryIdx].Time) || !tr.ExitTime.Equal(series[st.exitIdx].Time) ||
			tr.EntryPrice != st.entry || tr.ExitPrice != st.exit {
			t.Fatalf("trade %d diverge :\n  simulé %+v (entrée %s, sortie %s)\n  moteur %+v",
				k, st, series[st.entryIdx].Time, series[st.exitIdx].Time, tr)
		}
	}
	t.Logf("%d trades identiques entre le calibrage et le moteur", len(sim))
}

func TestCalibrationFallsBackWithoutEnoughTrades(t *testing.T) {
	r := v11()
	r.minCalibTrades = 1_000_000
	s := newTroglodyte(r)
	series := driftSeries(1500, 0.0003, 4)
	y, _ := logCloses(series)
	x, _ := normalizedPath(y, r.volHalfLife)
	f, err := estimate(x)
	if err != nil {
		t.Fatal(err)
	}
	ru, cal, err := s.calibrate(context.Background(), series, f.Params)
	if err != nil {
		t.Fatal(err)
	}
	if !cal.Fallback || ru.enterZ != r.enterZ || ru.stopATR != r.stopATR {
		t.Fatalf("sans point jugeable, les valeurs de repli doivent être gardées ET signalées : %+v %+v", ru, cal)
	}
}

func TestV11DeclaresNewsAndDirectionalExits(t *testing.T) {
	d := newTroglodyte(v11()).Describe()
	if !d.UsesNews || !d.HoldsOverWeekend || !d.ExitOnReversal {
		t.Fatalf("déclarations attendues : %+v", d)
	}
	if newTroglodyte(revisions[0]).Describe().UsesNews {
		t.Fatal("troglodyte_v1_0 est publiée sans filtre d'actualités : elle ne doit pas le déclarer")
	}
}
