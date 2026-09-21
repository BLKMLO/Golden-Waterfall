// Package theme centralise TOUTES les couleurs et tous les styles de
// l'interface.
//
// Aucune vue ne construit un lipgloss.Style à partir d'une couleur
// littérale : sans cette règle, un thème « cohérent » se délite en
// quelques semaines, et passer en thème clair devient une chasse au
// trésor. Les vues composent des styles NOMMÉS par leur rôle
// (Positive, Muted, Title…), jamais par leur teinte.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette : les couleurs brutes, déclinées clair/sombre.
//
// lipgloss.AdaptiveColor choisit automatiquement selon la luminosité du
// terminal, ce qui règle le cas fréquent d'un terminal clair où un gris
// « discret » pensé pour le sombre devient illisible.
type Palette struct {
	Background lipgloss.TerminalColor
	Surface    lipgloss.TerminalColor
	Text       lipgloss.TerminalColor
	Muted      lipgloss.TerminalColor
	Border     lipgloss.TerminalColor
	Accent     lipgloss.TerminalColor
	Positive   lipgloss.TerminalColor
	Negative   lipgloss.TerminalColor
	Warning    lipgloss.TerminalColor
	Info       lipgloss.TerminalColor
	Highlight  lipgloss.TerminalColor
}

// Theme : la palette PLUS les styles dérivés, prêts à l'emploi.
type Theme struct {
	Name    string
	Palette Palette

	Title       lipgloss.Style
	Subtitle    lipgloss.Style
	Text        lipgloss.Style
	Muted       lipgloss.Style
	Positive    lipgloss.Style
	Negative    lipgloss.Style
	Warning     lipgloss.Style
	Info        lipgloss.Style
	Accent      lipgloss.Style
	Panel       lipgloss.Style
	PanelTitle  lipgloss.Style
	Tab         lipgloss.Style
	TabActive   lipgloss.Style
	StatusBar   lipgloss.Style
	KeyHint     lipgloss.Style
	KeyCap      lipgloss.Style
	BadgeOn     lipgloss.Style
	BadgeOff    lipgloss.Style
	BadgeWarn   lipgloss.Style
	BadgeSim    lipgloss.Style
	TableHeader lipgloss.Style
	TableRow    lipgloss.Style
	TableCursor lipgloss.Style
}

func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// Dark et Light sont les deux thèmes livrés.
//
// Les couleurs de gain et de perte NE SONT PAS un simple vert et un simple
// rouge : la paire retenue (turquoise / magenta-rouge) reste distinguable
// pour les daltonismes deutan et protan, les plus fréquents. Sur un écran
// dont le seul rôle est de dire « je gagne » ou « je perds », c'est un
// détail qui compte.
func Dark() Theme {
	p := Palette{
		Background: adaptive("#FFFFFF", "#0B0E14"),
		Surface:    adaptive("#F2F4F8", "#141821"),
		Text:       adaptive("#1A1D23", "#D7DCE5"),
		Muted:      adaptive("#6B7280", "#6E7787"),
		Border:     adaptive("#D3D8E0", "#2A3040"),
		Accent:     adaptive("#B8860B", "#E3B341"),
		Positive:   adaptive("#0E7C66", "#2EC4A6"),
		Negative:   adaptive("#B3261E", "#FF6B81"),
		Warning:    adaptive("#B45309", "#F0A030"),
		Info:       adaptive("#1D4ED8", "#6BA8FF"),
		Highlight:  adaptive("#EAEEF6", "#1E2533"),
	}
	return build("dark", p)
}

// Light renvoie le même thème : les couleurs étant adaptatives, la
// distinction se fait à l'exécution selon le terminal. Le réglage
// ui.theme force l'interprétation quand la détection se trompe.
func Light() Theme {
	t := Dark()
	t.Name = "light"
	return t
}

// ByName renvoie un thème par son nom.
func ByName(name string) Theme {
	if name == "light" {
		return Light()
	}
	return Dark()
}

func build(name string, p Palette) Theme {
	base := lipgloss.NewStyle().Foreground(p.Text)
	badge := lipgloss.NewStyle().Padding(0, 1).Bold(true)
	return Theme{
		Name:    name,
		Palette: p,

		Title:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		Subtitle: lipgloss.NewStyle().Foreground(p.Text).Bold(true),
		Text:     base,
		Muted:    lipgloss.NewStyle().Foreground(p.Muted),
		Positive: lipgloss.NewStyle().Foreground(p.Positive),
		Negative: lipgloss.NewStyle().Foreground(p.Negative),
		Warning:  lipgloss.NewStyle().Foreground(p.Warning),
		Info:     lipgloss.NewStyle().Foreground(p.Info),
		Accent:   lipgloss.NewStyle().Foreground(p.Accent),

		Panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border).
			Padding(0, 1),
		PanelTitle: lipgloss.NewStyle().Foreground(p.Accent).Bold(true),

		Tab:       lipgloss.NewStyle().Foreground(p.Muted).Padding(0, 2),
		TabActive: lipgloss.NewStyle().Foreground(p.Background).Background(p.Accent).Bold(true).Padding(0, 2),

		StatusBar: lipgloss.NewStyle().Foreground(p.Muted),
		KeyHint:   lipgloss.NewStyle().Foreground(p.Muted),
		KeyCap:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true),

		BadgeOn:   badge.Foreground(p.Background).Background(p.Positive),
		BadgeOff:  badge.Foreground(p.Text).Background(p.Highlight),
		BadgeWarn: badge.Foreground(p.Background).Background(p.Warning),
		BadgeSim:  badge.Foreground(p.Background).Background(p.Info),

		TableHeader: lipgloss.NewStyle().Foreground(p.Muted).Bold(true),
		TableRow:    base,
		TableCursor: lipgloss.NewStyle().Foreground(p.Text).Background(p.Highlight).Bold(true),
	}
}
