package view

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/live"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
)

// bodySizes : hauteurs de corps que laisse le routeur (entête et pied
// déduits) de 60×18 à 120×40.
var bodySizes = [][2]int{{60, 13}, {70, 15}, {80, 19}, {100, 25}, {120, 35}}

// TestLoadedScreensFit : les écrans PLEINS — un backtest de 120 trades,
// un journal de 200, des positions ouvertes — tiennent eux aussi sans
// coupure. Les écrans vides ne sont pas ceux qu'on regarde.
func TestLoadedScreensFit(t *testing.T) {
	deps := newTestDeps(t)
	res := sampleResult(false, false)
	res.Trades = manyTrades(120)
	bt, _ := NewBacktest(deps).Update(backtestDoneMsg{result: res, took: time.Second})

	j := NewJournal(deps).(*Journal)
	j.trades, j.tradesFresh, j.tab, j.tradesTotal = manyTrades(200), true, 1, 900

	lv := NewLive(deps).(*Live)
	lv.snapshot = deps.App.Live.Snapshot()
	lv.snapshot.Positions = []core.Position{
		{Symbol: "EURUSD", Quantity: 20000, AveragePrice: 1.08},
		{Symbol: "USDJPY", Quantity: -10000, AveragePrice: 150.1},
	}
	lv.snapshot.Symbols = append(lv.snapshot.Symbols, live.SymbolState{})

	wf := &training.Result{Strategy: "colibri_v1_2", Symbols: []string{"EURUSD", "GBPUSD"},
		Aggregate: sampleResult(false, false).Stats, Equity: sampleResult(true, true).Equity}
	for i := 0; i < 5; i++ {
		wf.Folds = append(wf.Folds, training.Fold{Index: i, Stats: backtest.Stats{Trades: 10}, OOSAUC: 0.52, HasOOSAUC: true})
	}
	tr, _ := NewTraining(deps).Update(trainingDoneMsg{result: wf, took: time.Minute})

	screens := map[string]Model{"Backtest avec résultat": bt, "Journal trades": j,
		"Live avec positions": lv, "Entraînement avec résultat": tr}
	for name, v := range screens {
		for _, s := range bodySizes {
			if h := lipgloss.Height(v.Render(s[0], s[1])); h > s[1] {
				t.Errorf("%s en %d colonnes : %d lignes pour %d accordées", name, s[0], h, s[1])
			}
		}
	}
}
