package view

import (
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// tradable : une entrée sur cette paire peut-elle partir avec cette
// configuration ?
//
// Avec le dimensionnement au risque, une paire dont la devise du compte
// n'est ni la base ni la cotation n'a pas de budget de risque calculable :
// TOUTES ses entrées sont refusées. Sur un compte en dollars, c'est le cas
// de 21 des 31 instruments par défaut. Les montrer sans distinction
// revenait à proposer, au même rang, des paires qui ne produiront jamais
// rien.
func tradable(cfg config.Config, symbol string) bool {
	if cfg.Risk.RiskPerTradePct <= 0 {
		return true
	}
	return data.ConversionFor(symbol, cfg.Backtest.AccountCurrency).Exact
}

// filterTradable garde les paires tradables, dans l'ordre.
func filterTradable(cfg config.Config, symbols []string) []string {
	out := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if tradable(cfg, s) {
			out = append(out, s)
		}
	}
	return out
}

// tradableLabel : ce que le filtre retient, dit en clair.
func tradableLabel(cfg config.Config) string {
	return "dimensionnables en " + cfg.Backtest.AccountCurrency
}
