package troglodyte

import (
	"context"
	"math"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Calibrage de troglodyte_v1_1 : choisir, SUR LE SEUL JEU
// D'ENTRAÎNEMENT, le seuil d'entrée s_in et la distance du stop k parmi
// une petite grille, en rejouant la règle de décision (decide) comme le
// moteur l'exécuterait.
//
// Ce n'est PAS une mesure de performance : le choix est fait in-sample,
// et seul le walk-forward — qui calibre chaque pli sur son passé et le
// juge sur son futur — dit s'il tient. La grille est volontairement
// petite (4 × 3 points) : plus on essaie de réglages, plus le meilleur
// d'entre eux doit sa place au hasard.

// gridPoint : un réglage essayé et ce qu'il a donné sur l'entraînement.
type gridPoint struct {
	EnterZ  float64 `json:"enter_z"`
	StopATR float64 `json:"stop_atr"`
	Trades  int     `json:"trades"`
	// Score : t de Student de la moyenne des trades (moyenne / écart-type
	// × √n), en rendement logarithmique net du spread médian. Absent
	// (null) quand le point n'a pas assez de trades pour être jugé.
	Score *float64 `json:"score"`
	MeanR float64  `json:"mean_log_return"`
}

// calibration : ce qui est archivé dans le manifeste.
type calibration struct {
	Criterion string      `json:"criterion"`
	Grid      []gridPoint `json:"grid"`
	// Fallback : aucun point n'avait assez de trades ; les valeurs de
	// repli de la révision ont été gardées, et c'est écrit.
	Fallback bool `json:"fallback"`
	// CostsModelled : le spread médian a été facturé (côté ask présent).
	CostsModelled bool `json:"costs_modelled"`
}

// decisionInputs précalcule, pour chaque bougie décidable du jeu
// d'entraînement, l'état que verrait OnBar. C'est le poste coûteux
// (une fenêtre refiltrée par bougie) ; il ne dépend pas de la grille.
func (t *troglodyte) decisionInputs(ctx context.Context, series core.Series, p params) ([]barState, bool, error) {
	r := t.rev
	states := make([]barState, len(series))
	usable := false
	for i := range series {
		states[i].z = math.NaN()
		if i+1 < r.window {
			continue
		}
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		st, ok := t.state(series, i, p)
		if ok {
			states[i] = st
			usable = true
		}
	}
	return states, usable, nil
}

// simTrade : un trade de la simulation.
type simTrade struct {
	side              int // +1 long, −1 court
	entryIdx, exitIdx int
	entry, exit       float64
	stopLevel         float64
}

// simulateTrades rejoue la règle `ru` sur la série, comme le moteur de
// backtest : entrée au close, stop initial servi au pire du niveau et de
// l'ouverture (gap), sorties sur signal au close, retournement sans
// réouverture sur la même bougie, aucune entrée sur la dernière bougie,
// liquidation finale. Un test confronte ce rejeu au vrai moteur, trade par
// trade.
func simulateTrades(series core.Series, states []barState, ru rule) []simTrade {
	var out []simTrade
	var cur simTrade
	open := false
	closeAt := func(i int, price float64) {
		cur.exitIdx, cur.exit = i, price
		out = append(out, cur)
		open = false
	}
	for i, bar := range series {
		if open && i > cur.entryIdx {
			switch {
			case cur.side > 0 && bar.Low() <= cur.stopLevel:
				closeAt(i, math.Min(cur.stopLevel, bar.Open()))
			case cur.side < 0 && bar.High() >= cur.stopLevel:
				closeAt(i, math.Max(cur.stopLevel, bar.Open()))
			}
		}
		st := states[i]
		if math.IsNaN(st.z) || i == len(series)-1 {
			continue
		}
		action, level := decide(st, ru)
		if open {
			q := float64(cur.side)
			if action.Closes(q) || cur.side > 0 && action == core.EnterShort || cur.side < 0 && action == core.EnterLong {
				closeAt(i, bar.Close())
			}
			continue
		}
		if action.IsEntry() {
			cur = simTrade{side: 1, entryIdx: i, entry: bar.Close(), stopLevel: level}
			if action == core.EnterShort {
				cur.side = -1
			}
			open = true
		}
	}
	if open {
		closeAt(len(series)-1, series[len(series)-1].Close())
	}
	return out
}

// logReturns : rendement logarithmique net de chaque trade.
func logReturns(trades []simTrade, cost func(i int) float64) []float64 {
	out := make([]float64, len(trades))
	for k, t := range trades {
		out[k] = float64(t.side)*math.Log(t.exit/t.entry) - cost(t.entryIdx) - cost(t.exitIdx)
	}
	return out
}

// calibrate choisit la règle sur le jeu d'entraînement.
func (t *troglodyte) calibrate(ctx context.Context, series core.Series, p params) (rule, *calibration, error) {
	r := t.rev
	fallback := rule{enterZ: r.enterZ, exitZ: r.exitZ, stopATR: r.stopATR, trail: r.trailBars > 0}
	states, usable, err := t.decisionInputs(ctx, series, p)
	if err != nil {
		return fallback, nil, err
	}
	// Coût d'un côté = demi-spread médian rapporté au prix : un
	// aller-retour paie un spread, comme au moteur de backtest.
	spread, costs := series.MedianSpread()
	cost := func(i int) float64 {
		if !costs {
			return 0
		}
		return spread / 2 / series[i].Close()
	}
	cal := &calibration{
		Criterion:     "t de Student de la moyenne des trades (log-rendements nets du spread médian), in-sample",
		CostsModelled: costs,
		Fallback:      true,
	}
	best, bestScore := fallback, math.Inf(-1)
	for _, enter := range r.enterGrid {
		for _, stopATR := range r.stopGrid {
			ru := rule{enterZ: enter, exitZ: enter * r.exitRatio, stopATR: stopATR, trail: r.trailBars > 0}
			var trades []float64
			if usable {
				trades = logReturns(simulateTrades(series, states, ru), cost)
			}
			gp := gridPoint{EnterZ: enter, StopATR: stopATR, Trades: len(trades)}
			if len(trades) >= r.minCalibTrades {
				mean, sd := meanStd(trades)
				gp.MeanR = mean
				if sd > 0 {
					score := mean / sd * math.Sqrt(float64(len(trades)))
					gp.Score = &score
					// Égalité : le premier point de la grille l'emporte —
					// ordre fixe, résultat reproductible.
					if score > bestScore {
						best, bestScore = ru, score
						cal.Fallback = false
					}
				}
			}
			cal.Grid = append(cal.Grid, gp)
		}
	}
	return best, cal, nil
}

func meanStd(x []float64) (mean, sd float64) {
	for _, v := range x {
		mean += v
	}
	mean /= float64(len(x))
	if len(x) < 2 {
		return mean, 0
	}
	for _, v := range x {
		sd += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(sd / float64(len(x)-1))
}
