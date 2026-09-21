package component

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// TestUnavailableValuesShowDash verrouille la règle d'honnêteté jusque
// dans le formatage : « — » signifie « on ne sait pas », zéro signifie
// « zéro ». Les confondre, c'est mentir en silence.
func TestUnavailableValuesShowDash(t *testing.T) {
	cases := map[string]string{
		"Num NaN":       Num(math.NaN(), 2),
		"Num +Inf":      Num(math.Inf(1), 2),
		"Price nul":     Price("EURUSD", 0),
		"Pct NaN":       Pct(math.NaN(), 2),
		"Money NaN":     Money(math.NaN()),
		"Ratio NaN":     Ratio(math.NaN()),
		"Time zéro":     Time(time.Time{}),
		"Duration zéro": Duration(0),
	}
	for name, got := range cases {
		if got != Dash {
			t.Fatalf("%s doit s'afficher %q, reçu %q", name, Dash, got)
		}
	}
	if Num(0, 2) == Dash {
		t.Fatal("un zéro RÉEL ne doit jamais se confondre avec une absence de mesure")
	}
}

func TestRatioShowsInfinity(t *testing.T) {
	if got := Ratio(math.Inf(1)); got != "∞" {
		t.Fatalf("un profit factor sans aucune perte vaut ∞, reçu %q", got)
	}
	if got := Ratio(2.345); got != "2.35" {
		t.Fatalf("arrondi à deux décimales attendu, reçu %q", got)
	}
}

func TestPriceDecimalsFollowMarketConvention(t *testing.T) {
	if got := Price("EURUSD", 1.234567); got != "1.23457" {
		t.Fatalf("forex : 5 décimales, reçu %q", got)
	}
	if got := Price("USDJPY", 151.23456); got != "151.235" {
		t.Fatalf("paires JPY : 3 décimales, reçu %q", got)
	}
	if got := Price("XAUUSD", 2345.6789); got != "2345.679" {
		t.Fatalf("métaux : 3 décimales, reçu %q", got)
	}
	if got := Price("US500", 5432.198); got != "5432.20" {
		t.Fatalf("indices : 2 décimales, reçu %q", got)
	}
}

func TestCountUsesThinSpaces(t *testing.T) {
	if got := Count(1234567); got != "1 234 567" {
		t.Fatalf("séparateur de milliers français attendu, reçu %q", got)
	}
	if got := Count(-4200); got != "-4 200" {
		t.Fatalf("les négatifs doivent garder leur signe : %q", got)
	}
	if got := Count(42); got != "42" {
		t.Fatalf("pas de séparateur sous 1000 : %q", got)
	}
}

func TestTruncateAndPad(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("troncature incorrecte : %q", got)
	}
	if got := Truncate("abc", 10); got != "abc" {
		t.Fatalf("rien à tronquer : %q", got)
	}
	if got := Pad("ab", 5); got != "ab   " {
		t.Fatalf("remplissage à droite : %q", got)
	}
	if got := PadLeft("ab", 5); got != "   ab" {
		t.Fatalf("remplissage à gauche : %q", got)
	}
	// Les accents comptent pour UN caractère, pas pour leurs octets.
	if got := Pad("é", 3); len([]rune(got)) != 3 {
		t.Fatalf("largeur mal calculée sur un accent : %q", got)
	}
}

func TestLineChartDimensions(t *testing.T) {
	values := make([]float64, 200)
	for i := range values {
		values[i] = math.Sin(float64(i) / 10)
	}
	lines := LineChart(values, 40, 8, lipgloss.NewStyle())
	if len(lines) != 8 {
		t.Fatalf("%d lignes rendues, 8 demandées", len(lines))
	}
	// Au moins un point braille doit être dessiné.
	joined := strings.Join(lines, "")
	if !strings.ContainsAny(joined, "⠀⡀⠁⠂⠄⡄⢀⣀⣿") {
		t.Fatal("aucun point tracé : la courbe est vide")
	}
}

func TestLineChartHandlesDegenerateInput(t *testing.T) {
	if got := LineChart(nil, 20, 4, lipgloss.NewStyle()); len(got) != 4 {
		t.Fatalf("une série vide doit rendre un cadre vide de la bonne hauteur, reçu %d lignes", len(got))
	}
	flat := []float64{5, 5, 5, 5, 5}
	if got := LineChart(flat, 20, 4, lipgloss.NewStyle()); len(got) != 4 {
		t.Fatal("une série constante doit se tracer sans division par zéro")
	}
	withNaN := []float64{1, math.NaN(), 3, math.Inf(1), 5}
	if got := LineChart(withNaN, 20, 4, lipgloss.NewStyle()); len(got) != 4 {
		t.Fatal("les valeurs non finies doivent être écartées, pas faire paniquer")
	}
}

func TestSparklineLength(t *testing.T) {
	values := []float64{1, 5, 3, 9, 2, 7, 4}
	got := Sparkline(values, 7, lipgloss.NewStyle())
	if len([]rune(got)) != 7 {
		t.Fatalf("%d caractères pour 7 demandés : %q", len([]rune(got)), got)
	}
	long := make([]float64, 500)
	for i := range long {
		long[i] = float64(i)
	}
	if got := Sparkline(long, 20, lipgloss.NewStyle()); len([]rune(got)) != 20 {
		t.Fatalf("sous-échantillonnage incorrect : %q", got)
	}
}

