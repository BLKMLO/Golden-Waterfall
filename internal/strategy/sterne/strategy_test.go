package sterne

import (
	"context"
	"log/slog"
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

func v10() revision { return revisions[0] }

func mustWindow(t *testing.T, name string) window {
	t.Helper()
	w, ok := v10().windowByName(name)
	if !ok {
		t.Fatalf("fenêtre %q absente de la grille", name)
	}
	return w
}

// flatWindow : n bougies M15 calmes qui FINISSENT à la bougie commençant
// à `last`.
func flatWindow(n int, last time.Time) core.Series {
	w := make(core.Series, n)
	for i := range w {
		w[i] = core.Bar{Time: last.Add(time.Duration(i-n+1) * 15 * time.Minute),
			BidOpen: 1.1000, BidHigh: 1.1004, BidLow: 1.0996, BidClose: 1.1000,
			AskOpen: 1.1001, AskHigh: 1.1005, AskLow: 1.0997, AskClose: 1.1001}
	}
	return w
}

// driftSeries : marche au hasard M15 sur `days` jours ouvrés, à laquelle
// on ajoute une DÉRIVE de `drift` par bougie pendant les deux heures qui
// suivent 12 h 15 à Francfort — l'effet que « francfort_avant » doit
// retrouver. drift < 0 : la paire baisse, donc le dollar monte sur EURUSD.
func driftSeries(days int, seed int64, drift float64) core.Series {
	rng := rand.New(rand.NewSource(seed))
	w, _ := v10().windowByName("francfort_avant")
	t := time.Date(2020, 1, 6, 0, 0, 0, 0, time.UTC) // un lundi
	price := 1.10
	var out core.Series
	for d := 0; d < days; t = t.Add(15 * time.Minute) {
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		open := price
		price += rng.NormFloat64() * 0.0003
		local := t.In(w.loc)
		since := (local.Hour()*60 + local.Minute()) - (w.hour*60 + w.minute)
		if since >= 0 && since < 120 {
			price += drift
		}
		hi, lo := open, price
		if lo > hi {
			hi, lo = lo, hi
		}
		out = append(out, core.Bar{Time: t,
			BidOpen: open, BidHigh: hi + 0.0001, BidLow: lo - 0.0001, BidClose: price,
			AskOpen: open + 0.00002, AskHigh: hi + 0.00012, AskLow: lo - 0.00008, AskClose: price + 0.00002})
		if t.Hour() == 23 && t.Minute() == 45 {
			d++
		}
	}
	return out
}

// TestWindowsFollowTheClockOfTheirPlace : un fixing est à l'heure de SA
// place. Francfort 12 h 15 vaut 11 h 15 UTC en hiver et 10 h 15 UTC en
// été ; Tokyo, sans heure d'été, ne bouge pas.
func TestWindowsFollowTheClockOfTheirPlace(t *testing.T) {
	bar := 15 * time.Minute
	fra := mustWindow(t, "francfort_avant")
	for _, c := range []struct {
		start time.Time
		want  bool
	}{
		{time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC), true},  // hiver : finit à 11 h 15 UTC
		{time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), false}, // l'heure d'été, en hiver
		{time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC), true},  // été : finit à 10 h 15 UTC
		{time.Date(2024, 7, 15, 11, 0, 0, 0, time.UTC), false},
		{time.Date(2024, 7, 15, 10, 15, 0, 0, time.UTC), false}, // la bougie suivante
	} {
		if got := fra.opens(c.start, bar); got != c.want {
			t.Errorf("francfort_avant, bougie de %s : ouverture %v, attendu %v", c.start, got, c.want)
		}
	}
	tok := mustWindow(t, "tokyo_apres")
	for _, day := range []time.Time{time.Date(2024, 1, 15, 0, 45, 0, 0, time.UTC), time.Date(2024, 7, 15, 0, 45, 0, 0, time.UTC)} {
		if !tok.opens(day, bar) {
			t.Errorf("tokyo_apres : 10 h à Tokyo = 1 h UTC toute l'année, bougie de %s refusée", day)
		}
	}
}

