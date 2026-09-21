// Package component regroupe les briques d'affichage réutilisables de la
// TUI : formats, graphiques, panneaux, tableaux.
//
// Règle d'honnêteté appliquée jusque dans le formatage : une valeur
// INDISPONIBLE s'affiche « — », jamais « 0 ». Un zéro se confond avec une
// mesure réelle ; un tiret dit clairement « on ne sait pas ».
package component

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Dash est le marqueur universel d'une donnée indisponible.
const Dash = "—"

// Num formate un nombre avec n décimales, ou « — » s'il n'est pas fini.
func Num(v float64, decimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Dash
	}
	return fmt.Sprintf("%.*f", decimals, v)
}

// Price formate un prix : 5 décimales en forex, 3 sur les paires JPY, les
// métaux et les indices. La règle suit la convention du marché, pas une
// préférence d'affichage.
func Price(symbol string, v float64) string {
	if v <= 0 || math.IsNaN(v) {
		return Dash
	}
	decimals := 5
	up := strings.ToUpper(symbol)
	switch {
	case strings.HasSuffix(up, "JPY"), strings.HasPrefix(up, "XAU"), strings.HasPrefix(up, "XAG"):
		decimals = 3
	case strings.HasPrefix(up, "US"), strings.HasPrefix(up, "NAS"),
		strings.HasPrefix(up, "DE"), strings.HasPrefix(up, "UK"), strings.HasPrefix(up, "JP"):
		decimals = 2
	}
	return fmt.Sprintf("%.*f", decimals, v)
}

// Pct formate un pourcentage signé.
func Pct(v float64, decimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Dash
	}
	return fmt.Sprintf("%+.*f %%", decimals, v)
}

// Money formate un montant.
func Money(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Dash
	}
	return fmt.Sprintf("%+.2f", v)
}

// Ratio formate un ratio, avec « ∞ » quand il est réellement infini (un
// profit factor sans aucune perte, par exemple). Écrire un grand nombre à
// la place ferait croire à une mesure.
func Ratio(v float64) string {
	switch {
	case math.IsNaN(v):
		return Dash
	case math.IsInf(v, 1):
		return "∞"
	case math.IsInf(v, -1):
		return "-∞"
	}
	return fmt.Sprintf("%.2f", v)
}

// Count formate un entier avec des séparateurs de milliers (espace fine
// insécable, convention française).
func Count(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	joined := strings.Join(out, " ")
	if neg {
		return "-" + joined
	}
	return joined
}

// Bytes formate une taille lisible.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d o", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %co", float64(n)/float64(div), "kMGTPE"[exp])
}

// Time formate un instant UTC court, ou « — » s'il est nul.
func Time(t time.Time) string {
	if t.IsZero() {
		return Dash
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// Clock formate l'heure seule.
func Clock(t time.Time) string {
	if t.IsZero() {
		return Dash
	}
	return t.UTC().Format("15:04:05")
}

// Duration formate une durée de façon compacte.
func Duration(d time.Duration) string {
	if d <= 0 {
		return Dash
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%.1f j", d.Hours()/24)
}

// Truncate coupe une chaîne à `width` colonnes en ajoutant une ellipse.
func Truncate(s string, width int) string {
	runes := []rune(s)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// Pad complète une chaîne à droite jusqu'à `width` colonnes.
func Pad(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return Truncate(s, width)
	}
	return s + strings.Repeat(" ", width-n)
}

// PadLeft complète à gauche (colonnes numériques).
func PadLeft(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return Truncate(s, width)
	}
	return strings.Repeat(" ", width-n) + s
}
