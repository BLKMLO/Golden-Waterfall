package view

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// TestBacktestOffersTradablePairsFirst : sur un compte en dollars avec le
// dimensionnement au risque, 21 paires sur 31 ne produiront aucun trade.
// ↑↓ ne les proposent plus par défaut ; v les rend.
func TestBacktestOffersTradablePairsFirst(t *testing.T) {
	deps := newTestDeps(t)
	cfg := deps.App.Config
	exact, inexact := data.SplitByConversion(cfg.History.Instruments, cfg.Backtest.AccountCurrency)
	if cfg.Risk.RiskPerTradePct <= 0 || len(inexact) == 0 {
		t.Skip("configuration par défaut sans paire non dimensionnable")
	}
	v := NewBacktest(deps).(*Backtest)
	if got := len(v.pairs()); got != len(exact) {
		t.Fatalf("%d paires proposées, %d dimensionnables attendues", got, len(exact))
	}
	for i := 0; i < len(v.pairs()); i++ {
		if !tradable(cfg, v.selected()) {
			t.Fatalf("%s proposée alors qu'elle n'est pas dimensionnable", v.selected())
		}
		v.Update(key("down"))
	}
	v.Update(key("v"))
	if len(v.pairs()) != len(cfg.History.Instruments) {
		t.Fatal("v doit rendre toutes les paires")
	}
}

// TestPickerNoneClearsHiddenPairs : « aucun » décoche aussi ce que le
// masque « tradables » cache — sinon la sélection validée en contiendrait
// vingt de trop, sans qu'aucune ne soit visible.
func TestPickerNoneClearsHiddenPairs(t *testing.T) {
	deps := newTestDeps(t)
	v := NewTraining(deps).(*Training)
	v.Update(key("p"))
	if !v.picker.OnlyTradable {
		t.Skip("masque inactif avec la configuration par défaut")
	}
	v.picker.Update(key("n"))
	if n := len(v.picker.Selected()); n != 0 {
		t.Fatalf("%d paire(s) encore cochée(s) après « aucun »", n)
	}
	v.picker.Update(key("v"))
	if v.picker.OnlyTradable {
		t.Fatal("v doit lever le masque")
	}
}

// TestLiveChecklistSaysWhyAPairIsSilent : la liste de contrôle nomme la
// première condition manquante, et ne suppose rien de ce qui ne se sait
// qu'à la connexion.
func TestLiveChecklistSaysWhyAPairIsSilent(t *testing.T) {
	deps := newTestDeps(t)
	v := NewLive(deps).(*Live)
	v.Init()
	out := v.Render(160, 40)
	for _, want := range []string{"✗ passerelle", "? modèle", "? historique", "→ bloquée : passerelle"} {
		if !strings.Contains(out, want) {
			t.Fatalf("« %s » absent de la liste de contrôle :\n%s", want, out)
		}
	}
	if strings.Contains(out, "Moteur :") {
		t.Fatal("la liste de contrôle remplace la ligne « Moteur : »")
	}
}

// TestLiveConnectionAsksForTheAccountNumber : en mode live sur une
// passerelle réelle, « c » ne connecte pas — il demande le numéro du
// compte, et pendant la saisie aucune touche n'est une commande.
func TestLiveConnectionAsksForTheAccountNumber(t *testing.T) {
	deps := newTestDeps(t)
	deps.App.Config.Broker.Mode = "live"
	v := NewLive(deps).(*Live)
	v.Init()
	v.snapshot.Simulated = false
	v.Update(key("c"))
	if !v.confirming || !v.CapturesKeys() {
		t.Fatal("« c » doit ouvrir la confirmation, qui confisque les touches")
	}
	for _, r := range "DU1q" {
		v.Update(key(string(r)))
	}
	if v.typed != "DU1q" {
		t.Fatalf("saisie %q", v.typed)
	}
	if out := v.Render(100, 30); !strings.Contains(out, "ARGENT RÉEL") || !strings.Contains(out, "DU1q") {
		t.Fatalf("la confirmation doit dire l'enjeu et montrer la saisie :\n%s", out)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.confirming || v.connecting {
		t.Fatal("échap doit renoncer sans connecter")
	}

	// Un rejeu en « live » n'engage aucun argent : pas de confirmation.
	v.snapshot.Simulated = true
	if v.needsConfirmation() {
		t.Fatal("une passerelle simulée ne demande pas de numéro de compte")
	}
}

func TestTradeDetailOpensWithEnter(t *testing.T) {
	deps := newTestDeps(t)
	res := sampleResult(true, true)
	res.Trades = manyTrades(5)
	m, _ := NewBacktest(deps).Update(backtestDoneMsg{result: res, took: time.Second})
	v := m.(*Backtest)
	v.Update(key("enter"))
	out := v.Render(120, 40)
	if !strings.Contains(out, "Motif") || !strings.Contains(out, "n005") || !strings.Contains(out, "Durée") {
		t.Fatalf("entrée doit ouvrir le détail du trade sélectionné :\n%s", out)
	}
	v.Update(key("esc"))
	if strings.Contains(v.Render(120, 40), "Motif") {
		t.Fatal("échap doit revenir à la liste")
	}

	j := NewJournal(deps).(*Journal)
	j.trades, j.tradesFresh, j.tab = manyTrades(3), true, 1
	j.Update(key("enter"))
	if out := j.Render(120, 30); !strings.Contains(out, "Trade #1") {
		t.Fatalf("détail du journal :\n%s", out)
	}
	j.Update(key("esc"))
	if !strings.Contains(j.Render(120, 30), "Trades exécutés") {
		t.Fatal("échap doit revenir à la liste du journal")
	}
}
