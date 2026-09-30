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

func newTestApp(t *testing.T) *app.App { return newTestAppMode(t, app.Trading) }

func newTestAppMode(t *testing.T, mode app.Mode) *app.App {
	t.Helper()
	root := t.TempDir()
	a, err := app.Open(config.Paths{
		ConfigDir: filepath.Join(root, "cfg"),
		DataDir:   filepath.Join(root, "data"),
	}, mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// forEachMode fait tourner un test sur les DEUX interfaces : chaque
// promesse de mise en page vaut pour gw comme pour gw backtrain.
func forEachMode(t *testing.T, f func(t *testing.T, m *Model)) {
	t.Run("trading", func(t *testing.T) { f(t, New(newTestApp(t), Trading)) })
	t.Run("backtrain", func(t *testing.T) { f(t, New(newTestAppMode(t, app.Workshop), Backtrain)) })
}

// TestRenderAtManySizes : un terminal se redimensionne sans prévenir. Un
// panic ou un rendu négatif y est la panne la plus banale — et la plus
// évitable.
func TestRenderAtManySizes(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
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
	})
}

func TestSmallTerminalIsExplained(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		model, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
		out := model.View()
		if !strings.Contains(out, "trop petit") {
			t.Fatalf("un terminal minuscule doit être expliqué, pas rendu de travers : %q", out)
		}
	})
}

func TestAllTabsRender(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
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
	})
}

func TestTabCyclesBothWays(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
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
	})
}

func TestHelpToggles(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
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
	})
}

// TestHeaderAnnouncesSimulation : le bandeau REJEU est la garantie
// visuelle qu'un compte fictif ne sera pas pris pour un compte réel.
func TestHeaderAnnouncesSimulation(t *testing.T) {
	a := newTestApp(t)
	m := New(a, Trading)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	out := model.View()
	if !strings.Contains(out, "REJEU") {
		t.Fatalf("la passerelle par défaut est simulée : l'entête doit le dire.\n%s", out)
	}
}

func TestQuitBlockedWhileBusy(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m = model.(*Model)
		// Sans travail en cours, « q » demande la sortie.
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		if cmd == nil || !updated.(*Model).quitting {
			t.Fatal("« q » doit quitter quand rien ne tourne")
		}
	})
}

// TestBadgesNeverDisappear : l'entête supprimait TOUS les badges dès que
// la largeur manquait — dont « LIVE — ARGENT RÉEL » et l'état du
// kill-switch. Ce sont précisément les informations qu'on ne peut pas se
// permettre de perdre en réduisant une fenêtre.
func TestBadgesNeverDisappear(t *testing.T) {
	m := New(newTestApp(t), Trading)
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
	forEachMode(t, func(t *testing.T, m *Model) {
		for _, width := range []int{60, 80, 120} {
			m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			footer := m.renderFooter()
			for _, key := range []string{"aide", "quitter"} {
				if !strings.Contains(footer, key) {
					t.Errorf("largeur %d : « %s » absent de la barre de raccourcis\n%s", width, key, footer)
				}
			}
		}
	})
}

// screenSizes : de la plus petite taille acceptée aux grandes fenêtres.
var screenSizes = [][2]int{
	{60, 18}, {70, 20}, {80, 24}, {100, 30}, {120, 40}, {160, 44}, {200, 60},
}

// TestScreensNeverExceedTheTerminal est le pendant VERTICAL du contrôle de
// largeur.
//
// Un corps d'écran plus haut que la fenêtre ne perd pas ses dernières
// lignes : il fait défiler l'entête et la barre de raccourcis hors de
// l'écran. L'utilisateur perd alors le bandeau de mode (« LIVE — ARGENT
// RÉEL ») et « q quitter » — sans qu'aucun signe ne l'avertisse. Cinq
// écrans sur six débordaient ainsi en 80×24, la taille de terminal la
// plus banale qui soit.
func TestScreensNeverExceedTheTerminal(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		for _, size := range screenSizes {
			width, height := size[0], size[1]
			model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
			m = model.(*Model)
			for i := range m.views {
				m.active = i
				out := m.View()
				if h := lipgloss.Height(out); h > height {
					t.Errorf("écran %s en %d×%d : %d lignes rendues",
						m.views[i].Title(), width, height, h)
				}
				for n, line := range strings.Split(out, "\n") {
					if w := lipgloss.Width(line); w > width {
						t.Errorf("écran %s en %d×%d : ligne %d large de %d",
							m.views[i].Title(), width, height, n, w)
					}
				}
			}
		}
	})
}