// TestBuysTheDollarBeforeAFixingAndSellsItAfter : le sens se lit sur le
// DOLLAR, donc à l'envers selon que la paire le cote ou le porte.
func TestBuysTheDollarBeforeAFixingAndSellsItAfter(t *testing.T) {
	r := v10()
	before, after := mustWindow(t, "tokyo_avant"), mustWindow(t, "tokyo_apres")
	// 8 h à Tokyo = 23 h UTC la veille ; 10 h = 1 h UTC.
	wBefore := flatWindow(r.atrWindow, time.Date(2024, 3, 12, 22, 45, 0, 0, time.UTC))
	wAfter := flatWindow(r.atrWindow, time.Date(2024, 3, 13, 0, 45, 0, 0, time.UTC))
	for _, c := range []struct {
		symbol string
		win    core.Series
		active []window
		side   int
	}{
		{"USDJPY", wBefore, []window{before}, +1},
		{"EURUSD", wBefore, []window{before}, -1},
		{"USDJPY", wAfter, []window{after}, -1},
		{"EURUSD", wAfter, []window{after}, +1},
		{"EURUSD", wAfter, []window{before}, 0}, // fenêtre non ouverte à cette heure
		{"EURGBP", wAfter, []window{after}, 0},  // pas de dollar
	} {
		s := r.setupAt(c.win, usdSide(c.symbol), c.active)
		if s.side != c.side {
			t.Fatalf("%s : sens %d, attendu %d", c.symbol, s.side, c.side)
		}
		if s.side == 0 {
			continue
		}
		atr := windowATR(c.win, r.atrPeriod)
		if want := s.entry - float64(s.side)*r.stopATR*atr; s.stop != want || s.risk != r.stopATR*atr {
			t.Fatalf("%s : stop %v, attendu %v (entrée ∓ %g ATR)", c.symbol, s.stop, want, r.stopATR)
		}
	}
	// Une fenêtre trop courte pour l'ATR : abstention, pas un stop inventé.
	if s := r.setupAt(wAfter[1:], -1, []window{after}); s.side != 0 {
		t.Fatal("fenêtre incomplète : abstention attendue")
	}
	// Deux fenêtres de sens opposés sur la même bougie ne disent rien.
	twin := after
	twin.usd = +1
	if s := r.setupAt(wAfter, -1, []window{after, twin}); s.side != 0 {
		t.Fatal("deux fenêtres opposées sur la même bougie : abstention attendue")
	}
}

