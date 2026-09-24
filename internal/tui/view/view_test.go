package view

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// newTestDeps monte une application complète dans un dossier jetable :
// les écrans lisent la configuration, la base et le runtime live, on ne
// peut donc pas les tester à vide.
func newTestDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Paths{
		ConfigDir: filepath.Join(dir, "config"),
		DataDir:   filepath.Join(dir, "data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return Deps{
		App:    a,
		Theme:  theme.ByName(a.Config.UI.Theme),
		Emit:   func(tea.Msg) {},
		Status: func(string) {},
	}
}

func allViews(deps Deps) map[string]Model {
	return map[string]Model{
		"live":         NewLive(deps),
		"données":      NewData(deps),
		"backtest":     NewBacktest(deps),
		"entraînement": NewTraining(deps),
		"journal":      NewJournal(deps),
		"paramètres":   NewSettings(deps),
	}
}

// TestEveryViewRendersWithinItsWidth : un écran qui déborde casse la mise
// en page de TOUS les autres (Bubbletea compose des chaînes, il ne
// découpe pas). Les tailles couvrent un terminal étroit, la taille
// standard et un grand écran.
func TestEveryViewRendersWithinItsWidth(t *testing.T) {
	deps := newTestDeps(t)
	sizes := [][2]int{{60, 16}, {80, 24}, {120, 40}, {200, 60}}
	for name, v := range allViews(deps) {
		for _, size := range sizes {
			width, height := size[0], size[1]
			out := v.Render(width, height)
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("écran %s en %dx%d : la ligne %d fait %d colonnes",
						name, width, height, i+1, w)
					break
				}
			}
		}
	}
}

// TestEveryViewDeclaresATitleAndKeys : la barre d'aide et les onglets s'en
// nourrissent. Un écran muet est un écran qu'on ne sait pas piloter.
func TestEveryViewDeclaresATitleAndKeys(t *testing.T) {
	deps := newTestDeps(t)
	for name, v := range allViews(deps) {
		if strings.TrimSpace(v.Title()) == "" {
			t.Errorf("écran %s sans titre", name)
		}
		for _, k := range v.Keys() {
			if strings.TrimSpace(k[0]) == "" || strings.TrimSpace(k[1]) == "" {
				t.Errorf("écran %s : raccourci incomplet %v", name, k)
			}
		}
	}
}

func sampleResult(costsModelled, currencyExact bool) *backtest.Result {
	return &backtest.Result{
		Stats: backtest.Stats{
			Symbol: "EURUSD", Bars: 100, Trades: 3, Wins: 2, Losses: 1,
			WinRate: 66.7, NetPnL: 120, ProfitFactor: 2.1,
			InitialCapital: 10000, FinalEquity: 10120,
			CostsModelled: costsModelled, Currency: "USD", CurrencyExact: currencyExact,
			Start: time.Now().Add(-24 * time.Hour), End: time.Now(),
		},
		Equity: []backtest.EquityPoint{
			{Time: time.Now().Add(-24 * time.Hour), Value: 10000},
			{Time: time.Now(), Value: 10120},
		},
	}
}

// renderResult joue un résultat dans l'écran de backtest et rend l'écran.
func renderResult(t *testing.T, deps Deps, res *backtest.Result) string {
	t.Helper()
	v, _ := NewBacktest(deps).Update(backtestDoneMsg{result: res, took: time.Second})
	return v.Render(120, 40)
}

// TestBacktestAlwaysWarnsInSample : l'avertissement est PERMANENT, pas
// conditionnel — le modèle de production a été entraîné sur la période
// rejouée. Sans ce rappel, un taux de gain flatteur se prend pour une
// performance.
func TestBacktestAlwaysWarnsInSample(t *testing.T) {
	deps := newTestDeps(t)
	out := renderResult(t, deps, sampleResult(true, true))
	if !strings.Contains(out, "IN-SAMPLE") {
		t.Fatal("l'avertissement IN-SAMPLE a disparu de l'écran de backtest")
	}
}

