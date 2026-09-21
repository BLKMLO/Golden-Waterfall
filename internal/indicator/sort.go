package indicator

import "sort"

// quickSelectSort trie en place. Isolé dans son propre fichier pour que
// Median reste lisible ; sort.Float64s suffit largement aux tailles en jeu.
func quickSelectSort(x []float64) { sort.Float64s(x) }
