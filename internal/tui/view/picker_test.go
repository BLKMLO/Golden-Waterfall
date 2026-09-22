package view

import (
	"strings"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

func typeInto(p *SymbolPicker, text string) {
	for _, r := range text {
		p.Update(key(string(r)))
	}
}

// TestPickerFilterThenSelectAll : le geste qui justifie le sélecteur —
// taper « JPY », tout cocher, valider. Dans l'ancien champ de texte, il
// fallait écrire huit noms de paires sans se tromper.
func TestPickerFilterThenSelectAll(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", nil)
	p.Update(key("/"))
	typeInto(p, "JPY")
	p.Update(key("enter"))
	p.Update(key("a"))

	got := p.Selected()
	if len(got) == 0 {
		t.Fatal("« a » doit cocher tout ce que le filtre affiche")
	}
	for _, s := range got {
		if !strings.Contains(s, "JPY") {
			t.Fatalf("%s ne correspond pas au filtre : « tout » s'entend AU SENS DU FILTRE", s)
		}
	}

	// Le filtre levé, la sélection reste — et rien d'autre n'a été coché.
	p.Update(key("esc"))
	if len(p.Selected()) != len(got) {
		t.Fatalf("la sélection a changé en levant le filtre : %d puis %d",
			len(got), len(p.Selected()))
	}

	done, accepted := p.Update(key("enter"))
	if !done || !accepted {
		t.Fatal("entrée doit valider")
	}
}

// TestPickerRefusesAnEmptySelection : valider zéro paire produirait un
// entraînement sans données et une erreur trois écrans plus loin.
func TestPickerRefusesAnEmptySelection(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", []string{"EURUSD"})
	p.Update(key("n")) // tout décocher
	if done, accepted := p.Update(key("enter")); done || accepted {
		t.Fatal("une sélection vide ne doit pas être validable")
	}
	if !strings.Contains(p.Render(100, 20), "au moins une paire") {
		t.Fatal("le refus doit être écrit à l'écran, pas seulement appliqué")
	}
}

// TestPickerEscapeAbandons : échap sans filtre abandonne, et l'écran
// appelant doit pouvoir distinguer abandon et validation.
func TestPickerEscapeAbandons(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", []string{"EURUSD"})
	done, accepted := p.Update(key("esc"))
	if !done || accepted {
		t.Fatalf("échap doit terminer SANS valider (done=%v accepted=%v)", done, accepted)
	}
}

// TestPickerEscapeClearsTheFilterFirst : la première échappée lève le
// filtre, la seconde ferme. Fermer d'un coup ferait perdre la sélection
// en cours à qui voulait juste élargir sa recherche.
func TestPickerEscapeClearsTheFilterFirst(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", nil)
	p.Update(key("/"))
	typeInto(p, "EUR")
	p.Update(key("enter"))

	if done, _ := p.Update(key("esc")); done {
		t.Fatal("la première échappée doit lever le filtre, pas fermer")
	}
	if done, _ := p.Update(key("esc")); !done {
		t.Fatal("la seconde échappée doit fermer")
	}
}

// TestPickerInvertsWithinTheFilter : « i » inverse ce qui est affiché,
// jamais le reste — sans quoi filtrer deviendrait dangereux.
func TestPickerInvertsWithinTheFilter(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", []string{"EURUSD", "XAUUSD"})
	p.Update(key("/"))
	typeInto(p, "EURUSD")
	p.Update(key("enter"))
	p.Update(key("i")) // EURUSD décochée

	got := p.Selected()
	if len(got) != 1 || got[0] != "XAUUSD" {
		t.Fatalf("l'inversion a débordé du filtre : %v", got)
	}
}

// TestPickerAnnotatesWhatItKnows : une paire qu'on ne peut pas trader
// doit se voir AU MOMENT DU CHOIX, pas dans un run vide deux heures plus
// tard.
func TestPickerAnnotatesWhatItKnows(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", nil)
	p.Note = func(symbol string) string {
		if symbol == "EURGBP" {
			return "non dimensionnable"
		}
		return ""
	}
	p.Update(key("/"))
	typeInto(p, "EURGBP")
	p.Update(key("enter"))
	if !strings.Contains(p.Render(120, 20), "non dimensionnable") {
		t.Fatalf("l'annotation doit apparaître :\n%s", p.Render(120, 20))
	}
}

// TestPickerIsModal : tant qu'il est ouvert, il prend toutes les touches.
func TestPickerIsModal(t *testing.T) {
	p := NewSymbolPicker(theme.Dark(), "Paires", nil)
	if !p.CapturesKeys() {
		t.Fatal("un sélecteur ouvert confisque les touches")
	}
	// Un chiffre tapé pendant une recherche ne doit pas changer d'onglet :
	// il entre dans le filtre.
	p.Update(key("/"))
	typeInto(p, "US5")
	if p.buffer != "US5" {
		t.Fatalf("saisie perdue : %q", p.buffer)
	}
}

// TestTrainingSelectsItsOwnPairs : un walk-forward sur trente et une
// paires dure des heures. Pouvoir n'en reprendre qu'une, après avoir
// changé un réglage, est la différence entre essayer et renoncer.
func TestTrainingSelectsItsOwnPairs(t *testing.T) {
	deps := newTestDeps(t)
	v := NewTraining(deps).(*Training)

	if len(v.symbols) != len(deps.App.Config.History.Instruments) {
		t.Fatalf("par défaut, toutes les paires de la configuration : %d", len(v.symbols))
	}
	if v.CapturesKeys() {
		t.Fatal("aucun sélecteur ouvert : les touches restent au routeur")
	}

	v.Update(key("p"))
	if !v.CapturesKeys() {
		t.Fatal("« p » doit ouvrir le sélecteur, qui est modal")
	}
	v.picker.Update(key("n")) // tout décocher
	v.picker.Update(key("/"))
	for _, r := range "EURUSD" {
		v.picker.Update(key(string(r)))
	}
	v.picker.Update(key("enter")) // valider le filtre
	v.picker.Update(key("a"))     // cocher ce qu'il affiche
	v.Update(key("enter"))        // valider la sélection

	if v.picker != nil {
		t.Fatal("le sélecteur doit se refermer après validation")
	}
	if len(v.symbols) != 1 || v.symbols[0] != "EURUSD" {
		t.Fatalf("paires retenues : %v", v.symbols)
	}
	// Et l'écran doit le DIRE, sinon on lance un run en croyant tout
	// entraîner.
	if out := v.Render(120, 30); !strings.Contains(out, "EURUSD") {
		t.Fatalf("les paires retenues doivent être visibles :\n%s", out)
	}
}

// TestTrainingRefusesToRunWithoutPairs : mieux vaut un refus immédiat
// qu'une erreur trois écrans plus loin.
func TestTrainingRefusesToRunWithoutPairs(t *testing.T) {
	deps := newTestDeps(t)
	var status []string
	deps.Status = func(s string) { status = append(status, s) }
	v := NewTraining(deps).(*Training)
	v.symbols = nil

	if cmd := v.run(); cmd != nil {
		t.Fatal("aucune paire : aucun travail ne doit démarrer")
	}
	if v.Busy() {
		t.Fatal("l'écran ne doit pas rester marqué occupé")
	}
	if len(status) == 0 || !strings.Contains(status[len(status)-1], "aucune paire") {
		t.Fatalf("le refus doit être expliqué : %v", status)
	}
}

// TestSettingsEditsPairsWithThePicker : le réglage « paires suivies » ne
// s'édite plus dans un champ de texte de deux cents caractères.
func TestSettingsEditsPairsWithThePicker(t *testing.T) {
	v := NewSettings(newTestDeps(t)).(*Settings)
	idx := -1
	for i, f := range v.fields {
		if f.Path == "history.instruments" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("réglage history.instruments introuvable")
	}
	if v.fields[idx].Kind != kindSymbols {
		t.Fatal("les paires suivies doivent être un champ de type sélecteur")
	}

	v.cursor = idx
	v.Update(key("enter"))
	if v.picker == nil || !v.CapturesKeys() {
		t.Fatal("entrée doit ouvrir le sélecteur, qui est modal")
	}
	v.picker.Update(key("n"))
	v.picker.Update(key("/"))
	for _, r := range "XAUUSD" {
		v.picker.Update(key(string(r)))
	}
	v.picker.Update(key("enter"))
	v.picker.Update(key("a"))
	v.Update(key("enter"))

	if v.picker != nil {
		t.Fatal("le sélecteur doit se refermer")
	}
	if got := v.draft.History.Instruments; len(got) != 1 || got[0] != "XAUUSD" {
		t.Fatalf("le brouillon doit porter la sélection : %v", got)
	}
	if !v.dirty {
		t.Fatal("une sélection modifiée est une modification en attente")
	}
}
