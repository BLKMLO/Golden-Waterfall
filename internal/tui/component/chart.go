package component

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// --- Courbe en braille -----------------------------------------------------

// brailleDots : masque de bit de chaque point d'une cellule braille
// (2 colonnes × 4 lignes). L'alphabet braille Unicode donne ainsi une
// résolution de 2×4 par caractère — huit fois celle d'un bloc plein, ce
// qui change tout pour une courbe d'équité dans un terminal.
var brailleDots = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// LineChart dessine une courbe en braille sur `width` × `height` cellules.
//
// Renvoie les lignes prêtes à afficher. Une série vide ou constante ne
// produit pas un trait au hasard : la constante est tracée à mi-hauteur,
// ce qui est la lecture correcte d'une valeur qui ne bouge pas.
func LineChart(values []float64, width, height int, style lipgloss.Style) []string {
	if width < 2 || height < 1 {
		return nil
	}
	clean := finiteOnly(values)
	if len(clean) < 2 {
		return blankLines(width, height)
	}

	min, max := minMax(clean)
	span := max - min
	if span == 0 {
		span = 1
		min -= 0.5
	}

	cols := width * 2
	rows := height * 4
	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = make([]rune, width)
	}

	// Ré-échantillonnage : chaque sous-colonne braille reçoit la valeur
	// interpolée de la série, ce qui évite les marches d'escalier quand
	// la série est plus courte que la largeur disponible.
	prevRow := -1
	for c := 0; c < cols; c++ {
		pos := float64(c) / float64(cols-1) * float64(len(clean)-1)
		i := int(pos)
		frac := pos - float64(i)
		v := clean[i]
		if i+1 < len(clean) {
			v = clean[i]*(1-frac) + clean[i+1]*frac
		}
		norm := (v - min) / span
		row := int(math.Round((1 - norm) * float64(rows-1)))
		row = clamp(row, 0, rows-1)

		// Relier au point précédent : sans ça, une variation brutale
		// laisse un trou dans la courbe.
		from, to := row, row
		if prevRow >= 0 {
			from, to = minInt(prevRow, row), maxInt(prevRow, row)
		}
		for r := from; r <= to; r++ {
			setDot(grid, c, r)
		}
		prevRow = row
	}

	out := make([]string, height)
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if grid[y][x] == 0 {
				sb.WriteRune(' ')
				continue
			}
			sb.WriteRune(0x2800 + grid[y][x])
		}
		out[y] = style.Render(sb.String())
	}
	return out
}

func setDot(grid [][]rune, col, row int) {
	cellY, cellX := row/4, col/2
	if cellY >= len(grid) || cellX >= len(grid[cellY]) {
		return
	}
	grid[cellY][cellX] |= brailleDots[row%4][col%2]
}

// --- Chandeliers en blocs --------------------------------------------------

// CandleChart dessine des chandeliers en caractères de bloc.
//
// Chaque bougie occupe UNE colonne : la mèche en trait fin, le corps en
// bloc plein, colorés selon le sens. C'est volontairement sobre — un
// terminal n'a ni la résolution ni la palette d'un graphique web, et
// vouloir l'imiter produit une bouillie illisible.
func CandleChart(series core.Series, width, height int, th theme.Theme) []string {
	if width < 3 || height < 3 || len(series) == 0 {
		return blankLines(width, height)
	}
	if len(series) > width {
		series = series[len(series)-width:]
	}

	low, high := math.Inf(1), math.Inf(-1)
	for _, b := range series {
		low = math.Min(low, b.Low())
		high = math.Max(high, b.High())
	}
	span := high - low
	if span <= 0 {
		span = 1
	}
	level := func(v float64) int {
		norm := (v - low) / span
		return clamp(int(math.Round((1-norm)*float64(height-1))), 0, height-1)
	}

	cells := make([][]string, height)
	for y := range cells {
		cells[y] = make([]string, len(series))
		for x := range cells[y] {
			cells[y][x] = " "
		}
	}
	for x, b := range series {
		up := b.Close() >= b.Open()
		style := th.Negative
		if up {
			style = th.Positive
		}
		hiY, loY := level(b.High()), level(b.Low())
		bodyTop, bodyBottom := level(math.Max(b.Open(), b.Close())), level(math.Min(b.Open(), b.Close()))
		for y := hiY; y <= loY; y++ {
			cells[y][x] = style.Render("│")
		}
		for y := bodyTop; y <= bodyBottom; y++ {
			cells[y][x] = style.Render("█")
		}
	}

	out := make([]string, height)
	for y := 0; y < height; y++ {
		out[y] = strings.Join(cells[y], "")
	}
	return out
}

// --- Barre de progression --------------------------------------------------

// ProgressBar rend une barre pleine/vide de `width` colonnes.
func ProgressBar(ratio float64, width int, th theme.Theme) string {
	if width < 3 {
		return ""
	}
	ratio = math.Max(0, math.Min(1, ratio))
	filled := int(math.Round(ratio * float64(width)))
	return th.Accent.Render(strings.Repeat("━", filled)) +
		th.Muted.Render(strings.Repeat("─", width-filled))
}

// Sparkline rend une micro-courbe sur une seule ligne (blocs 1/8).
func Sparkline(values []float64, width int, style lipgloss.Style) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	clean := finiteOnly(values)
	if len(clean) == 0 || width < 1 {
		return ""
	}
	if len(clean) > width {
		// Sous-échantillonnage par pas régulier : on montre la FORME, pas
		// une moyenne qui gommerait les à-coups.
		step := float64(len(clean)-1) / float64(width-1)
		sampled := make([]float64, width)
		for i := range sampled {
			sampled[i] = clean[clamp(int(math.Round(float64(i)*step)), 0, len(clean)-1)]
		}
		clean = sampled
	}
	min, max := minMax(clean)
	span := max - min
	var sb strings.Builder
	for _, v := range clean {
		idx := 0
		if span > 0 {
			idx = clamp(int((v-min)/span*float64(len(blocks)-1)+0.5), 0, len(blocks)-1)
		}
		sb.WriteRune(blocks[idx])
	}
	return style.Render(sb.String())
}

// --- Utilitaires -----------------------------------------------------------

func finiteOnly(values []float64) []float64 {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		}
	}
	return out
}

func minMax(x []float64) (min, max float64) {
	min, max = math.Inf(1), math.Inf(-1)
	for _, v := range x {
		min = math.Min(min, v)
		max = math.Max(max, v)
	}
	return min, max
}

func blankLines(width, height int) []string {
	out := make([]string, height)
	blank := strings.Repeat(" ", maxInt(width, 0))
	for i := range out {
		out[i] = blank
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
