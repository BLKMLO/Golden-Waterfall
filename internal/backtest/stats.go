package backtest

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// computeStats agrège les métriques d'un backtest terminé.
//
// Toutes les métriques de RATIO (taux de gain, profit factor) sont
// recalculées sur les trades BRUTS. C'est une règle du projet : on ne fait
// jamais la moyenne de ratios déjà agrégés — moyenner des taux de gain de
// blocs de tailles différentes donne un chiffre qui ne correspond à aucun
// portefeuille réel.
func computeStats(req Request, trades []core.Trade, equity []EquityPoint,
	initialCapital, costs, spread float64, costsModelled bool,
	rejected int, exitReasons map[string]int, rejections map[string]int) Stats {

	s := Stats{
		Symbol:         req.Symbol,
		Bars:           len(equity),
		Trades:         len(trades),
		Costs:          costs,
		CostsModelled:  costsModelled,
		Spread:         spread,
		InitialCapital: initialCapital,
		RejectedOrders: rejected,
		ExitReasons:    exitReasons,
		Rejections:     rejections,
	}
	if len(equity) > 0 {
		s.Start = equity[0].Time
		s.End = equity[len(equity)-1].Time
		s.FinalEquity = equity[len(equity)-1].Value
		s.ReturnPct = (s.FinalEquity/initialCapital - 1) * 100
		s.MaxDrawdownPct = maxDrawdownPct(equity)
		s.Sharpe = sharpe(equity)
	} else {
		s.FinalEquity = initialCapital
	}

	pnls := make([]float64, 0, len(trades))
	for _, t := range trades {
		pnls = append(pnls, t.PnL)
		s.NetPnL += t.PnL
		if t.PnL > 0 {
			s.Wins++
			s.GrossProfit += t.PnL
		} else {
			s.Losses++
			s.GrossLoss += -t.PnL
		}
	}
	if s.Trades > 0 {
		s.WinRate = float64(s.Wins) / float64(s.Trades) * 100
		s.Expectancy = s.NetPnL / float64(s.Trades)
	}
	if s.Wins > 0 {
		s.AvgWin = s.GrossProfit / float64(s.Wins)
	}
	if s.Losses > 0 {
		s.AvgLoss = s.GrossLoss / float64(s.Losses)
	}
	switch {
	case s.GrossLoss > 0:
		s.ProfitFactor = s.GrossProfit / s.GrossLoss
	case s.GrossProfit > 0:
		// Aucune perte du tout : le profit factor est INFINI, pas énorme.
		// L'interface affiche « ∞ » plutôt qu'un nombre qui ferait croire
		// à une mesure.
		s.ProfitFactor = math.Inf(1)
	default:
		s.ProfitFactor = math.NaN()
	}
	s.SQN = sqn(pnls)
	return s
}

