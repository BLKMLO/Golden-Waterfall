package strategies

import (
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
