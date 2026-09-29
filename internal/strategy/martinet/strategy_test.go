package martinet

import (
	"context"
	"log/slog"
	"math"
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

func v10() revision { return revisions[0] }

// flatWindow : une fenêtre M5 calme autour de 1,1000, bougies de 6 pips,
// qui se termine un mardi à 10 h UTC (en séance).
func flatWindow(n int) core.Series {
	end := time.Date(2024, 3, 5, 10, 0, 0, 0, time.UTC)
	out := make(core.Series, n)
	for i := range out {
		t := end.Add(-time.Duration(n-1-i) * 5 * time.Minute)
		out[i] = core.Bar{Time: t, BidOpen: 1.1000, BidClose: 1.1000, BidHigh: 1.1003, BidLow: 1.0997}
	}
	return out
}

// withSwingHigh pose un plus haut de swing net à l'indice j.
func withSwingHigh(w core.Series, j int, level float64) {
	w[j].BidHigh = level
}

func TestSweepAboveASwingHighThatClosesBackBelowIsASell(t *testing.T) {
	r := v10()
	w := flatWindow(r.window)
	cur := len(w) - 1
	withSwingHigh(w, cur-20, 1.1006)
	// Bougie de décision : pointe à 1,1008 (au-dessus de la zone), close
	// 1,1002 (revenu dessous).
	w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.1000, BidClose: 1.1002}
	s := r.setupAt(w, 3)
	if s.side != -1 || s.level != 1.1006 || s.entry != 1.1002 {
		t.Fatalf("vente attendue sur la zone 1,1006 : %+v", s)
	}
	atr := windowATR(w, r.atrPeriod)
	if want := 1.1008 + r.stopBufferATR*atr; math.Abs(s.stop-want) > 1e-12 {
		t.Fatalf("stop %v, %v attendu (haut de la mèche + tampon)", s.stop, want)
	}
	if tp := s.target(1.5); math.Abs(tp-(s.entry-1.5*s.risk)) > 1e-12 || tp >= s.entry {
		t.Fatalf("cible %v mal placée", tp)
	}
}

func TestNoSetupWithoutRejectionOrOnATakenZone(t *testing.T) {
	r := v10()
	cases := map[string]func(w core.Series, cur int){
		// Close AU-DESSUS de la zone : c'est une cassure, pas un rejet.
		"cassure": func(w core.Series, cur int) {
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.1000, BidClose: 1.1007}
		},
		// Zone déjà prise par une bougie antérieure : plus de liquidité.
		"zone deja prise": func(w core.Series, cur int) {
			w[cur-2].BidHigh = 1.1007
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.1000, BidClose: 1.1002}
		},
		// Hors séance : 3 h UTC.
		"hors seance": func(w core.Series, cur int) {
			for i := range w {
				w[i].Time = w[i].Time.Add(-7 * time.Hour)
			}
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.1000, BidClose: 1.1002}
		},
		// Dépassement de plus d'un ATR : un marché qui part.
		"depassement profond": func(w core.Series, cur int) {
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1040, BidLow: 1.1000, BidClose: 1.1002}
		},
		// Spread supérieur au quart du risque.
		"spread": func(w core.Series, cur int) {
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.1000, BidClose: 1.1002,
				AskOpen: 1.1004, AskHigh: 1.1011, AskLow: 1.1003, AskClose: 1.1005}
		},
		// Balayage des deux côtés : la bougie ne dit rien.
		"deux cotes": func(w core.Series, cur int) {
			w[cur-30].BidLow = 1.0994
			w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.1001, BidHigh: 1.1008, BidLow: 1.0992, BidClose: 1.1002}
		},
	}
	for name, mutate := range cases {
		w := flatWindow(r.window)
		cur := len(w) - 1
		withSwingHigh(w, cur-20, 1.1006)
		mutate(w, cur)
		if s := r.setupAt(w, 3); s.side != 0 {
			t.Errorf("%s : aucune entrée attendue, reçu %+v", name, s)
		}
	}
}