// TestBacktestSaysWhenNoCostsAreModelled : afficher « coûts : 0 » sans
// rien dire ferait passer une lacune de données pour de la gratuité.
func TestBacktestSaysWhenNoCostsAreModelled(t *testing.T) {
	deps := newTestDeps(t)
	out := renderResult(t, deps, sampleResult(false, true))
	if !strings.Contains(out, "AUCUN") {
		t.Fatal("un backtest sans coût modélisé doit le DIRE")
	}
}

// TestBacktestSaysWhenCurrencyIsNotConverted : additionner des devises non
// convertibles est interdit ; le dire l'est d'autant plus.
func TestBacktestSaysWhenCurrencyIsNotConverted(t *testing.T) {
	deps := newTestDeps(t)
	out := renderResult(t, deps, sampleResult(true, false))
	if !strings.Contains(out, "NON convertis") {
		t.Fatal("une paire non convertible doit être signalée à l'écran")
	}
}

// TestLiveShowsDashesWithoutAccount : sans courtier connecté, le compte
// est INCONNU. Un zéro se lirait comme un solde.
func TestLiveShowsDashesWithoutAccount(t *testing.T) {
	deps := newTestDeps(t)
	out := NewLive(deps).Render(120, 40)
	if !strings.Contains(out, "—") {
		t.Fatal("sans compte, l'écran live doit afficher des tirets et non des zéros")
	}
	if strings.Contains(out, "0.00") {
		t.Fatal("un solde à zéro est affiché alors qu'aucun compte n'est connu")
	}
}

// TestBacktestSizeCardTellsWhichRegimeIsActive : « Taille 10000 » devient
// un mensonge dès que le dimensionnement au risque est actif — la taille
// varie alors d'un trade à l'autre et ce nombre n'est plus qu'un plafond.
func TestBacktestSizeCardTellsWhichRegimeIsActive(t *testing.T) {
	fixed := sizeCard(config.RiskConfig{MaxPositionSize: 100000, FixedPositionSize: 10000})
	if fixed.Label != "Taille" || fixed.Value != "10000.00" {
		t.Fatalf("à risque désactivé, la carte annonce fixed_position_size : %+v", fixed)
	}
	if !strings.Contains(fixed.Note, "fixe") || !strings.Contains(fixed.Note, "plafond") {
		t.Fatalf("la carte doit distinguer la taille du plafond : %+v", fixed)
	}
	sized := sizeCard(config.RiskConfig{
		MaxPositionSize: 100000, FixedPositionSize: 10000, RiskPerTradePct: 1})
	if sized.Label == "Taille" {
		t.Fatalf("dimensionnement au risque actif : la carte ne doit plus annoncer UNE taille (%+v)", sized)
	}
	if !strings.Contains(sized.Note, "plafond") {
		t.Fatalf("max_position_size devient un plafond, et cela doit se lire : %+v", sized)
	}
}

// sampleViewTrades : deux trades sur deux paires, de quoi vérifier qu'un
// filtre sépare vraiment.
func sampleViewTrades() []core.Trade {
	entry := time.Date(2024, 3, 4, 8, 0, 0, 0, time.UTC)
	return []core.Trade{
		{ID: 1, Symbol: "EURUSD", Side: core.Buy, Quantity: 10000, EntryTime: entry,
			EntryPrice: 1.08, ExitTime: entry.Add(time.Hour), ExitPrice: 1.081,
			PnL: 10, ExitReason: "tp"},
		{ID: 2, Symbol: "GBPUSD", Side: core.Sell, Quantity: 10000, EntryTime: entry,
			EntryPrice: 1.27, ExitTime: entry.Add(time.Hour), ExitPrice: 1.269,
			PnL: 10, ExitReason: "sl"},
	}
}

// key fabrique une touche depuis son nom. Les écrans de ce paquet
// réagissent autant aux touches spéciales (entrée, échap, espace) qu'aux
// caractères : un helper qui ne saurait produire que des runes obligerait
// chaque test à réécrire le même switch.
func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
