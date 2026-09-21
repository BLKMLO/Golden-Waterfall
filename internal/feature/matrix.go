// Package feature transforme une série de bougies en matrice de features
// prête pour l'apprentissage.
//
// Garantie anti-fuite : toute feature à la bougie t n'utilise que des
// données <= t (cf. package indicator). Elle est vérifiée par un test de
// STABILITÉ PAR PRÉFIXE : Compute(série)[:k] doit être identique à
// Compute(série[:k]). Une feature qui regarderait vers l'avant ferait
// échouer ce test — c'est le garde-fou le moins cher et le plus efficace
// contre la fuite temporelle.
package feature

import (
	"fmt"
	"math"
)

// Matrix est une matrice dense en ligne-majeur, avec ses noms de colonnes.
//
// Choix volontaire de ne PAS réimplémenter un DataFrame : tout le projet a
// besoin d'un bloc de float64 contigu (c'est ce que consomme le GBDT) et
// d'un ordre de colonnes FIGÉ. Un type de 60 lignes fait le travail et
// reste inspectable.
type Matrix struct {
	Rows  int
	Cols  int
	Names []string
	Data  []float64 // len == Rows*Cols
}

// NewMatrix alloue une matrice remplie de NaN (aucune valeur par défaut
// n'est légitime : un zéro se confondrait avec une mesure réelle).
func NewMatrix(rows int, names []string) *Matrix {
	data := make([]float64, rows*len(names))
	for i := range data {
		data[i] = math.NaN()
	}
	return &Matrix{Rows: rows, Cols: len(names), Names: append([]string(nil), names...), Data: data}
}

// At lit la valeur (ligne, colonne).
func (m *Matrix) At(row, col int) float64 { return m.Data[row*m.Cols+col] }

// Set écrit la valeur (ligne, colonne).
func (m *Matrix) Set(row, col int, v float64) { m.Data[row*m.Cols+col] = v }

// Row renvoie une VUE sur la ligne (aucune copie).
func (m *Matrix) Row(row int) []float64 { return m.Data[row*m.Cols : (row+1)*m.Cols] }

// Column renvoie une copie de la colonne.
func (m *Matrix) Column(col int) []float64 {
	out := make([]float64, m.Rows)
	for r := 0; r < m.Rows; r++ {
		out[r] = m.At(r, col)
	}
	return out
}

// ColumnIndex renvoie l'indice d'une colonne par son nom.
func (m *Matrix) ColumnIndex(name string) (int, error) {
	for i, n := range m.Names {
		if n == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("colonne %q absente de la matrice (%d colonnes)", name, m.Cols)
}

// SetColumn écrit une colonne entière depuis un vecteur.
func (m *Matrix) SetColumn(col int, values []float64) {
	n := m.Rows
	if len(values) < n {
		n = len(values)
	}
	for r := 0; r < n; r++ {
		m.Set(r, col, values[r])
	}
}

// RowComplete indique si la ligne ne contient aucun NaN. Sert au filtrage
// du jeu d'entraînement (on retire la chauffe des features).
func (m *Matrix) RowComplete(row int) bool {
	for _, v := range m.Row(row) {
		if math.IsNaN(v) {
			return false
		}
	}
	return true
}

// AppendColumn ajoute une colonne (utilisé par Colibri v1.1 pour la
// feature catégorielle `symbol`). Réalloue : à ne pas faire en boucle.
func (m *Matrix) AppendColumn(name string, values []float64) *Matrix {
	out := NewMatrix(m.Rows, append(append([]string(nil), m.Names...), name))
	for r := 0; r < m.Rows; r++ {
		copy(out.Row(r)[:m.Cols], m.Row(r))
		v := math.NaN()
		if r < len(values) {
			v = values[r]
		}
		out.Set(r, m.Cols, v)
	}
	return out
}

// Concat empile verticalement des matrices de colonnes IDENTIQUES (même
// noms, même ordre). Un désaccord est une erreur : concaténer deux jeux
// dont les colonnes ne correspondent pas produirait un modèle qui apprend
// n'importe quoi, en silence.
func Concat(parts ...*Matrix) (*Matrix, error) {
	parts = nonEmpty(parts)
	if len(parts) == 0 {
		return nil, fmt.Errorf("aucune matrice à concaténer")
	}
	names := parts[0].Names
	total := 0
	for _, p := range parts {
		if len(p.Names) != len(names) {
			return nil, fmt.Errorf("colonnes incompatibles : %d contre %d", len(p.Names), len(names))
		}
		for i := range names {
			if p.Names[i] != names[i] {
				return nil, fmt.Errorf("colonne %d : %q contre %q", i, p.Names[i], names[i])
			}
		}
		total += p.Rows
	}
	out := NewMatrix(total, names)
	row := 0
	for _, p := range parts {
		copy(out.Data[row*out.Cols:], p.Data)
		row += p.Rows
	}
	return out, nil
}

func nonEmpty(parts []*Matrix) []*Matrix {
	out := parts[:0:0]
	for _, p := range parts {
		if p != nil && p.Rows > 0 {
			out = append(out, p)
		}
	}
	return out
}

// SelectRows construit une nouvelle matrice avec les lignes demandées, dans
// l'ordre donné.
func (m *Matrix) SelectRows(rows []int) *Matrix {
	out := NewMatrix(len(rows), m.Names)
	for i, r := range rows {
		copy(out.Row(i), m.Row(r))
	}
	return out
}