func TestSweepBelowASwingLowIsABuy(t *testing.T) {
	r := v10()
	w := flatWindow(r.window)
	cur := len(w) - 1
	w[cur-40].BidLow = 1.0994
	w[cur] = core.Bar{Time: w[cur].Time, BidOpen: 1.0999, BidHigh: 1.1000, BidLow: 1.0992, BidClose: 1.0998}
	s := r.setupAt(w, 5)
	if s.side != +1 || s.level != 1.0994 || s.stop >= 1.0992 || s.target(2) <= s.entry {
		t.Fatalf("achat attendu sous la zone 1,0994 : %+v", s)
	}
	// Une zone trop ancienne n'en est plus une.
	w2 := flatWindow(r.window)
	w2[cur-r.lookback-1].BidLow = 1.0994
	w2[cur] = w[cur]
	if s := r.setupAt(w2, 5); s.side != 0 {
		t.Fatalf("zone plus ancienne que %d bougies : aucune entrée attendue, %+v", r.lookback, s)
	}
}

// TestCalibrationSimulationMatchesTheBacktestEngine : les trades que le
// calibrage rejoue sont, un pour un, ceux que le moteur de backtest
// produit avec la stratégie réelle — entrée, sortie, prix et motif.
func TestCalibrationSimulationMatchesTheBacktestEngine(t *testing.T) {
	r := v10()
	series := strategytest.SeriesAt(data.M5, 9000, 17, 1.10)
	for _, c := range []struct {
		rr    float64
		pivot int
	}{{1.5, 3}, {2, 5}, {1, 3}} {
		dir := t.TempDir()
		meta := r.newMeta()
		meta.Symbol, meta.Timeframe, meta.RR, meta.Pivot = "EURUSD", "M5", c.rr, c.pivot
		meta.Calibration = &calibration{}
		if err := meta.save(dir); err != nil {
			t.Fatal(err)
		}
		s := newMartinet(r)
		if err := s.Warmup(context.Background(), strategy.WarmupRequest{
			Symbol: "EURUSD", Timeframe: data.M5, ModelDir: dir}); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Risk.RiskPerTradePct = 0
		cfg.Risk.FixedPositionSize = 1000
		cfg.Risk.MaxPositionSize = 1_000_000
		cfg.Costs.CommissionPerUnit = 0
		engine := backtest.NewEngine(cfg, risk.New(cfg.Risk, "USD", slog.New(slog.DiscardHandler)))
		res, err := engine.Run(context.Background(), backtest.Request{
			Symbol: "EURUSD", Series: series, From: r.window, Strategy: s, Timeframe: data.M5})
		if err != nil {
			t.Fatal(err)
		}
		setups, _ := r.setups(context.Background(), series, r.window, c.pivot)
		sim := r.simulateTrades(series, setups, c.rr, data.M5.Duration())
		if len(sim) != len(res.Trades) || len(sim) < 20 {
			t.Fatalf("rr %v pivot %d : %d trades simulés, %d au moteur (20 au moins attendus)",
				c.rr, c.pivot, len(sim), len(res.Trades))
		}
		for k, tr := range res.Trades {
			st := sim[k]
			if !tr.EntryTime.Equal(series[st.entryIdx].Time) || !tr.ExitTime.Equal(series[st.exitIdx].Time) ||
				tr.EntryPrice != st.entry || tr.ExitPrice != st.exit || tr.ExitReason != st.reason {
				t.Fatalf("trade %d : moteur %s→%s %v→%v (%s), simulation %s→%s %v→%v (%s)", k,
					tr.EntryTime, tr.ExitTime, tr.EntryPrice, tr.ExitPrice, tr.ExitReason,
					series[st.entryIdx].Time, series[st.exitIdx].Time, st.entry, st.exit, st.reason)
			}
		}
		t.Logf("rr %v pivot %d : %d trades identiques", c.rr, c.pivot, len(sim))
	}
}

