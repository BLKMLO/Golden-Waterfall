package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestJournalFilterCapturesKeys : pendant une saisie, l'écran doit garder
// les touches que le routeur intercepte. Sans cela, taper « 3 » dans le
// filtre changerait d'onglet et « q » quitterait le programme.
func TestJournalFilterCapturesKeys(t *testing.T) {
	v := NewJournal(newTestDeps(t)).(*Journal)
	if v.CapturesKeys() {
		t.Fatal("au repos, l'écran ne doit pas confisquer les touches")
	}
	v.Update(runes("/"))
	if !v.CapturesKeys() {
		t.Fatal("pendant la saisie, l'écran doit confisquer les touches")
	}
	for _, r := range "eur3q" {
		v.Update(runes(string(r)))
	}
	if v.buffer != "eur3q" {
		t.Fatalf("saisie perdue : %q", v.buffer)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.CapturesKeys() || v.filter != "eur3q" {
		t.Fatalf("entrée doit valider le filtre : capture=%v filtre=%q", v.CapturesKeys(), v.filter)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.filter != "" {
		t.Fatalf("échap doit effacer le filtre, reste %q", v.filter)
	}
}

// TestJournalFilterSaysWhyTheListIsEmpty : un filtre oublié ressemble
// trait pour trait à un programme silencieux. L'écran doit lever le doute.
func TestJournalFilterSaysWhyTheListIsEmpty(t *testing.T) {
	deps := newTestDeps(t)
	deps.App.Logging.Logger.Info("connexion de la passerelle replay")
	v := NewJournal(deps).(*Journal)

	v.filter = "introuvable-xyz"
	out := v.Render(120, 20)
	if !strings.Contains(out, "introuvable-xyz") {
		t.Fatalf("le filtre actif doit être rappelé à l'écran :\n%s", out)
	}
	if !strings.Contains(out, "échap") {
		t.Fatalf("l'écran doit dire comment effacer le filtre :\n%s", out)
	}

	v.filter = "passerelle"
	if out := v.Render(120, 20); !strings.Contains(out, "passerelle") {
		t.Fatalf("la ligne correspondante doit survivre au filtre :\n%s", out)
	}
}

// TestJournalExportWritesWhatIsShown : un fichier dont le contenu ne
// correspond pas à l'écran qui l'a produit est un piège. L'export suit
// donc le filtre.
func TestJournalExportWritesWhatIsShown(t *testing.T) {
	deps := newTestDeps(t)
	v := NewJournal(deps).(*Journal)
	v.trades = sampleViewTrades()
	v.tradesFresh = true
	v.tab = 1

	var status []string
	deps.Status = func(s string) { status = append(status, s) }
	v.deps = deps

	v.filter = "GBPUSD"
	v.Update(runes("e"))

	dir := deps.App.Config.Paths.ExportsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d fichiers écrits, attendu 1 : %v", len(entries), entries)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "EURUSD") {
		t.Fatal("l'export contient une ligne que le filtre cachait à l'écran")
	}
	if !strings.Contains(string(raw), "GBPUSD") {
		t.Fatal("l'export ne contient pas la ligne affichée")
	}
	if len(status) == 0 || !strings.Contains(status[len(status)-1], dir) {
		t.Fatalf("l'écran doit dire OÙ le fichier a été écrit : %v", status)
	}
}

// TestJournalExportSaysWhenThereIsNothing : mieux vaut un refus explicite
// qu'un fichier vide qu'on prendra pour une absence de trades.
func TestJournalExportSaysWhenThereIsNothing(t *testing.T) {
	deps := newTestDeps(t)
	var status []string
	deps.Status = func(s string) { status = append(status, s) }
	v := NewJournal(deps).(*Journal)
	v.tradesFresh = true

	v.Update(runes("e"))
	if len(status) == 0 || !strings.Contains(status[0], "aucun trade") {
		t.Fatalf("un export vide doit être refusé et dit : %v", status)
	}
	if entries, _ := os.ReadDir(deps.App.Config.Paths.ExportsDir()); len(entries) != 0 {
		t.Fatalf("aucun fichier ne devait être écrit : %v", entries)
	}
}
