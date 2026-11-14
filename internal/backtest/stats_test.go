package backtest

import (
	"math"
	"testing"
)

// TestCostCoverIsGrossOverCosts : couverture = (net + coûts) ÷ coûts, et
// rien quand le rapport ne veut rien dire.
func TestCostCoverIsGrossOverCosts(t *testing.T) {
	if c, ok := (Stats{Trades: 5, NetPnL: 50, Costs: 100, CostsModelled: true}).CostCover(); !ok || c != 1.5 {
		t.Fatalf("(50 + 100) ÷ 100 = 1,5 attendu : %v, %v", c, ok)
	}
	for name, s := range map[string]Stats{
		"aucun coût modélisé":     {Trades: 5, NetPnL: 50, Costs: 100},
		"aucun trade":             {NetPnL: 50, Costs: 100, CostsModelled: true},
		"coûts nuls":              {Trades: 5, NetPnL: 50, CostsModelled: true},
		"avant coûts non positif": {Trades: 5, NetPnL: -120, Costs: 100, CostsModelled: true},
		"coûts NaN":               {Trades: 5, NetPnL: 50, Costs: math.NaN(), CostsModelled: true},
	} {
		if c, ok := s.CostCover(); ok {
			t.Errorf("%s : aucune couverture attendue, reçu %v", name, c)
		}
	}
}
