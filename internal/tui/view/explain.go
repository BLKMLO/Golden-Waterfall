package view

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// explainPanel rend un panneau explicatif qui TIENT dans la hauteur
// donnée : la version longue si elle entre, sinon la courte.
//
// Les textes étaient coupés à la main à soixante-dix colonnes : sur un
// terminal plus étroit, chaque ligne s'enroulait en deux et un paragraphe
// de six lignes en prenait douze. Les paragraphes sont désormais écrits
// d'un trait, et c'est le panneau qui enroule à sa largeur.
func explainPanel(th theme.Theme, title string, width, height int, long, short string) string {
	out := component.Panel(th, title, th.Muted.Render(long), width)
	if lipgloss.Height(out) <= height || short == "" {
		return out
	}
	return component.Panel(th, title, th.Muted.Render(short), width)
}
