package training

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/news"
)

// Comparaison AVEC / SANS filtre d'actualités.
//
// Le filtre n'agit qu'à l'exécution : il ne change ni l'entraînement ni le
// modèle. Chaque pli rejoue donc son bloc out-of-sample DEUX fois, avec le
// MÊME modèle — une fois avec le filtre, une fois sans — et l'écart entre
// les deux ne peut venir que du filtre.
//
// Le filtre ne peut agir que pendant les semaines que le calendrier
// archivé couvre. L'écart est donc mesuré à part sur les trades dont la
// DÉCISION tombe dans une semaine couverte ; hors couverture, les deux
// branches ne diffèrent que par ricochet (une entrée refusée libère la
// place d'une autre plus tard). Sans semaine couverte dans les blocs de
// test, la comparaison ne mesure rien, et c'est dit.

// NewsSide : les trades d'une branche.
type NewsSide struct {
	Trades      int     `json:"trades"`
	Wins        int     `json:"wins"`
	NetPnL      float64 `json:"net_pnl"`
	GrossProfit float64 `json:"gross_profit"`
	GrossLoss   float64 `json:"gross_loss"`
}

// ProfitFactor : gains bruts / pertes brutes. NaN sans trade, +∞ sans
// perte.
func (s NewsSide) ProfitFactor() float64 {
	if s.Trades == 0 {
		return math.NaN()
	}
	if s.GrossLoss == 0 {
		return math.Inf(1)
	}
	return s.GrossProfit / math.Abs(s.GrossLoss)
}

func (s *NewsSide) addTrade(t core.Trade) {
	s.Trades++
	s.NetPnL += t.PnL
	if t.IsWin() {
		s.Wins++
		s.GrossProfit += t.PnL
	} else {
		s.GrossLoss += t.PnL
	}
}

func (s *NewsSide) add(o NewsSide) {
	s.Trades += o.Trades
	s.Wins += o.Wins
	s.NetPnL += o.NetPnL
	s.GrossProfit += o.GrossProfit
	s.GrossLoss += o.GrossLoss
}

// NewsWindow : les deux branches sur une même période.
type NewsWindow struct {
	With    NewsSide `json:"with_filter"`
	Without NewsSide `json:"without_filter"`
}

// Delta : P&L net avec filtre moins P&L net sans filtre. Positif = le
// filtre a rapporté.
func (w NewsWindow) Delta() float64 { return w.With.NetPnL - w.Without.NetPnL }

func (w *NewsWindow) add(o NewsWindow) {
	w.With.add(o.With)
	w.Without.add(o.Without)
}

// NewsComparison : la comparaison d'un pli, ou de tout le walk-forward.
type NewsComparison struct {
	// Covered : trades dont la décision tombe dans une semaine couverte
	// par le calendrier — là seulement le filtre peut agir.
	Covered NewsWindow `json:"covered"`
	// Total : tous les trades out-of-sample.
	Total NewsWindow `json:"total"`
	// Blocked : entrées écartées par une annonce (branche avec filtre).
	// Uncovered : entrées décidées hors de toute semaine couverte.
	Blocked   int `json:"blocked"`
	Uncovered int `json:"uncovered"`
	// CoveredWeeks : semaines archivées au moment du run.
	CoveredWeeks int `json:"covered_weeks"`
	// CurrencyExact : false si des P&L de devises différentes sont
	// additionnés (paires non convertibles vers la devise du compte).
	CurrencyExact bool `json:"currency_exact"`
}

// Measured : la période couverte contient-elle au moins une décision,
// dans l'une ou l'autre branche ? Sinon l'écart ne mesure rien.
func (c NewsComparison) Measured() bool {
	return c.Covered.With.Trades+c.Covered.Without.Trades+c.Blocked > 0
}

func (c *NewsComparison) add(o NewsComparison) {
	c.Covered.add(o.Covered)
	c.Total.add(o.Total)
	c.Blocked += o.Blocked
	c.Uncovered += o.Uncovered
	c.CurrencyExact = c.CurrencyExact && o.CurrencyExact
	if o.CoveredWeeks > c.CoveredWeeks {
		c.CoveredWeeks = o.CoveredWeeks
	}
}

// compareNews range les trades des deux branches. `bar` : durée d'une
// bougie — la décision d'un trade tombe au CLOSE de sa bougie d'entrée,
// l'instant même que le filtre juge (backtest.Engine).
func compareNews(with, without []*backtest.Result, cal *news.Calendar, bar time.Duration) NewsComparison {
	c := NewsComparison{CoveredWeeks: cal.Weeks(), CurrencyExact: true}
	side := func(results []*backtest.Result, total, covered *NewsSide) {
		for _, r := range results {
			if r == nil {
				continue
			}
			if !r.Stats.CurrencyExact {
				c.CurrencyExact = false
			}
			for _, t := range r.Trades {
				total.addTrade(t)
				if cal.Covers(t.EntryTime.Add(bar)) {
					covered.addTrade(t)
				}
			}
		}
	}
	side(with, &c.Total.With, &c.Covered.With)
	side(without, &c.Total.Without, &c.Covered.Without)
	for _, r := range with {
		if r != nil {
			c.Blocked += r.Stats.NewsBlocked
			c.Uncovered += r.Stats.NewsUncovered
		}
	}
	return c
}
