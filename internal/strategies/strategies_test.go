package strategies

import (
	"strings"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy/strategytest"
)

// TestEveryCatalogedStrategyHonoursTheContract : toute stratégie du
// catalogue passe le banc de conformité. Une génération ajoutée ici y est
// soumise d'office.
func TestEveryCatalogedStrategyHonoursTheContract(t *testing.T) {
	names := strategy.List()
	if len(names) == 0 {
		t.Fatal("catalogue vide : aucune stratégie enregistrée")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) { strategytest.Run(t, name) })
	}
}

// TestColibriNeverUsesTheNews : règle du propriétaire du projet — Colibri
// n'a pas droit à internet. Aucune de ses révisions ne déclare le filtre
// d'actualités ; une révision future qui le ferait casse ce test.
func TestColibriNeverUsesTheNews(t *testing.T) {
	found := 0
	for _, name := range strategy.List() {
		if !strings.HasPrefix(name, "colibri_") {
			continue
		}
		found++
		s, err := strategy.New(name)
		if err != nil {
			t.Fatal(err)
		}
		if s.Describe().UsesNews {
			t.Errorf("%s déclare le filtre d'actualités : Colibri n'a pas droit à internet", name)
		}
	}
	if found == 0 {
		t.Fatal("aucune révision Colibri au catalogue : le test ne vérifie plus rien")
	}
}