func TestTrainCalibratesAndRecordsTheGrid(t *testing.T) {
	r := v10()
	series := strategytest.SeriesAt(data.M5, 12000, 21, 1.10)
	s := newMartinet(r)
	dir := t.TempDir()
	rep, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series}, Timeframe: data.M5, OutputDir: dir, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.loadModel(dir, "EURUSD", "M5")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calibration.Grid) != len(r.rrGrid)*len(r.pivotGrid) || !m.Calibration.CostsModelled {
		t.Fatalf("grille archivée incomplète : %+v", m.Calibration)
	}
	if rep.Metrics["calibrage_rr"] != m.RR || rep.Metrics["calibrage_pivot"] != float64(m.Pivot) {
		t.Fatalf("rapport et manifeste divergent : %+v / rr %v pivot %d", rep.Metrics, m.RR, m.Pivot)
	}
	if !m.Calibration.Fallback {
		// Le réglage retenu est le meilleur score jugeable de la grille.
		best := math.Inf(-1)
		for _, gp := range m.Calibration.Grid {
			if gp.Score != nil && *gp.Score > best {
				best = *gp.Score
			}
		}
		if rep.Metrics["score_calibrage"] != best {
			t.Fatalf("score retenu %v, meilleur de la grille %v", rep.Metrics["score_calibrage"], best)
		}
	}
	t.Logf("calibrage : rr %v, pivot %d, repli %v, %v trades", m.RR, m.Pivot, m.Calibration.Fallback,
		rep.Metrics["trades_calibrage"])
}

func TestCalibrationFallsBackWithoutEnoughTrades(t *testing.T) {
	r := v10()
	r.minCalibTrades = 1_000_000
	rr, pivot, cal, err := r.calibrate(context.Background(),
		strategytest.SeriesAt(data.M5, 3000, 5, 1.10), data.M5.Duration())
	if err != nil {
		t.Fatal(err)
	}
	if !cal.Fallback || rr != r.fallbackRR || pivot != r.fallbackPivot {
		t.Fatalf("sans point jugeable, le repli doit être gardé ET signalé : rr %v pivot %d %+v", rr, pivot, cal)
	}
}

func TestRefusesWhatItCannotDo(t *testing.T) {
	r := v10()
	ctx := context.Background()
	s := newMartinet(r)
	one := map[string]core.Series{"EURUSD": strategytest.SeriesAt(data.M5, 3000, 1, 1.10)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: one, Timeframe: data.H4, OutputDir: t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "M1, M5, M15") {
		t.Fatalf("H4 : refus nommant les unités acceptées attendu, reçu %v", err)
	}
	two := map[string]core.Series{"EURUSD": one["EURUSD"], "GBPUSD": strategytest.SeriesAt(data.M5, 3000, 2, 1.27)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: two, Timeframe: data.M5, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("un calibrage mutualisé doit être refusé")
	}
	short := map[string]core.Series{"EURUSD": strategytest.SeriesAt(data.M5, r.minTrainBars-1, 1, 1.10)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: short, Timeframe: data.M5, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("trop peu de bougies : refus attendu")
	}

	dir := t.TempDir()
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: one, Timeframe: data.M5, OutputDir: dir}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]strategy.WarmupRequest{
		"GBPUSD": {Symbol: "GBPUSD", Timeframe: data.M5, ModelDir: dir},
		"M15":    {Symbol: "EURUSD", Timeframe: data.M15, ModelDir: dir},
	} {
		fresh := newMartinet(r)
		if err := fresh.Warmup(ctx, req); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("modèle EURUSD M5 chargé pour %s : refus nommant %s attendu, reçu %v", name, name, err)
		}
		if ready, why := fresh.Ready(); ready || why == "" {
			t.Fatalf("%s : la stratégie doit rester non prête et dire pourquoi", name)
		}
	}
	changed := r
	changed.maxSpreadR = 0.5
	if _, err := changed.loadModel(dir, "EURUSD", "M5"); err == nil {
		t.Fatal("un modèle calibré sous une autre définition doit être refusé")
	}
}

func TestDeclaresItsExecutionRules(t *testing.T) {
	d := newMartinet(v10()).Describe()
	if d.HoldsOverWeekend || d.ExitOnReversal || d.UsesVolume || !d.UsesNews ||
		d.MaxHold != 2*time.Hour || d.ContextBars != v10().window {
		t.Fatalf("règles d'exécution déclarées inattendues : %+v", d)
	}
	if strategy.CheckTimeframe(d, data.M5) != nil || strategy.CheckTimeframe(d, data.H1) == nil {
		t.Fatal("M5 accepté, H1 refusé attendus")
	}
}
