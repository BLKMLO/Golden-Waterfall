package martinet

import (
	"context"
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Calibrage : choisir, SUR LE SEUL JEU D'ENTRAÎNEMENT, la cible (en
// multiples de R) et la force des pivots parmi une petite grille, en
// rejouant chaque balayage comme le moteur de backtest l'exécuterait.
//
// Ce n'est PAS une mesure de performance : le choix est fait in-sample,
// et seul le walk-forward — qui calibre chaque pli sur son passé et le
// juge sur son futur — dit s'il tient.

// gridPoint : un réglage essayé et ce qu'il a donné sur l'entraînement.
type gridPoint struct {
	RR    float64 `json:"rr"`
	Pivot int     `json:"pivot"`
	// VolumeMin : seuil de volume relatif (0 = sans filtre).
	VolumeMin float64 `json:"volume_min"`
	Trades    int     `json:"trades"`
	// Skipped : pourquoi le point n'a pas été essayé (« volume non
	// mesuré » : l'historique n'en a pas). Vide s'il l'a été.
	Skipped string `json:"skipped,omitempty"`
	// Score : t de Student de la moyenne des trades (moyenne / écart-type
	// × √n), en R nets du spread médian. Absent (null) quand le point n'a
	// pas assez de trades pour être jugé.
	Score *float64 `json:"score"`
	MeanR float64  `json:"mean_r"`
	// WinRate : part des trades gagnants nets, en %.
	WinRate float64 `json:"win_rate"`
}

// calibration : ce qui est archivé dans le manifeste.
type calibration struct {
	Criterion string      `json:"criterion"`
	Grid      []gridPoint `json:"grid"`
	// Fallback : aucun point n'avait assez de trades ; le réglage de
	// repli de la révision a été gardé, et c'est écrit.
	Fallback bool `json:"fallback"`
	// CostsModelled : le spread médian a été facturé (côté ask présent).
	CostsModelled bool `json:"costs_modelled"`
}

// setups précalcule, pour chaque bougie décidable à partir de `from`, ce
// que verrait OnBar avec des pivots de force `pivot` et le filtre de
// volume `volMin`. C'est le poste coûteux ; il ne dépend pas de la cible.
func (r revision) setups(ctx context.Context, series core.Series, from, pivot int, volMin float64) ([]setup, error) {
	out := make([]setup, len(series))
	if from < r.window-1 {
		from = r.window - 1
	}
	for i := from; i < len(series); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		out[i] = r.setupAt(series[i+1-r.window:i+1], pivot, volMin)
	}
	return out, nil
}

// simTrade : un trade de la simulation.
type simTrade struct {
	side              int
	entryIdx, exitIdx int
	entry, exit, risk float64
	stop, target      float64
	reason            string
	deadline          time.Time
}

// simulateTrades rejoue les balayages comme le moteur de backtest : entrée
// au close de la bougie de décision ; stop puis cible testés sur les
// bougies SUIVANTES, le stop d'abord quand les deux tombent dans la même
// bougie, rempli au pire du niveau et de l'ouverture (gap), la cible à
// son prix exact ; barrière verticale (core.HoldExpired) ; clôture et
// aucune entrée sur la dernière bougie de la semaine ISO ; liquidation
// finale. Un test confronte ce rejeu au vrai moteur, trade par trade.
//
// Ce que la simulation ignore, et que le moteur applique : le filtre
// d'actualités et les refus du gestionnaire de risque (dimensionnement,
// marge). Le calibrage choisit donc sur les balayages que la règle
// prendrait, pas sur ceux que le compte aurait pu prendre.
func (r revision) simulateTrades(series core.Series, setups []setup, rr float64, bar time.Duration) []simTrade {
	var out []simTrade
	var cur simTrade
	open := false
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
			case cur.side > 0 && b.High() >= cur.target:
				closeAt(i, cur.target, "tp")
			case cur.side < 0 && b.High() >= cur.stop:
				closeAt(i, math.Max(cur.stop, b.Open()), "sl")
			case cur.side < 0 && b.Low() <= cur.target:
				closeAt(i, cur.target, "tp")
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
			stop: s.stop, target: s.target(rr), deadline: core.HoldDeadline(b.Time, r.maxHold)}
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

// calibrate choisit la cible, la force des pivots et le filtre de volume
// sur le jeu d'entraînement. Un historique sans volume mesuré (FXCM,
// import sans colonne) ne permet d'essayer que les points sans filtre :
// les autres sont archivés « non essayés », avec la raison.
func (r revision) calibrate(ctx context.Context, series core.Series, bar time.Duration) (rr float64, pivot int, volMin float64, cal *calibration, err error) {
	spread, costs := series.MedianSpread()
	if !costs {
		spread = 0
	}
	cal = &calibration{
		Criterion:     "t de Student de la moyenne des trades (R nets du spread médian), in-sample",
		CostsModelled: costs,
		Fallback:      true,
	}
	rr, pivot = r.fallbackRR, r.fallbackPivot
	hasVolume := core.HasVolume(series)
	best := math.Inf(-1)
	for _, vm := range r.volGrid {
		for _, pv := range r.pivotGrid {
			p := int(pv)
			if vm > 0 && !hasVolume {
				for _, target := range r.rrGrid {
					cal.Grid = append(cal.Grid, gridPoint{RR: target, Pivot: p, VolumeMin: vm,
						Skipped: "volume non mesuré dans l'historique"})
				}
				continue
			}
			setups, err := r.setups(ctx, series, r.window-1, p, vm)
			if err != nil {
				return 0, 0, 0, nil, err
			}
			for _, target := range r.rrGrid {
				res := returnsR(r.simulateTrades(series, setups, target, bar), spread)
				gp := gridPoint{RR: target, Pivot: p, VolumeMin: vm, Trades: len(res)}
				if len(res) > 0 {
					wins := 0
					for _, x := range res {
						if x > 0 {
							wins++
						}
					}
					gp.WinRate = float64(wins) / float64(len(res)) * 100
				}
				if len(res) >= r.minCalibTrades {
					mean, sd := meanStd(res)
					gp.MeanR = mean
					if sd > 0 {
						score := mean / sd * math.Sqrt(float64(len(res)))
						gp.Score = &score
						// Égalité : le premier point de la grille l'emporte —
						// ordre fixe, résultat reproductible.
						if score > best {
							best, rr, pivot, volMin = score, target, p, vm
							cal.Fallback = false
						}
					}
				}
				cal.Grid = append(cal.Grid, gp)
			}
		}
	}
	return rr, pivot, volMin, cal, nil
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
