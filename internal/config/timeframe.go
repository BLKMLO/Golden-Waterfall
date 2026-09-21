package config

import (
	"fmt"
	"strings"
)

// knownTimeframes duplique VOLONTAIREMENT la liste du paquet data.
//
// Pourquoi ne pas importer data : la configuration est le socle, elle est
// chargée avant tout le reste et ne doit dépendre d'aucun module métier.
// La liste est courte et figée ; un test croisé (data.TestTimeframesMatchConfig)
// garantit que les deux ne divergent pas.
var knownTimeframes = []string{"M1", "M5", "M15", "M30", "H1", "H4", "D1", "W1", "MN1"}

func parseTimeframeName(s string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(s))
	for _, tf := range knownTimeframes {
		if tf == up {
			return tf, nil
		}
	}
	return "", fmt.Errorf("unité de temps inconnue %q (%s)", s, strings.Join(knownTimeframes, ", "))
}

// KnownTimeframes expose la liste pour les vérifications croisées.
func KnownTimeframes() []string { return append([]string(nil), knownTimeframes...) }
