package component

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// Clip coupe une chaîne DÉJÀ STYLÉE à `width` colonnes.
//
// Truncate compte les runes : sur du texte stylé, il compterait les
// séquences d'échappement ANSI comme des caractères et couperait donc
// beaucoup trop tôt (une barre de raccourcis colorée perdait la moitié de
// son contenu sur un terminal large). MaxWidth, lui, raisonne en COLONNES
// affichées.
func Clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// Panel encadre un contenu avec un titre.
func Panel(th theme.Theme, title, content string, width int) string {
	inner := width - 4 // bordure + marge intérieure
	if inner < 4 {
		inner = 4
	}
	head := th.PanelTitle.Render(Truncate(title, inner))
	return th.Panel.Width(inner).Render(head + "\n" + content)
}

// PanelH encadre un contenu en imposant une hauteur TOTALE exacte
// (bordures comprises).
//
// Sans elle, deux panneaux côte à côte prennent chacun la hauteur de leur
// contenu et se désalignent dès que l'un est plus rempli que l'autre — ce
// qui, sur un tableau de bord, donne immédiatement une impression de
// bricolage.
func PanelH(th theme.Theme, title, content string, width, height int) string {
	if height < 3 {
		height = 3
	}
	lines := strings.Split(content, "\n")
	inner := height - 3 // bordures (2) + ligne de titre (1)
	if inner < 0 {
		inner = 0
	}
	if len(lines) > inner {
		lines = lines[:inner]
	}
	for len(lines) < inner {
		lines = append(lines, "")
	}
	return Panel(th, title, strings.Join(lines, "\n"), width)
}

// Fill complète un bloc pour qu'il occupe exactement `height` lignes.
// Sert à ancrer le pied de page en bas de l'écran plutôt que de le laisser
// flotter juste sous le contenu.
func Fill(content string, height int) string {
	n := lipgloss.Height(content)
	if n >= height {
		return content
	}
	return content + strings.Repeat("\n", height-n)
}

// StatCard : une statistique nommée, avec sa valeur mise en évidence.
type StatCard struct {
	Label string
	Value string
	Style lipgloss.Style
	Note  string
}

// StatRow aligne plusieurs cartes sur une ligne.
//
// La largeur est répartie ÉQUITABLEMENT et les cartes qui ne tiennent pas
// sont retirées plutôt que tronquées en bouillie : mieux vaut trois
// chiffres lisibles que six illisibles.
func StatRow(th theme.Theme, cards []StatCard, width int) string {
	if len(cards) == 0 || width < 12 {
		return ""
	}
	const minCard = 14
	max := width / minCard
	if max < 1 {
		max = 1
	}
	if len(cards) > max {
		cards = cards[:max]
	}
	cardWidth := width / len(cards)

	cols := make([]string, 0, len(cards))
	for _, c := range cards {
		style := c.Style
		if style.String() == "" {
			style = th.Text
		}
		lines := []string{
			th.Muted.Render(Truncate(c.Label, cardWidth-1)),
			style.Bold(true).Render(Truncate(c.Value, cardWidth-1)),
		}
		if c.Note != "" {
			lines = append(lines, th.Muted.Render(Truncate(c.Note, cardWidth-1)))
		}
		cols = append(cols, lipgloss.NewStyle().Width(cardWidth).Render(strings.Join(lines, "\n")))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// Column décrit une colonne de tableau.
type Column struct {
	Title string
	Width int
	Right bool // alignement à droite (colonnes numériques)
}

// Table rend un tableau simple avec curseur.
//
// `cursor` < 0 = aucune ligne sélectionnée. Les cellules sont déjà
// stylées par l'appelant : le tableau ne décide pas des couleurs, il gère
// l'alignement et la sélection.
func Table(th theme.Theme, cols []Column, rows [][]string, cursor int, maxRows int) string {
	var sb strings.Builder

	head := make([]string, len(cols))
	for i, c := range cols {
		if c.Right {
			head[i] = PadLeft(c.Title, c.Width)
		} else {
			head[i] = Pad(c.Title, c.Width)
		}
	}
	sb.WriteString(th.TableHeader.Render(strings.Join(head, " ")))
	sb.WriteString("\n")

	if len(rows) == 0 {
		sb.WriteString(th.Muted.Render("  (aucune donnée)"))
		return sb.String()
	}

	start := 0
	if maxRows > 0 && len(rows) > maxRows {
		// Fenêtre glissante centrée sur le curseur : sur une liste de
		// 31 paires dans un terminal court, perdre la ligne sélectionnée
		// de vue rend la navigation inutilisable.
		start = cursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
	}
	end := len(rows)
	if maxRows > 0 && start+maxRows < end {
		end = start + maxRows
	}

	for i := start; i < end; i++ {
		cells := make([]string, len(cols))
		for j, c := range cols {
			value := ""
			if j < len(rows[i]) {
				value = rows[i][j]
			}
			// La largeur se calcule sur le texte NU : les séquences ANSI
			// des styles ne consomment aucune colonne à l'écran.
			plain := lipgloss.NewStyle().Render(value)
			pad := c.Width - lipgloss.Width(plain)
			if pad < 0 {
				pad = 0
			}
			if c.Right {
				cells[j] = strings.Repeat(" ", pad) + value
			} else {
				cells[j] = value + strings.Repeat(" ", pad)
			}
		}
		line := strings.Join(cells, " ")
		if i == cursor {
			line = th.TableCursor.Render("▸" + line)
		} else {
			line = " " + line
		}
		sb.WriteString(line)
		if i < end-1 {
			sb.WriteString("\n")
		}
	}
	if end < len(rows) || start > 0 {
		sb.WriteString(th.Muted.Render("\n  " + Count(len(rows)) + " lignes"))
	}
	return sb.String()
}

// KeyHints rend une barre d'aide « touche → action ».
func KeyHints(th theme.Theme, pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, th.KeyCap.Render(p[0])+" "+th.KeyHint.Render(p[1]))
	}
	return strings.Join(parts, th.Muted.Render("  ·  "))
}

// Badge rend une pastille d'état.
func Badge(th theme.Theme, label string, on bool) string {
	if on {
		return th.BadgeOn.Render(label)
	}
	return th.BadgeOff.Render(label)
}
