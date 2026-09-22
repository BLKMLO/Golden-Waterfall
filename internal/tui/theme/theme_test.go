package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
)

// TestApplyForcesBackground : ui.theme AGIT.
//
// C'est le test qui manquait quand la clé ne faisait rien. Il échoue dès
// que Apply cesse de forcer la luminosité, donc dès que « dark » et
// « light » redeviennent des synonymes.
func TestApplyForcesBackground(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(true) })

	if forced, dark := Apply(NameDark); !forced || !dark {
		t.Fatalf("Apply(dark) = (%v, %v), attendu (true, true)", forced, dark)
	}
	if !lipgloss.HasDarkBackground() {
		t.Fatal("Apply(dark) n'a pas forcé le fond sombre")
	}
	if forced, dark := Apply(NameLight); !forced || dark {
		t.Fatalf("Apply(light) = (%v, %v), attendu (true, false)", forced, dark)
	}
	if lipgloss.HasDarkBackground() {
		t.Fatal("Apply(light) n'a pas forcé le fond clair")
	}
}

// TestApplyAutoChangesNothing : « auto » laisse la détection décider.
//
// Forcer par défaut serait le meilleur moyen de rendre l'interface
// illisible chez quelqu'un dont le terminal est correctement détecté.
func TestApplyAutoChangesNothing(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(true) })

	for _, want := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(want)
		if forced, dark := Apply(NameAuto); forced || dark != want {
			t.Fatalf("Apply(auto) = (%v, %v) alors que le terminal dit %v", forced, dark, want)
		}
		if lipgloss.HasDarkBackground() != want {
			t.Fatalf("Apply(auto) a modifié la détection")
		}
	}
}

// TestForcedThemesRenderDifferentColors : forcer le mode change VRAIMENT
// ce qui sort à l'écran.
//
// Sans lui, Apply pourrait forcer un drapeau que plus personne ne lit —
// le défaut d'origine, déguisé.
func TestForcedThemesRenderDifferentColors(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		lipgloss.SetHasDarkBackground(true)
	})
	// Hors terminal, le profil par défaut est sans couleur : tous les
	// styles rendraient la même chaîne et le test passerait pour de
	// mauvaises raisons.
	lipgloss.SetColorProfile(termenv.TrueColor)

	Apply(NameDark)
	dark := ByName(NameDark).Muted.Render("x")
	Apply(NameLight)
	light := ByName(NameLight).Muted.Render("x")

	if dark == light {
		t.Fatalf("le mode forcé ne change pas le rendu : %q", dark)
	}
	if !strings.Contains(dark, "\x1b[") || !strings.Contains(light, "\x1b[") {
		t.Fatalf("aucune couleur émise : dark=%q light=%q", dark, light)
	}
}

// TestNamesMatchConfig : les valeurs proposées par l'écran Paramètres sont
// exactement celles que la configuration accepte.
//
// Une liste qui dérive de la validation, c'est un écran qui propose une
// valeur refusée au démarrage suivant.
func TestNamesMatchConfig(t *testing.T) {
	for _, name := range Names {
		c := config.Default()
		c.UI.Theme = name
		if err := c.Validate(); err != nil {
			t.Fatalf("theme.Names propose %q, que config refuse : %v", name, err)
		}
	}
	c := config.Default()
	c.UI.Theme = "sepia"
	if err := c.Validate(); err == nil {
		t.Fatal("config accepte un thème inconnu")
	}
}

// TestEveryStyleIsDefined : aucun style nommé n'est laissé à zéro.
//
// Un lipgloss.Style vide rend le texte SANS couleur, ce qui ne casse rien
// et ne se remarque pas — jusqu'à ce qu'un écran entier soit gris.
func TestEveryStyleIsDefined(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	lipgloss.SetColorProfile(termenv.TrueColor)

	th := Dark()
	styles := map[string]lipgloss.Style{
		"Title": th.Title, "Subtitle": th.Subtitle, "Text": th.Text, "Muted": th.Muted,
		"Positive": th.Positive, "Negative": th.Negative, "Warning": th.Warning,
		"Info": th.Info, "Accent": th.Accent, "Panel": th.Panel, "PanelTitle": th.PanelTitle,
		"Tab": th.Tab, "TabActive": th.TabActive, "StatusBar": th.StatusBar,
		"KeyHint": th.KeyHint, "KeyCap": th.KeyCap, "BadgeOn": th.BadgeOn,
		"BadgeOff": th.BadgeOff, "BadgeWarn": th.BadgeWarn, "BadgeSim": th.BadgeSim,
		"TableHeader": th.TableHeader, "TableRow": th.TableRow, "TableCursor": th.TableCursor,
	}
	for name, st := range styles {
		if st.Render("x") == "x" {
			t.Errorf("style %s : rendu nu, donc non défini", name)
		}
	}
}