// TestHelpFitsAndScrolls : l'aide occupait cinquante-neuf lignes quelle
// que soit la fenêtre. Sur vingt-quatre lignes, les trois quarts — dont la
// ligne qui explique comment la refermer — partaient hors de l'écran.
func TestHelpFitsAndScrolls(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
		m = model.(*Model)

		for _, size := range screenSizes {
			width, height := size[0], size[1]
			model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
			m = model.(*Model)
			if h := lipgloss.Height(m.View()); h > height {
				t.Errorf("aide en %d×%d : %d lignes rendues", width, height, h)
			}
		}

		// Le défilement atteint bien la fin : la dernière section de l'aide
		// (les chemins) doit devenir visible.
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		for i := 0; i < 20; i++ {
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			m = updated.(*Model)
		}
		if !strings.Contains(m.View(), "Données") {
			t.Fatalf("le défilement n'atteint pas le bas de l'aide :\n%s", m.View())
		}

		// L'aide est MODALE : une flèche lui appartient, elle ne doit pas
		// atteindre l'écran de dessous.
		before := m.active
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		if updated.(*Model).active != before {
			t.Fatal("tab a changé d'écran alors que l'aide couvrait l'écran")
		}
		// « échap » la referme.
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if updated.(*Model).showHelp {
			t.Fatal("« échap » doit refermer l'aide")
		}
	})
}

// TestBodiesFitWithoutCutting : TestScreensNeverExceedTheTerminal passe
// même quand Fit coupe le corps — il ne voit que le résultat. Celui-ci
// exige que chaque écran TIENNE dans la hauteur accordée, dès 60×18 : une
// coupure annoncée vaut mieux qu'une coupure muette, mais un écran qui
// n'a rien à couper vaut mieux que les deux.
func TestBodiesFitWithoutCutting(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		for _, size := range screenSizes {
			model, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = model.(*Model)
			body := m.height - lipgloss.Height(m.renderHeader()) - lipgloss.Height(m.renderFooter())
			for i, v := range m.views {
				if h := lipgloss.Height(v.Render(m.width, body)); h > body {
					t.Errorf("écran %s en %d×%d : corps de %d lignes pour %d accordées",
						m.views[i].Title(), size[0], size[1], h, body)
				}
			}
		}
	})
}

// TestHeaderShowsAccountCurrency : la devise du compte décide des paires
// qui tradent ; elle reste dans l'entête à toutes les largeurs.
func TestHeaderShowsAccountCurrency(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		cur := m.app.Config.Backtest.AccountCurrency
		for _, size := range screenSizes {
			model, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = model.(*Model)
			if h := m.renderHeader(); !strings.Contains(h, cur) {
				t.Errorf("devise %s absente de l'entête en %d colonnes :\n%s", cur, size[0], h)
			}
		}
	})
}

// TestTheTwoInterfacesSplitTheScreens : gw montre la séance, gw backtrain
// l'atelier — dans l'ordre du travail — et l'entête dit laquelle est
// ouverte. L'atelier n'affiche AUCUN badge de séance : il ne trade pas.
func TestTheTwoInterfacesSplitTheScreens(t *testing.T) {
	titles := func(m *Model) []string {
		var out []string
		for _, v := range m.views {
			out = append(out, v.Title())
		}
		return out
	}
	trading := New(newTestApp(t), Trading)
	if got := strings.Join(titles(trading), ","); got != "Live,Journal,Paramètres" {
		t.Fatalf("écrans de gw : %s", got)
	}
	backtrain := New(newTestAppMode(t, app.Workshop), Backtrain)
	if got := strings.Join(titles(backtrain), ","); got != "Données,Entraînement,Backtest,Paramètres" {
		t.Fatalf("écrans de gw backtrain : %s", got)
	}
	backtrain.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	header := backtrain.renderHeader()
	for _, want := range []string{"Backtrain", "ATELIER", backtrain.app.Config.Strategy.Name} {
		if !strings.Contains(header, want) {
			t.Errorf("« %s » absent de l'entête de l'atelier :\n%s", want, header)
		}
	}
	for _, unwanted := range []string{"REJEU", "PAPER", "kill-switch"} {
		if strings.Contains(header, unwanted) {
			t.Errorf("l'atelier affiche un badge de séance (%s) :\n%s", unwanted, header)
		}
	}
	trading.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if h := trading.renderHeader(); !strings.Contains(h, "· Live") {
		t.Errorf("l'entête de gw doit dire « Live » :\n%s", h)
	}
}

// TestHeaderAlwaysNamesTheInterface : deux interfaces peuvent être
// ouvertes côte à côte ; leur nom reste dans l'entête à toutes les
// largeurs, comme les badges de sécurité.
func TestHeaderAlwaysNamesTheInterface(t *testing.T) {
	forEachMode(t, func(t *testing.T, m *Model) {
		for _, size := range screenSizes {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			if h := m.renderHeader(); !strings.Contains(h, m.modeName()) {
				t.Errorf("« %s » absent de l'entête en %d colonnes :\n%s", m.modeName(), size[0], h)
			}
		}
	})
}

// TestRepairedConfigIsAnnouncedInTheStatusBar : des réglages remis
// d'office au démarrage se lisent dans la barre d'état, dans les deux
// interfaces.
func TestRepairedConfigIsAnnouncedInTheStatusBar(t *testing.T) {
	a := newTestApp(t)
	a.Config.Repairs = []config.Repair{{Key: "strategy.name", Old: "martinet_v1_0", New: "martinet_v1_1"}}
	for _, mode := range []Mode{Trading, Backtrain} {
		m := New(a, mode)
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		if f := m.renderFooter(); !strings.Contains(f, "réparé") || !strings.Contains(f, "strategy.name") {
			t.Fatalf("mode %d : annonce absente de la barre d'état :\n%s", mode, f)
		}
	}
}
