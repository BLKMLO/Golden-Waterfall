package sterne

import (
	"context"
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Calibrage : RETENIR ou ÉCARTER, sur le seul jeu d'entraînement de la
// paire, chacune des fenêtres de la grille, en rejouant ses entrées comme
// le moteur de backtest les exécuterait.
//
// Ce n'est PAS une mesure de performance : le choix est fait in-sample.
// Seul le walk-forward — qui calibre chaque pli sur son passé et le juge
// sur son futur — dit si l'effet tient.

// windowResult : une fenêtre essayée et ce qu'elle a donné.
type windowResult struct {
	Window string `json:"window"`
	Trades int    `json:"trades"`
	// Score : t de Student de la moyenne des trades (moyenne / écart-type
	// × √n), en R nets du spread médian. Absent (null) sous minTrades.
	Score *float64 `json:"score"`
	MeanR float64  `json:"mean_r"`
	// WinRate : part des trades gagnants nets, en %.
	WinRate  float64 `json:"win_rate"`
	Selected bool    `json:"selected"`
}

// calibration : ce qui est archivé dans le manifeste.
type calibration struct {
	Criterion string         `json:"criterion"`
	Windows   []windowResult `json:"windows"`
	// CostsModelled : le spread médian a été facturé (côté ask présent).
	CostsModelled bool `json:"costs_modelled"`
}

// setups précalcule, pour chaque bougie décidable à partir de `from`, ce
// que verrait OnBar avec les fenêtres `active`.
func (r revision) setups(ctx context.Context, series core.Series, from, pair int, active []window) ([]setup, error) {
	out := make([]setup, len(series))
	if from < r.atrWindow-1 {
		from = r.atrWindow - 1
	}
	for i := from; i < len(series); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		out[i] = r.setupAt(series[i+1-r.atrWindow:i+1], pair, active)
	}
	return out, nil
}

// simTrade : un trade de la simulation.
type simTrade struct {
	side              int
	entryIdx, exitIdx int
	entry, exit, risk float64
	stop              float64
	reason            string
	deadline          time.Time
}

// simulateTrades rejoue les entrées comme le moteur de backtest : entrée
// au close de la bougie de décision ; stop testé sur les bougies
// SUIVANTES, rempli au pire du niveau et de l'ouverture (gap) ; barrière
// verticale (core.HoldExpired) ; clôture et aucune entrée sur la dernière
// bougie de la semaine ISO ; liquidation finale. Un test confronte ce
// rejeu au vrai moteur, trade par trade.
//
// Ce que la simulation ignore, et que le moteur applique : les refus du
// gestionnaire de risque (dimensionnement, marge, plafond de positions).
func (r revision) simulateTrades(series core.Series, setups []setup) []simTrade {
	var out []simTrade
	var cur simTrade
	open := false
	bar := r.barDuration()
	weekEnd := core.LastBarsOfWeek(series)
	last := len(series) - 1
	closeAt := func(i int, price float64, reason string) {
		cur.exitIdx, cur.exit, cur.reason = i, price, reason
		out = append(out, cur)
		open = false
	}
	for i, b := range series {
		if open && i > cur.entryIdx {
			switch {
			case cur.side > 0 && b.Low() <= cur.stop:
				closeAt(i, math.Min(cur.stop, b.Open()), "sl")
			case cur.side < 0 && b.High() >= cur.stop:
				closeAt(i, math.Max(cur.stop, b.Open()), "sl")
			}
		}
		if open && i > cur.entryIdx && core.HoldExpired(b.Time, bar, cur.deadline) {
			closeAt(i, b.Close(), "time")
		}
		if open && (weekEnd[i] || i == last) {
			reason := "weekend"
			if i == last {
				reason = "final"
			}
			closeAt(i, b.Close(), reason)
			continue // une bougie de clôture forcée ne rouvre rien
		}
		if open || weekEnd[i] || i == last || setups[i].side == 0 {
			continue
		}
		s := setups[i]
		cur = simTrade{side: s.side, entryIdx: i, entry: s.entry, risk: s.risk,
			stop: s.stop, deadline: core.HoldDeadline(b.Time, r.hold)}
		open = true
	}
	return out
}

// returnsR : résultat de chaque trade en R, net d'un spread par
// aller-retour (le moteur facture un demi-spread par côté).
func returnsR(trades []simTrade, spread float64) []float64 {
	out := make([]float64, len(trades))
	for k, t := range trades {
		out[k] = (float64(t.side)*(t.exit-t.entry) - spread) / t.risk
	}
	return out
}

// calibrate juge chaque fenêtre SEULE et renvoie celles qui sont retenues,
// dans l'ordre de la grille.
func (r revision) calibrate(ctx context.Context, series core.Series, pair int) ([]string, *calibration, error) {
	spread, costs := series.MedianSpread()
	if !costs {
		spread = 0
	}
	cal := &calibration{
		Criterion:     "t de Student de la moyenne des trades (R nets du spread médian), in-sample, fenêtre par fenêtre",
		CostsModelled: costs,
	}
	var selected []string
	for _, w := range r.windows {
		setups, err := r.setups(ctx, series, r.atrWindow-1, pair, []window{w})
		if err != nil {
			return nil, nil, err
		}
		res := returnsR(r.simulateTrades(series, setups), spread)
		wr := windowResult{Window: w.name, Trades: len(res)}
		if len(res) > 0 {
			wins := 0
			for _, x := range res {
				if x > 0 {
					wins++
				}
			}
			wr.WinRate = float64(wins) / float64(len(res)) * 100
			wr.MeanR, _ = meanStd(res)
		}
		if len(res) >= r.minTrades {
			mean, sd := meanStd(res)
			if sd > 0 {
				score := mean / sd * math.Sqrt(float64(len(res)))
				wr.Score = &score
				if score >= r.minScore {
					wr.Selected = true
					selected = append(selected, w.name)
				}
			}
		}
		cal.Windows = append(cal.Windows, wr)
	}
	return selected, cal, nil
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
