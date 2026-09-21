package view

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

func newData(t *testing.T) *Data {
	t.Helper()
	return NewData(newTestDeps(t)).(*Data)
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// TestPeriodStartsOnTheWholeHistory : le comportement par défaut ne change
// pas — c'est la possibilité de le restreindre qui est nouvelle.
func TestPeriodStartsOnTheWholeHistory(t *testing.T) {
	v := newData(t)
	full := data.FullRange(v.deps.App.Config.History.StartYear)
	if !v.span.Covers(full) {
		t.Fatalf("période initiale %s, historique complet attendu (%s)", v.span, full)
	}
}

// TestPeriodCanBeNarrowedToOneYear : « je veux juste 2019 pour essayer ».
func TestPeriodCanBeNarrowedToOneYear(t *testing.T) {
	v := newData(t)
	// La borne de début monte jusqu'à rejoindre la borne de fin.
	for i := 0; i < 100; i++ {
		v.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if v.span.From != v.span.To {
		t.Fatalf("la borne de début doit pouvoir rejoindre la fin : %s", v.span)
	}
	years := v.span.Years()
	if len(years) != 1 {
		t.Fatalf("%d année(s) demandées, 1 attendue", len(years))
	}
}

// TestPeriodEdgeSwitches : « p » passe d'une borne à l'autre, sinon seule
// la première serait réglable.
func TestPeriodEdgeSwitches(t *testing.T) {
	v := newData(t)
	before := v.span
	v.Update(key("p"))
	v.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if v.span.To == before.To {
		t.Fatalf("après « p », ← doit déplacer la borne de FIN : %s", v.span)
	}
	if v.span.From != before.From {
		t.Fatalf("la borne de début ne devait pas bouger : %s", v.span)
	}
}

// TestPeriodResets : « a » revient à l'historique complet.
func TestPeriodResets(t *testing.T) {
	v := newData(t)
	v.span = data.YearRange{From: 2019, To: 2019}
	v.Update(key("a"))
	if !v.span.Covers(data.FullRange(v.deps.App.Config.History.StartYear)) {
		t.Fatalf("« a » doit rendre l'historique complet : %s", v.span)
	}
}

// TestPeriodIsVisible : un réglage invisible est un réglage qu'on oublie
// avoir changé — et l'utilisateur croirait télécharger tout l'historique.
func TestPeriodIsVisible(t *testing.T) {
	v := newData(t)
	v.span = data.YearRange{From: 2019, To: 2020}
	out := v.Render(100, 30)
	if !strings.Contains(out, "2019") || !strings.Contains(out, "2020") {
		t.Fatalf("la période demandée doit s'afficher :\n%s", out)
	}
	if !strings.Contains(out, "partielle") {
		t.Fatalf("une période restreinte doit être signalée comme telle :\n%s", out)
	}
}
