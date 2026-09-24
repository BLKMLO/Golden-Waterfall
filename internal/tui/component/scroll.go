package component

import "fmt"

// Scroll est le curseur d'une liste défilable affichée par Table.
//
// Table centre déjà sa fenêtre sur le curseur ; Scroll ne fait que
// tenir ce curseur dans les bornes et traduire les touches. Une liste de
// trades qui n'affiche que ses vingt premières lignes cache tout le
// reste sans le dire — c'est ce que ce type remplace.
type Scroll struct{ Cursor int }

// Clamp ramène le curseur dans [0, n).
func (s *Scroll) Clamp(n int) {
	if s.Cursor >= n {
		s.Cursor = n - 1
	}
	if s.Cursor < 0 {
		s.Cursor = 0
	}
}

// Move déplace le curseur de delta lignes.
func (s *Scroll) Move(delta, n int) {
	s.Cursor += delta
	s.Clamp(n)
}

// Key applique une touche de défilement et dit si elle en était une.
// `page` est le nombre de lignes visibles : une page ramène la ligne
// suivante en haut, sans en sauter.
func (s *Scroll) Key(key string, n, page int) bool {
	if page < 1 {
		page = 1
	}
	switch key {
	case "up", "K":
		s.Move(-1, n)
	case "down", "J":
		s.Move(1, n)
	case "pgup":
		s.Move(-page, n)
	case "pgdown":
		s.Move(page, n)
	case "home":
		s.Cursor = 0
		s.Clamp(n)
	case "end":
		s.Cursor = n - 1
		s.Clamp(n)
	default:
		return false
	}
	return true
}

// Wheel : trois lignes par cran de molette, comme un terminal.
func (s *Scroll) Wheel(up bool, n int) {
	if up {
		s.Move(-3, n)
	} else {
		s.Move(3, n)
	}
}

// Position résume la place du curseur : « 12/340 ». Vide sur une liste
// vide, pour ne pas afficher « 1/0 ».
func (s Scroll) Position(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", s.Cursor+1, n)
}
