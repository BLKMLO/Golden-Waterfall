package troglodyte

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// driftSeries : prix H4 en tendance (dérive `drift` par bougie en
// logarithme, bruit gaussien), sans week-end.
func driftSeries(n int, drift float64, seed int64) core.Series {
	rng := rand.New(rand.NewSource(seed))
	t := time.Date(2020, 1, 6, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, 0, n)
	logP := math.Log(1.1)
	for len(out) < n {
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
			t = t.Add(4 * time.Hour)
			continue
		}
		open := math.Exp(logP)
		logP += drift + rng.NormFloat64()*0.002
		c := math.Exp(logP)
		out = append(out, core.Bar{Time: t, BidOpen: open, BidClose: c,
			BidHigh: math.Max(open, c) * 1.0005, BidLow: math.Min(open, c) * 0.9995})
		t = t.Add(4 * time.Hour)
	}
	return out
}

func trained(t *testing.T, symbol string, series core.Series) (*troglodyte, string) {
	t.Helper()
	s := newTroglodyte(revisions[0])
	dir := t.TempDir()
	rep, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{symbol: series}, Timeframe: data.H4, OutputDir: dir, Seed: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Samples != len(series)-2 || rep.Features != 1 || rep.Symbols[0] != symbol {
		t.Fatalf("rapport incohérent : %+v", rep)
	}
	return s, dir
}

// Une tendance nette doit être SUIVIE : entrées dans son sens, aucune à
// contresens.
func TestFollowsTheTrendInBothDirections(t *testing.T) {
	for _, c := range []struct {
		drift float64
		want  core.SignalAction
	}{{+0.0006, core.EnterLong}, {-0.0006, core.EnterShort}} {
		series := driftSeries(2500, c.drift, 3)
		_, dir := trained(t, "EURUSD", series[:1500])
		s := newTroglodyte(revisions[0])
		if err := s.Warmup(context.Background(), strategy.WarmupRequest{
			Symbol: "EURUSD", Series: series, Timeframe: data.H4, ModelDir: dir,
		}); err != nil {
			t.Fatal(err)
		}
		with, against := 0, 0
		for i := 1500; i < len(series); i++ {
			sig, err := s.OnBar(context.Background(), "EURUSD", series, i)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case sig.Action == c.want:
				with++
			case sig.Action.IsEntry():
				against++
			}
		}
		t.Logf("dérive %+v : %d entrée(s) dans le sens, %d à contresens, sur %d bougies",
			c.drift, with, against, len(series)-1500)
		if with == 0 || against != 0 {
			t.Fatalf("dérive %+v : %d entrée(s) dans le sens, %d à contresens", c.drift, with, against)
		}
	}
}

func TestEntriesCarryAStopAndNoLimit(t *testing.T) {
	series := driftSeries(2500, 0.0006, 4)
	s, dir := trained(t, "EURUSD", series[:1500])
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1500; i < len(series); i++ {
		sig, _ := s.OnBar(context.Background(), "EURUSD", series, i)
		if sig.Action != core.EnterLong {
			continue
		}
		if sig.TakeProfit != 0 {
			t.Fatalf("un suivi de tendance ne pose pas de limite : %+v", sig)
		}
		atr := windowATR(series[i+1-revisions[0].window:i+1], revisions[0].atrPeriod)
		if want := sig.Price - revisions[0].stopATR*atr; math.Abs(sig.StopLoss-want) > 1e-12 {
			t.Fatalf("stop %v, %v attendu (close − 3 × ATR)", sig.StopLoss, want)
		}
		return
	}
	t.Fatal("aucune entrée longue sur une tendance haussière")
}

func TestModelIsRefusedOutsideItsPairTimeframeAndStrategy(t *testing.T) {
	series := driftSeries(1200, 0.0003, 5)
	_, dir := trained(t, "EURUSD", series)
	ctx := context.Background()
	cases := []struct {
		name string
		req  strategy.WarmupRequest
		want string
	}{
		{"autre paire", strategy.WarmupRequest{Symbol: "GBPUSD", Timeframe: data.H4, ModelDir: dir}, "GBPUSD"},
		{"autre unité", strategy.WarmupRequest{Symbol: "EURUSD", Timeframe: data.H1, ModelDir: dir}, "H1"},
	}
	for _, c := range cases {
		s := newTroglodyte(revisions[0])
		err := s.Warmup(ctx, c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s : refus attendu nommant %q, reçu %v", c.name, c.want, err)
		}
		if ready, why := s.Ready(); ready || why == "" {
			t.Fatalf("%s : la stratégie doit rester non prête et dire pourquoi", c.name)
		}
	}
	other := revisions[0]
	other.name = "troglodyte_autre"
	if _, err := other.loadModel(dir, "EURUSD", "H4"); err == nil {
		t.Fatal("un modèle d'une autre révision doit être refusé")
	}
	changed := revisions[0]
	changed.enterZ = 2
	if _, err := changed.loadModel(dir, "EURUSD", "H4"); err == nil {
		t.Fatal("un modèle estimé sous une autre définition doit être refusé")
	}
}

func TestTrainRefusesWhatItCannotEstimate(t *testing.T) {
	s := newTroglodyte(revisions[0])
	ctx := context.Background()
	two := map[string]core.Series{"EURUSD": driftSeries(1200, 0, 1), "GBPUSD": driftSeries(1200, 0, 2)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: two, Timeframe: data.H4, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("un entraînement mutualisé doit être refusé")
	}
	short := map[string]core.Series{"EURUSD": driftSeries(revisions[0].minTrainBars-1, 0, 1)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: short, Timeframe: data.H4, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("trop peu de bougies : refus attendu")
	}
	bad := driftSeries(1200, 0, 1)
	bad[600].BidClose = 0
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: map[string]core.Series{"EURUSD": bad},
		Timeframe: data.H4, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("un prix nul rend le logarithme impossible : refus attendu")
	}
}

func TestDeclaresItsExecutionRules(t *testing.T) {
	d := newTroglodyte(revisions[0]).Describe()
	if !d.HoldsOverWeekend || !d.ExitOnReversal || d.MaxHold != 0 || d.ContextBars != revisions[0].window {
		t.Fatalf("règles d'exécution déclarées inattendues : %+v", d)
	}
}
