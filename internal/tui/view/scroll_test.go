package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// manyTrades : assez de trades pour qu'aucun écran ne puisse tous les
// montrer à la fois. Le prix d'entrée porte le numéro du trade, pour
// savoir lequel est affiché.
func manyTrades(n int) []core.Trade {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]core.Trade, n)
	for i := range out {
		entry := start.Add(time.Duration(i) * 4 * time.Hour)
		out[i] = core.Trade{
			ID: int64(i + 1), Symbol: "EURUSD", Side: core.Buy, Quantity: 10000,
			EntryTime: entry, EntryPrice: 1 + float64(i)/10000,
			ExitTime: entry.Add(time.Hour), ExitPrice: 1.5, PnL: 1, ExitReason: fmt.Sprintf("n%03d", i+1),
		}
	}
	return out
}

// TestBacktestTradesScroll : la liste des trades d'un backtest défile. Elle
// ne montrait que ses premières lignes, sans rien dire du reste.
func TestBacktestTradesScroll(t *testing.T) {
	deps := newTestDeps(t)
	res := sampleResult(true, true)
	res.Trades = manyTrades(120)
	m, _ := NewBacktest(deps).Update(backtestDoneMsg{result: res, took: time.Second})
	v := m.(*Backtest)

	out := v.Render(120, 40)
	// Plus récent d'abord : le trade 120 est sélectionné, sa ligne de
	// détail donne son prix d'entrée.
	if !strings.Contains(out, "1/120") || !strings.Contains(out, "1.01190") {
		t.Fatalf("position ou détail absents :\n%s", out)
	}
	if strings.Contains(out, "n001") {
		t.Fatal("le plus ancien trade ne doit pas tenir dans l'écran — sinon ce test ne prouve rien")
	}

	v.Update(key("end"))
	out = v.Render(120, 40)
	if !strings.Contains(out, "n001") || !strings.Contains(out, "120/120") {
		t.Fatalf("fin doit atteindre le plus ancien trade :\n%s", out)
	}

	// ↑↓ choisissent la paire tant que le focus n'est pas sur les trades.
	v.Update(key("home"))
	pair := v.cursor
	v.Update(key("down"))
	if v.cursor == pair || v.trades.Cursor != 0 {
		t.Fatalf("sans focus, ↓ change de paire (paire %d → %d, trade %d)", pair, v.cursor, v.trades.Cursor)
	}
	v.Update(key("t"))
	v.Update(key("down"))
	v.Update(key("down"))
	if v.trades.Cursor != 2 {
		t.Fatalf("avec le focus, ↓ parcourt les trades : curseur %d", v.trades.Cursor)
	}
	v.Update(key("pgdown"))
	if v.trades.Cursor <= 2 {
		t.Fatal("pgdn doit avancer d'une page")
	}
	for _, h := range []int{13, 15, 19, 24, 40, 60} {
		if got := lipgloss.Height(v.Render(100, h)); got > h {
			t.Fatalf("écran Backtest de %d lignes pour %d accordées", got, h)
		}
	}
	v.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if got := v.Render(120, 40); !strings.Contains(got, "▸ Trades") {
		t.Fatalf("le focus doit se voir dans le titre :\n%s", got)
	}
}

func TestJournalTradesScroll(t *testing.T) {
	deps := newTestDeps(t)
	v := NewJournal(deps).(*Journal)
	v.trades, v.tradesFresh, v.tab = manyTrades(200), true, 1
	v.tradesTotal = 900

	out := v.Render(120, 30)
	if !strings.Contains(out, "1/200") || !strings.Contains(out, "200 plus récents sur 900") {
		t.Fatalf("position et total absents :\n%s", out)
	}
	for _, w := range []int{80, 120} {
		for _, h := range []int{18, 24, 30, 60} {
			if got := lipgloss.Height(v.Render(w, h)); got > h {
				t.Fatalf("onglet trades de %d lignes pour %d accordées (largeur %d)", got, h, w)
			}
		}
	}
	v.Update(key("end"))
	if out := v.Render(120, 30); !strings.Contains(out, "n200") || !strings.Contains(out, "200/200") {
		t.Fatalf("fin doit atteindre le dernier trade :\n%s", out)
	}
	v.Update(key("pgup"))
	v.Update(key("up"))
	if v.tradeScroll.Cursor >= 199 || v.tradeScroll.Cursor < 150 {
		t.Fatalf("curseur %d après pgup + ↑", v.tradeScroll.Cursor)
	}
	// Un filtre ramène au début de la liste filtrée.
	v.Update(key("/"))
	for _, r := range "n19" {
		v.Update(key(string(r)))
	}
	v.Update(key("enter"))
	if out := v.Render(120, 30); !strings.Contains(out, "1/10") {
		t.Fatalf("le filtre doit repartir du premier résultat :\n%s", out)
	}
}