// TestCalibrationSimulationMatchesTheBacktestEngine : le calibrage juge
// exactement ce que le moteur exécutera — trade par trade.
func TestCalibrationSimulationMatchesTheBacktestEngine(t *testing.T) {
	r := v10()
	series := strategytest.SeriesAt(data.M15, 9000, 17, 1.10)
	for _, c := range []struct {
		symbol string
		name   string
	}{{"EURUSD", "francfort_avant"}, {"USDJPY", "tokyo_apres"}, {"EURUSD", "londres_apres"}} {
		dir := t.TempDir()
		meta := r.newMeta()
		meta.Symbol, meta.Timeframe, meta.Windows = c.symbol, "M15", []string{c.name}
		meta.Calibration = &calibration{}
		if err := meta.save(dir); err != nil {
			t.Fatal(err)
		}
		s := newSterne(r)
		if err := s.Warmup(context.Background(), strategy.WarmupRequest{
			Symbol: c.symbol, Timeframe: data.M15, ModelDir: dir}); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Risk.RiskPerTradePct = 0
		cfg.Risk.FixedPositionSize = 1000
		cfg.Risk.MaxPositionSize = 1_000_000
		cfg.Costs.CommissionPerUnit = 0
		engine := backtest.NewEngine(cfg, risk.New(cfg.Risk, "USD", slog.New(slog.DiscardHandler)))
		res, err := engine.Run(context.Background(), backtest.Request{
			Symbol: c.symbol, Series: series, From: r.atrWindow, Strategy: s, Timeframe: data.M15})
		if err != nil {
			t.Fatal(err)
		}
		setups, _ := r.setups(context.Background(), series, r.atrWindow, usdSide(c.symbol), []window{mustWindow(t, c.name)})
		sim := r.simulateTrades(series, setups)
		if len(sim) != len(res.Trades) || len(sim) < 20 {
			t.Fatalf("%s %s : %d trades simulés, %d au moteur (20 au moins attendus)",
				c.symbol, c.name, len(sim), len(res.Trades))
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
		t.Logf("%s %s : %d trades identiques", c.symbol, c.name, len(sim))
	}
}

// TestPositionIsHeldTwoHoursThenClosedOnTime : la sortie normale est
// l'heure, exactement deux heures après l'entrée.
func TestPositionIsHeldTwoHoursThenClosedOnTime(t *testing.T) {
	r := v10()
	series := driftSeries(40, 3, 0)
	setups, _ := r.setups(context.Background(), series, 0, -1, []window{mustWindow(t, "francfort_avant")})
	trades := r.simulateTrades(series, setups)
	if len(trades) < 30 {
		t.Fatalf("%d trades sur 40 jours : un par jour ouvré attendu", len(trades))
	}
	onTime := 0
	for _, tr := range trades {
		if tr.reason != "time" {
			continue
		}
		onTime++
		if held := series[tr.exitIdx].Time.Sub(series[tr.entryIdx].Time); held != r.hold {
			t.Fatalf("sortie à l'heure après %s, attendu %s", held, r.hold)
		}
	}
	if onTime < len(trades)*8/10 {
		t.Fatalf("%d sorties à l'heure sur %d : un stop à %g ATR ne devrait pas être la sortie courante", onTime, len(trades), r.stopATR)
	}
}

// TestTrainKeepsTheWindowThatCarriesAnEffect : une dérive plantée dans
// UNE fenêtre est retrouvée, archivée, et tradée par le modèle rechargé.
func TestTrainKeepsTheWindowThatCarriesAnEffect(t *testing.T) {
	r := v10()
	series := driftSeries(300, 7, -0.00006)
	s := newSterne(r)
	dir := t.TempDir()
	rep, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series}, Timeframe: data.M15, OutputDir: dir, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.loadModel(dir, "EURUSD", "M15")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calibration.Windows) != len(r.windows) || !m.Calibration.CostsModelled {
		t.Fatalf("grille archivée incomplète : %+v", m.Calibration)
	}
	kept := false
	for _, name := range m.Windows {
		kept = kept || name == "francfort_avant"
	}
	if !kept {
		t.Fatalf("la fenêtre qui porte la dérive n'a pas été retenue : %v (%+v)", m.Windows, m.Calibration.Windows)
	}
	for _, wr := range m.Calibration.Windows {
		in := false
		for _, name := range m.Windows {
			in = in || name == wr.Window
		}
		passes := wr.Score != nil && *wr.Score >= r.minScore && wr.Trades >= r.minTrades
		if wr.Selected != in || in != passes {
			t.Fatalf("%s : retenue=%v, au manifeste=%v, passe le seuil=%v", wr.Window, wr.Selected, in, passes)
		}
	}
	if rep.Metrics["fenetres_retenues"] != float64(len(m.Windows)) || rep.Metrics["fenetres_essayees"] != float64(len(r.windows)) {
		t.Fatalf("rapport et manifeste divergent : %+v / %v", rep.Metrics, m.Windows)
	}
	// Dans l'autre sens, la même fenêtre perd : elle doit être écartée.
	against := newSterne(r)
	dir2 := t.TempDir()
	if _, err := against.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": driftSeries(300, 7, +0.00006)}, Timeframe: data.M15, OutputDir: dir2}); err != nil {
		t.Fatal(err)
	}
	m2, _ := r.loadModel(dir2, "EURUSD", "M15")
	for _, name := range m2.Windows {
		if name == "francfort_avant" {
			t.Fatal("une fenêtre qui perd à l'entraînement a été retenue")
		}
	}

	fresh := newSterne(r)
	if err := fresh.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Timeframe: data.M15, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	entries := 0
	for i := r.atrWindow; i < 3000; i++ {
		sig, err := fresh.OnBar(context.Background(), "EURUSD", series, i)
		if err != nil {
			t.Fatal(err)
		}
		if sig.Action.IsEntry() {
			entries++
			if sig.TakeProfit != 0 || sig.Confidence != 0 {
				t.Fatalf("ni cible ni confiance attendues : %+v", sig)
			}
		}
	}
	if entries == 0 {
		t.Fatal("le modèle rechargé ne trade pas la fenêtre retenue")
	}
}

// TestModelWithoutWindowIsValidAndSilent : aucune fenêtre ne passe le
// seuil (trop peu de trades) — le modèle existe, ne trade pas, et
// l'entraînement le dit.
func TestModelWithoutWindowIsValidAndSilent(t *testing.T) {
	r := v10()
	series := strategytest.SeriesAt(data.M15, 3000, 5, 1.10)
	s := newSterne(r)
	dir := t.TempDir()
	rep, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series}, Timeframe: data.M15, OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Metrics["fenetres_retenues"] != 0 {
		t.Fatalf("%v fenêtre(s) retenue(s) sur un mois d'historique : il en faut %d trades", rep.Metrics["fenetres_retenues"], r.minTrades)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, strategy.ModelManifest))
	if !strings.Contains(string(raw), `"windows": []`) {
		t.Fatal("un modèle sans fenêtre écrit une liste VIDE, pas null")
	}
	fresh := newSterne(r)
	if err := fresh.Warmup(context.Background(), strategy.WarmupRequest{Symbol: "EURUSD", Timeframe: data.M15, ModelDir: dir}); err != nil {
		t.Fatal(err)
	}
	for i := r.atrWindow; i < len(series); i++ {
		if sig, _ := fresh.OnBar(context.Background(), "EURUSD", series, i); sig.Action != core.Hold {
			t.Fatalf("bougie %d : signal %v d'un modèle sans fenêtre", i, sig.Action)
		}
	}
}