func TestCandleChartHeight(t *testing.T) {
	th := theme.Dark()
	series := core.Series{
		{BidOpen: 1, BidHigh: 2, BidLow: 0.5, BidClose: 1.5},
		{BidOpen: 1.5, BidHigh: 1.8, BidLow: 1.2, BidClose: 1.3},
		{BidOpen: 1.3, BidHigh: 2.2, BidLow: 1.1, BidClose: 2.1},
	}
	lines := CandleChart(series, 20, 6, th)
	if len(lines) != 6 {
		t.Fatalf("%d lignes, 6 demandées", len(lines))
	}
	if got := CandleChart(nil, 20, 6, th); len(got) != 6 {
		t.Fatal("une série vide doit rendre un cadre vide")
	}
}

func TestProgressBarBounds(t *testing.T) {
	th := theme.Dark()
	for _, ratio := range []float64{-1, 0, 0.5, 1, 2} {
		if got := ProgressBar(ratio, 10, th); got == "" {
			t.Fatalf("barre vide pour un ratio de %v", ratio)
		}
	}
}

func TestTableRendersHeaderAndEmptyState(t *testing.T) {
	th := theme.Dark()
	cols := []Column{{Title: "Paire", Width: 8}, {Title: "Prix", Width: 8, Right: true}}
	empty := Table(th, cols, nil, -1, 5, 40)
	if !strings.Contains(empty, "aucune donnée") {
		t.Fatalf("un tableau vide doit le DIRE : %q", empty)
	}
	filled := Table(th, cols, [][]string{{"EURUSD", "1.10"}}, 0, 5, 40)
	if !strings.Contains(filled, "EURUSD") || !strings.Contains(filled, "Paire") {
		t.Fatalf("tableau incomplet : %q", filled)
	}
}

// TestTableNeverWraps : une ligne plus large que la place disponible
// s'enroulait sur deux lignes, et chaque enregistrement occupait deux
// lignes à l'écran. Un tableau enroulé n'est plus un tableau.
func TestTableNeverWraps(t *testing.T) {
	th := theme.Dark()
	cols := []Column{
		{Title: "Paire", Width: 8},
		{Title: "Bid", Width: 10, Right: true, Priority: 2},
		{Title: "Var.", Width: 8, Right: true, Priority: 3},
		{Title: "État", Width: 9},
		{Title: "Signal", Width: 12, Flex: true, Min: 6, Priority: 1},
	}
	rows := [][]string{{"EURUSD", "1.10512", "+0.42 %", "armée", "LONG 0.78"}}
	for _, width := range []int{20, 30, 42, 60, 100} {
		out := Table(th, cols, rows, 0, 5, width)
		for _, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("largeur %d : une ligne en occupe %d (%q)", width, w, line)
			}
		}
		// La paire reste lisible quoi qu'il arrive : c'est ce qui donne
		// son sens à la ligne.
		if !strings.Contains(out, "EURUSD") {
			t.Fatalf("largeur %d : la colonne essentielle a disparu\n%s", width, out)
		}
	}
}

// TestTableSaysWhenColumnsAreDropped : une colonne retirée faute de place
// ne doit pas passer pour une colonne inexistante.
func TestTableSaysWhenColumnsAreDropped(t *testing.T) {
	th := theme.Dark()
	cols := []Column{
		{Title: "Paire", Width: 8},
		{Title: "Bid", Width: 10, Priority: 2},
		{Title: "Var.", Width: 8, Priority: 3},
	}
	out := Table(th, cols, [][]string{{"EURUSD", "1.10", "+1 %"}}, -1, 5, 16)
	if !strings.Contains(out, "+") {
		t.Fatalf("les colonnes retirées doivent être annoncées :\n%s", out)
	}
}

// TestStatRowKeepsEveryCard : les cartes qui ne tenaient pas étaient
// retirées en silence, ce qui rendait une valeur manquante
// indistinguable d'une valeur sans objet.
func TestStatRowKeepsEveryCard(t *testing.T) {
	th := theme.Dark()
	cards := []StatCard{
		{Label: "Équité", Value: "1000"}, {Label: "Marge", Value: "10"},
		{Label: "Perte", Value: "0"}, {Label: "Ticks", Value: "42"},
		{Label: "Bougies", Value: "7"}, {Label: "Ordres", Value: "3"},
	}
	out := StatRow(th, cards, 60) // 4 cartes par rangée au plus
	for _, c := range cards {
		if !strings.Contains(out, c.Label) {
			t.Fatalf("carte « %s » perdue :\n%s", c.Label, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Fatalf("rangée de %d colonnes pour 60 disponibles", w)
		}
	}
}

// TestTableKeepsCellsWithTheirColumn : quand une colonne est retirée
// faute de place, les cellules suivantes ne doivent PAS glisser d'un cran
// — sinon le tableau affiche un prix sous l'entête « État », ce qui est
// pire que de ne rien afficher.
func TestTableKeepsCellsWithTheirColumn(t *testing.T) {
	th := theme.Dark()
	cols := []Column{
		{Title: "Paire", Width: 8},
		{Title: "Bid", Width: 10, Priority: 2},
		{Title: "Var.", Width: 8, Priority: 3},
		{Title: "État", Width: 9},
	}
	rows := [][]string{{"EURUSD", "1.10512", "+0.42", "armée"}}
	out := Table(th, cols, rows, -1, 5, 20) // Bid et Var. ne tiennent pas
	if !strings.Contains(out, "armée") {
		t.Fatalf("la valeur de la colonne conservée a disparu :\n%s", out)
	}
	if strings.Contains(out, "1.10512") || strings.Contains(out, "+0.42") {
		t.Fatalf("une valeur de colonne RETIRÉE est affichée dans une autre colonne :\n%s", out)
	}
}
