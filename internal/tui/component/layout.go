package component

import (
	"fmt"
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

// PanelContent est la largeur réellement disponible DANS un panneau de
// largeur `width` : deux colonnes de bordure et deux de marge.
//
// Les vues la calculaient de tête, et se trompaient de deux colonnes : le
// contenu dépassait alors la zone de texte et lipgloss l'enroulait, ce qui
// donnait un tableau sur deux lignes par enregistrement.
func PanelContent(width int) int {
	if w := width - 4; w > 0 {
		return w
	}
	return 1
}

// Panel encadre un contenu avec un titre. Le cadre occupe EXACTEMENT
// `width` colonnes, bordures comprises — il s'arrêtait deux colonnes plus
// tôt que les filets de séparation qui le soulignaient.
func Panel(th theme.Theme, title, content string, width int) string {
	// lipgloss.Width() compte la marge intérieure mais pas la bordure.
	inner := width - 2
	if inner < 4 {
		inner = 4
	}
	head := th.PanelTitle.Render(Truncate(title, PanelContent(width)))
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
	// Enrouler AVANT de compter : une ligne trop longue était comptée
	// pour une et rendue sur deux, et le panneau dépassait d'autant la
	// hauteur promise — ce qui désalignait son voisin.
	content = lipgloss.NewStyle().Width(PanelContent(width)).Render(content)
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

// StatRow aligne des cartes, en passant à la LIGNE SUIVANTE plutôt qu'en
// abandonnant celles qui ne tiennent pas.
//
// Les retirer silencieusement était le même défaut qu'afficher zéro pour
// une valeur inconnue : une carte absente devient indistinguable d'une
// carte sans objet. Sur un écran étroit, le panneau Compte perdait ainsi
// « Bougies » et « Ordres » sans que rien ne le dise.
//
// Au-delà de maxStatRows rangées, le reste est résumé par une carte
// « +N » : à ce stade, empiler encore mangerait tout l'écran.
func StatRow(th theme.Theme, cards []StatCard, width int) string {
	if len(cards) == 0 || width < 12 {
		return ""
	}
	const (
		minCard     = 14
		maxStatRows = 3
	)
	perRow := width / minCard
	if perRow < 1 {
		perRow = 1
	}
	if extra := len(cards) - perRow*maxStatRows; extra > 0 {
		cards = append(cards[:perRow*maxStatRows-1:perRow*maxStatRows-1], StatCard{
			Label: "non affichées",
			Value: fmt.Sprintf("+%d", extra+1),
			Style: th.Muted,
		})
	}

	rows := make([]string, 0, (len(cards)+perRow-1)/perRow)
	for start := 0; start < len(cards); start += perRow {
		end := start + perRow
		if end > len(cards) {
			end = len(cards)
		}
		rows = append(rows, statLine(th, cards[start:end], width, perRow))
	}
	return strings.Join(rows, "\n")
}

// statLine dessine une rangée. La largeur de carte est celle d'une rangée
// PLEINE : sans cela, la dernière rangée étalerait deux cartes sur tout
// l'écran et les colonnes ne seraient plus alignées d'une rangée à
// l'autre.
func statLine(th theme.Theme, cards []StatCard, width, perRow int) string {
	cardWidth := width / perRow
	if cardWidth < 1 {
		cardWidth = 1
	}
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
	// Priority : ordre de SACRIFICE quand la largeur manque. 0 = colonne
	// essentielle, jamais retirée ; plus le nombre est grand, plus la
	// colonne part tôt. Sans cet ordre, le tableau s'enroulait sur deux
	// lignes par enregistrement et devenait illisible.
	Priority int
	// Flex : la colonne absorbe le déficit de largeur avant qu'une autre
	// ne soit retirée. Typiquement la colonne de texte libre.
	Flex bool
	// Min : largeur en deçà de laquelle une colonne Flex ne se laisse
	// plus comprimer.
	Min int
}

// fitColumns ajuste les colonnes à la largeur RÉELLEMENT disponible.
//
// Les largeurs étaient fixes : dès que leur somme dépassait le panneau —
// ce qui arrivait à 100 colonnes sur l'écran Live, la taille la plus
// courante — lipgloss enroulait, et chaque ligne du tableau en occupait
// deux. Un tableau enroulé n'est pas un tableau.
//
// Deux leviers, dans cet ordre : comprimer les colonnes `Flex` jusqu'à
// leur `Min`, puis retirer les colonnes par `Priority` décroissante. Une
// colonne de priorité 0 n'est jamais retirée : c'est à l'appelant de dire
// ce qui fait le sens de la ligne.
// Elle renvoie les colonnes retenues ET leur indice d'ORIGINE : les
// cellules d'une ligne sont rangées dans l'ordre déclaré par l'appelant,
// pas dans celui des colonnes survivantes. Les confondre affichait le prix
// dans la colonne « État ».
func fitColumns(cols []Column, width int) ([]Column, []int) {
	idx := make([]int, len(cols))
	for i := range cols {
		idx[i] = i
	}
	if width <= 0 || len(cols) == 0 {
		return cols, idx
	}
	out := append([]Column(nil), cols...)
	// 1 colonne pour le marqueur de curseur, 1 séparateur entre colonnes.
	used := func(cs []Column) int {
		n := 1 + len(cs) - 1
		for _, c := range cs {
			n += c.Width
		}
		return n
	}

	for used(out) > width {
		// Comprimer d'abord ce qui est élastique.
		shrunk := false
		for i := range out {
			min := out[i].Min
			if min <= 0 {
				min = 4
			}
			if out[i].Flex && out[i].Width > min {
				out[i].Width--
				shrunk = true
				if used(out) <= width {
					return out, idx
				}
			}
		}
		if shrunk {
			continue
		}
		// Puis sacrifier la colonne la moins essentielle.
		victim, rank := -1, 0
		for i, c := range out {
			if c.Priority > rank {
				victim, rank = i, c.Priority
			}
		}
		if victim < 0 {
			return out, idx // plus rien à céder : l'appelant coupera.
		}
		out = append(out[:victim], out[victim+1:]...)
		idx = append(idx[:victim], idx[victim+1:]...)
	}
	return grow(out, cols, idx, used(out), width), idx
}

// grow rend aux colonnes élastiques la place libérée par une colonne
// retirée, sans jamais dépasser leur largeur DÉCLARÉE.
//
// Sans ce retour en arrière, une colonne comprimée à son minimum pour
// tenter d'éviter une suppression restait comprimée après la suppression :
// on perdait une colonne ET on affichait l'autre tronquée, avec de la
// place inutilisée à côté.
func grow(out, declared []Column, idx []int, used, width int) []Column {
	for used < width {
		grown := false
		for i := range out {
			if out[i].Flex && out[i].Width < declared[idx[i]].Width && used < width {
				out[i].Width++
				used++
				grown = true
			}
		}
		if !grown {
			break
		}
	}
	return out
}

// Table rend un tableau simple avec curseur.
//
// `cursor` < 0 = aucune ligne sélectionnée. Les cellules sont déjà
// stylées par l'appelant : le tableau ne décide pas des couleurs, il gère
// l'alignement, la sélection et l'ADAPTATION à la largeur.
//
// `width` est la largeur disponible pour les lignes (contenu d'un panneau
// = largeur du panneau − 4). À 0, le tableau garde ses largeurs
// déclarées — mais aucune vue ne devrait faire ce pari.
func Table(th theme.Theme, cols []Column, rows [][]string, cursor, maxRows, width int) string {
	var sb strings.Builder
	all := cols
	cols, source := fitColumns(cols, width)
	// clip ramène chaque ligne dans la largeur : dernier rempart pour
	// qu'un tableau ne s'enroule JAMAIS, même si les colonnes
	// essentielles suffisent à déborder.
	clip := func(line string) string {
		if width <= 0 {
			return line
		}
		return Clip(line, width)
	}

	head := make([]string, len(cols))
	for i, c := range cols {
		if c.Right {
			head[i] = PadLeft(c.Title, c.Width)
		} else {
			head[i] = Pad(c.Title, c.Width)
		}
	}
	headline := th.TableHeader.Render(" " + strings.Join(head, " "))
	if len(cols) < len(all) {
		// Une colonne retirée se DIT : sinon l'information manquante
		// passe pour une information inexistante.
		headline += th.Muted.Render(fmt.Sprintf("  +%d col.", len(all)-len(cols)))
	}
	sb.WriteString(clip(headline))
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
			if src := source[j]; src < len(rows[i]) {
				value = rows[i][src]
			}
			// La largeur se calcule sur le texte NU : les séquences ANSI
			// des styles ne consomment aucune colonne à l'écran.
			pad := c.Width - lipgloss.Width(value)
			if pad < 0 {
				// Cellule trop longue : on la COUPE à sa colonne, avec
				// des points de suspension pour que la coupe se voie.
				// Sans cela, une colonne comprimée ne rendrait aucune
				// place et le rognage final mangerait les suivantes.
				value, pad = Clip(value, c.Width-1)+th.Muted.Render("…"), 0
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
		sb.WriteString(clip(line))
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