// maxDrawdownPct : plus forte baisse depuis un sommet, en % du sommet.
func maxDrawdownPct(equity []EquityPoint) float64 {
	peak := math.Inf(-1)
	worst := 0.0
	for _, p := range equity {
		if p.Value > peak {
			peak = p.Value
		}
		if peak > 0 {
			dd := (peak - p.Value) / peak * 100
			if dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

// sharpe : ratio de Sharpe des rendements par bougie, ANNUALISÉ.
//
// Le facteur d'annualisation est déduit de la cadence RÉELLE des bougies
// (nombre de bougies rapporté au temps écoulé) plutôt que d'une constante
// « 252 » ou « 6 par jour » : sur du forex, les bougies manquent le
// week-end et les jours fériés, et une constante surestimerait le ratio.
//
// Taux sans risque supposé NUL — c'est une hypothèse, et elle est dite.
func sharpe(equity []EquityPoint) float64 {
	if len(equity) < 3 {
		return math.NaN()
	}
	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		prev := equity[i-1].Value
		if prev == 0 {
			continue
		}
		returns = append(returns, equity[i].Value/prev-1)
	}
	if len(returns) < 2 {
		return math.NaN()
	}
	mean, std := meanStd(returns)
	if std == 0 {
		return math.NaN()
	}
	elapsed := equity[len(equity)-1].Time.Sub(equity[0].Time)
	if elapsed <= 0 {
		return math.NaN()
	}
	barsPerYear := float64(len(returns)) / (elapsed.Hours() / (365.25 * 24))
	return mean / std * math.Sqrt(barsPerYear)
}

// sqn (System Quality Number, Van Tharp) : qualité d'un système rapportée
// à sa dispersion, pondérée par le nombre de trades.
func sqn(pnls []float64) float64 {
	if len(pnls) < 2 {
		return math.NaN()
	}
	mean, std := meanStd(pnls)
	if std == 0 {
		return math.NaN()
	}
	return math.Sqrt(float64(len(pnls))) * mean / std
}

func meanStd(x []float64) (mean, std float64) {
	n := float64(len(x))
	for _, v := range x {
		mean += v
	}
	mean /= n
	var variance float64
	for _, v := range x {
		d := v - mean
		variance += d * d
	}
	variance /= n - 1
	if variance < 0 {
		variance = 0
	}
	return mean, math.Sqrt(variance)
}

// AggregateStats fusionne des résultats de plusieurs actifs ou plusieurs
// plis en un agrégat HONNÊTE : les ratios sont recalculés sur l'ensemble
// des trades, les montants sont sommés.
func AggregateStats(results []*Result, initialCapital float64) Stats {
	agg := Stats{Symbol: "AGRÉGAT", InitialCapital: initialCapital,
		CostsModelled: true, CurrencyExact: true}
	var allPnL []float64
	var trades []core.Trade
	exitReasons := map[string]int{}
	rejections := map[string]int{}
	for _, r := range results {
		if r == nil {
			continue
		}
		trades = append(trades, r.Trades...)
		agg.Bars += r.Stats.Bars
		agg.Costs += r.Stats.Costs
		agg.RejectedOrders += r.Stats.RejectedOrders
		if !r.Stats.CostsModelled {
			// Un seul actif sans spread mesurable suffit à rendre
			// l'agrégat incomplet : on le dit pour l'ensemble.
			agg.CostsModelled = false
		}
		// Même exigence pour la devise : additionner des montants nés dans
		// des devises différentes n'a de sens que si la conversion était
		// exacte partout. Un seul actif non convertible, ou une devise
		// divergente, rend la somme approximative — et on le SIGNALE.
		if !r.Stats.CurrencyExact {
			agg.CurrencyExact = false
		}
		if agg.Currency == "" {
			agg.Currency = r.Stats.Currency
		} else if r.Stats.Currency != "" && agg.Currency != r.Stats.Currency {
			agg.CurrencyExact = false
		}
		for k, v := range r.Stats.ExitReasons {
			exitReasons[k] += v
		}
		for k, v := range r.Stats.Rejections {
			rejections[k] += v
		}
		if agg.Start.IsZero() || (!r.Stats.Start.IsZero() && r.Stats.Start.Before(agg.Start)) {
			agg.Start = r.Stats.Start
		}
		if r.Stats.End.After(agg.End) {
			agg.End = r.Stats.End
		}
	}
	for _, t := range trades {
		allPnL = append(allPnL, t.PnL)
		agg.NetPnL += t.PnL
		if t.PnL > 0 {
			agg.Wins++
			agg.GrossProfit += t.PnL
		} else {
			agg.Losses++
			agg.GrossLoss += -t.PnL
		}
	}
	agg.Trades = len(trades)
	if agg.Trades > 0 {
		agg.WinRate = float64(agg.Wins) / float64(agg.Trades) * 100
		agg.Expectancy = agg.NetPnL / float64(agg.Trades)
	}
	if agg.Wins > 0 {
		agg.AvgWin = agg.GrossProfit / float64(agg.Wins)
	}
	if agg.Losses > 0 {
		agg.AvgLoss = agg.GrossLoss / float64(agg.Losses)
	}
	switch {
	case agg.GrossLoss > 0:
		agg.ProfitFactor = agg.GrossProfit / agg.GrossLoss
	case agg.GrossProfit > 0:
		agg.ProfitFactor = math.Inf(1)
	default:
		agg.ProfitFactor = math.NaN()
	}
	agg.SQN = sqn(allPnL)
	agg.FinalEquity = initialCapital + agg.NetPnL
	agg.ReturnPct = (agg.FinalEquity/initialCapital - 1) * 100
	agg.ExitReasons = exitReasons
	agg.Rejections = rejections
	return agg
}

// MergeEquity fusionne les courbes de plusieurs actifs sur une timeline
// commune : à chaque instant, la valeur du compte est le capital initial
// plus la somme des P&L cumulés de chaque actif.
//
// Simplification ASSUMÉE : chaque actif est backtesté avec son propre
// moteur, donc les limites du risque s'appliquent par actif et
// l'exposition croisée n'est pas plafonnée. C'est dit plutôt que masqué.
func MergeEquity(results []*Result, initialCapital float64, max int) []EquityPoint {
	type cursor struct {
		points []EquityPoint
		i      int
		last   float64
	}
	cursors := make([]*cursor, 0, len(results))
	var times []time.Time
	seen := map[int64]struct{}{}
	for _, r := range results {
		if r == nil || len(r.Equity) == 0 {
			continue
		}
		cursors = append(cursors, &cursor{points: r.Equity})
		for _, p := range r.Equity {
			k := p.Time.UnixNano()
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				times = append(times, p.Time)
			}
		}
	}
	if len(cursors) == 0 {
		return nil
	}
	sortTimes(times)
	out := make([]EquityPoint, 0, len(times))
	for _, t := range times {
		total := initialCapital
		for _, c := range cursors {
			for c.i < len(c.points) && !c.points[c.i].Time.After(t) {
				// P&L cumulé de l'actif = valeur - capital initial.
				c.last = c.points[c.i].Value - initialCapital
				c.i++
			}
			total += c.last
		}
		out = append(out, EquityPoint{Time: t, Value: total})
	}
	return Downsample(out, max)
}
