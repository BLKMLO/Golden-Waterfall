package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
)

func newTestApp(t *testing.T) *app.App {
	t.Helper()
	root := t.TempDir()
	a, err := app.New(config.Paths{
		ConfigDir: filepath.Join(root, "cfg"),
		DataDir:   filepath.Join(root, "data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// TestRenderAtManySizes : un terminal se redimensionne sans prévenir. Un
// panic ou un rendu négatif y est la panne la plus banale — et la plus
// évitable.
func TestRenderAtManySizes(t *testing.T) {
	m := New(newTestApp(t))
	sizes := [][2]int{
		{40, 10}, {60, 18}, {80, 24}, {100, 30}, {120, 40}, {200, 60}, {300, 100},
	}
	for _, s := range sizes {
		model, _ := m.Update(tea.WindowSizeMsg{Width: s[0], Height: s[1]})
		out := model.View()
		if out == "" {
			t.Fatalf("rendu vide en %d×%d", s[0], s[1])
		}
		m = model.(*Model)
	}
}

func TestSmallTerminalIsExplained(t *testing.T) {
	m := New(newTestApp(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	out := model.View()
	if !strings.Contains(out, "trop petit") {
		t.Fatalf("un terminal minuscule doit être expliqué, pas rendu de travers : %q", out)
	}
}

func TestAllTabsRender(t *testing.T) {
	m := New(newTestApp(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	m = model.(*Model)
	for i := range m.views {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + i)}})
		m = updated.(*Model)
		if m.active != i {
			t.Fatalf("la touche %d doit activer l'écran %d, actif : %d", i+1, i, m.active)
		}
		if out := m.View(); out == "" {
			t.Fatalf("écran %q : rendu vide", m.views[i].Title())
		}
	}
}

func TestTabCyclesBothWays(t *testing.T) {
	m := New(newTestApp(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = model.(*Model)
	n := len(m.views)
	for i := 0; i < n+2; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(*Model)
	}
	if m.active != (n+2)%n {
		t.Fatalf("cycle avant incorrect : %d", m.active)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(*Model)
	if m.active != (n+1)%n {
		t.Fatalf("cycle arrière incorrect : %d", m.active)
	}
}

func TestHelpToggles(t *testing.T) {
	m := New(newTestApp(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = model.(*Model)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(*Model)
	if !m.showHelp {
		t.Fatal("« ? » doit afficher l'aide")
	}
	if !strings.Contains(m.View(), "Navigation") {
		t.Fatal("l'aide doit lister la navigation")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if updated.(*Model).showHelp {
		t.Fatal("« ? » doit aussi masquer l'aide")
	}
}

// TestHeaderAnnouncesSimulation : le bandeau REJEU est la garantie
// visuelle qu'un compte fictif ne sera pas pris pour un compte réel.
func TestHeaderAnnouncesSimulation(t *testing.T) {
	a := newTestApp(t)
	m := New(a)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	out := model.View()
	if !strings.Contains(out, "REJEU") {
		t.Fatalf("la passerelle par défaut est simulée : l'entête doit le dire.\n%s", out)
	}
}

func TestQuitBlockedWhileBusy(t *testing.T) {
	m := New(newTestApp(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = model.(*Model)
	// Sans travail en cours, « q » demande la sortie.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil || !updated.(*Model).quitting {
		t.Fatal("« q » doit quitter quand rien ne tourne")
	}
}

// TestBadgesNeverDisappear : l'entête supprimait TOUS les badges dès que
// la largeur manquait — dont « LIVE — ARGENT RÉEL » et l'état du
// kill-switch. Ce sont précisément les informations qu'on ne peut pas se
// permettre de perdre en réduisant une fenêtre.
func TestBadgesNeverDisappear(t *testing.T) {
	m := New(newTestApp(t))
	for _, width := range []int{60, 70, 80, 100, 140, 200} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		header := m.renderHeader()
		if !strings.Contains(header, "REJEU") {
			t.Errorf("largeur %d : le bandeau de mode a disparu de l'entête\n%s", width, header)
		}
		if !strings.Contains(header, "k-s") && !strings.Contains(header, "kill-switch") {
			t.Errorf("largeur %d : l'état du kill-switch a disparu de l'entête", width)
		}
		for _, line := range strings.Split(header, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("largeur %d : l'entête en occupe %d", width, w)
			}
		}
	}
}

// TestFooterAlwaysOffersTheWayOut : les touches globales étaient en fin de
// liste, donc les premières rognées. Un utilisateur perdait « q quitter »
// avant tout le reste.
func TestFooterAlwaysOffersTheWayOut(t *testing.T) {
	m := New(newTestApp(t))
	for _, width := range []int{60, 80, 120} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		footer := m.renderFooter()
		for _, key := range []string{"aide", "quitter"} {
			if !strings.Contains(footer, key) {
				t.Errorf("largeur %d : « %s » absent de la barre de raccourcis\n%s", width, key, footer)
			}
		}
	}
}