func TestRefusesWhatItCannotDo(t *testing.T) {
	r := v10()
	ctx := context.Background()
	s := newSterne(r)
	series := strategytest.SeriesAt(data.M15, 3000, 1, 1.10)
	one := map[string]core.Series{"EURUSD": series}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: one, Timeframe: data.H1, OutputDir: t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "M15") {
		t.Fatalf("H1 : refus nommant M15 attendu, reçu %v", err)
	}
	cross := map[string]core.Series{"EURGBP": series}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: cross, Timeframe: data.M15, OutputDir: t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "dollar") {
		t.Fatalf("EURGBP : refus disant que la paire ne contient pas le dollar attendu, reçu %v", err)
	}
	two := map[string]core.Series{"EURUSD": series, "GBPUSD": strategytest.SeriesAt(data.M15, 3000, 2, 1.27)}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: two, Timeframe: data.M15, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("un calibrage mutualisé doit être refusé")
	}
	short := map[string]core.Series{"EURUSD": series[:r.minTrainBars-1]}
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: short, Timeframe: data.M15, OutputDir: t.TempDir()}); err == nil {
		t.Fatal("trop peu de bougies : refus attendu")
	}

	dir := t.TempDir()
	if _, err := s.Train(ctx, strategy.TrainRequest{Datasets: one, Timeframe: data.M15, OutputDir: dir}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]strategy.WarmupRequest{
		"GBPUSD": {Symbol: "GBPUSD", Timeframe: data.M15, ModelDir: dir},
		"M5":     {Symbol: "EURUSD", Timeframe: data.M5, ModelDir: dir},
	} {
		fresh := newSterne(r)
		if err := fresh.Warmup(ctx, req); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("modèle EURUSD M15 chargé pour %s : refus nommant %s attendu, reçu %v", name, name, err)
		}
		if ready, why := fresh.Ready(); ready || why == "" {
			t.Fatalf("%s : la stratégie doit rester non prête et dire pourquoi", name)
		}
	}
	changed := r
	changed.stopATR = 3
	if _, err := changed.loadModel(dir, "EURUSD", "M15"); err == nil {
		t.Fatal("un modèle calibré sous une autre définition doit être refusé")
	}
	moved := r
	moved.windows = append([]window(nil), r.windows...)
	moved.windows[0].minute = 15
	if _, err := moved.loadModel(dir, "EURUSD", "M15"); err == nil {
		t.Fatal("une grille dont une fenêtre a bougé d'un quart d'heure n'est plus la même définition")
	}
	meta, _ := r.loadModel(dir, "EURUSD", "M15")
	meta.Windows = []string{"new_york_avant"}
	bad := t.TempDir()
	if err := meta.save(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := r.loadModel(bad, "EURUSD", "M15"); err == nil || !strings.Contains(err.Error(), "new_york_avant") {
		t.Fatalf("fenêtre hors grille : refus la nommant attendu, reçu %v", err)
	}
}

func TestDeclaresItsExecutionRules(t *testing.T) {
	d := newSterne(v10()).Describe()
	if d.HoldsOverWeekend || d.ExitOnReversal || d.UsesVolume || d.UsesNews ||
		d.MaxHold != 2*time.Hour || d.ContextBars != v10().atrWindow {
		t.Fatalf("règles d'exécution déclarées inattendues : %+v", d)
	}
	if strategy.CheckTimeframe(d, data.M15) != nil || strategy.CheckTimeframe(d, data.M5) == nil ||
		strategy.CheckTimeframe(d, data.H1) == nil {
		t.Fatal("M15 accepté, M5 et H1 refusés attendus")
	}
}

// TestUSDSide : le sens de la paire pour un achat de dollar.
func TestUSDSide(t *testing.T) {
	for symbol, want := range map[string]int{"USDJPY": 1, "USDCAD": 1, "EURUSD": -1, "AUDUSD": -1, "EURGBP": 0, "XAU": 0, "": 0} {
		if got := usdSide(symbol); got != want {
			t.Errorf("%q : %d, attendu %d", symbol, got, want)
		}
	}
}

// TestAcceptsOnlyDollarPairs : le walk-forward écarte d'après cette
// réponse les paires que Sterne ne peut pas trader.
func TestAcceptsOnlyDollarPairs(t *testing.T) {
	var f strategy.SymbolFilter = newSterne(v10())
	if err := f.AcceptsSymbol("USDJPY"); err != nil {
		t.Fatal(err)
	}
	if err := f.AcceptsSymbol("EURGBP"); err == nil || !strings.Contains(err.Error(), "dollar") {
		t.Fatalf("EURGBP : refus disant que la paire ne contient pas le dollar attendu, reçu %v", err)
	}
}
