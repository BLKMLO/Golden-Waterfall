package label

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Side : l'issue, bougie par bougie, d'un trade ouvert dans UN sens au
// close de t — telle que le moteur d'exécution la produirait.
type Side struct {
	// Value vaut 1 si le trade finit GAGNANT NET de coûts, 0 sinon, NaN
	// quand l'issue n'est pas connue (ATR manquant, fenêtre incomplète,
	// entrée interdite).
	Value []float64
	// GrossR : résultat BRUT du trade en unités de barrière (± 1 = une
	// barrière pleine). Un stop rempli dans un gap vaut moins que −1.
	GrossR []float64
	// Exit : indice de la bougie où le trade se termine, −1 si indéfini.
	// Sert à PURGER un jeu d'entraînement et à mesurer le chevauchement
	// des labels.
	Exit []int
	// Which : barrière qui a mis fin au trade.
	Which []Barrier
}

// Defined indique si la bougie porte une issue exploitable.
func (s Side) Defined(i int) bool { return !math.IsNaN(s.Value[i]) }

// SidedLabels : issues des deux sens, alignées sur la série.
type SidedLabels struct {
	Long  Side
	Short Side
}

// Sided étiquette chaque bougie par l'issue d'un LONG et d'un SHORT
// ouverts à son close, barrières à ± atrMult × atr[t], trade clos au plus
// tard au close de ends[t], coût aller-retour cost[t] en unités de prix.
//
// C'est la règle du moteur de backtest, rejouée à l'identique :
//
//   - barrières testées sur les bougies SUIVANT l'entrée ;
//   - le STOP d'abord quand les deux tombent dans la même bougie — pour un
//     long comme pour un short. Le label symétrique historique donnait ce
//     cas au short (label 0 = « la basse d'abord ») alors que l'exécution
//     l'y compte perdant : le short apprenait des gains que personne ne lui
//     verserait ;
//   - un stop franchi par un GAP est rempli à l'ouverture ; la limite
//     garde son prix exact ;
//   - sans barrière touchée, sortie au close de ends[t] ;
//   - le coût est retiré AVANT de juger « gagnant » : un trade qui touche
//     sa limite mais rend moins que le spread est une perte, comme dans
//     les statistiques du backtest (Trade.IsWin est NET).
//
// Une bougie sans fenêtre (ends[t] ≤ t, par exemple la dernière de sa
// semaine, où le moteur n'ouvre rien) reste NaN.
func Sided(series core.Series, atr []float64, atrMult float64, ends []int, cost []float64) SidedLabels {
	n := len(series)
	out := SidedLabels{Long: newSide(n), Short: newSide(n)}
	if n == 0 {
		return out
	}
	open := series.Opens()
	high := series.Highs()
	low := series.Lows()
	closes := series.Closes()

	for t := 0; t < n; t++ {
		a := atr[t]
		end := ends[t]
		if math.IsNaN(a) || a <= 0 || end <= t || end >= n {
			continue
		}
		d := atrMult * a
		c := 0.0
		if t < len(cost) && !math.IsNaN(cost[t]) && cost[t] > 0 {
			c = cost[t]
		}
		entry := closes[t]
		upper, lower := entry+d, entry-d

		// LONG : stop = basse, limite = haute.
		longR, longExit, longWhich := math.NaN(), end, BarrierTime
		for j := t + 1; j <= end; j++ {
			if low[j] <= lower {
				longR, longExit, longWhich = (math.Min(lower, open[j])-entry)/d, j, BarrierSL
				break
			}
			if high[j] >= upper {
				longR, longExit, longWhich = 1, j, BarrierTP
				break
			}
		}
		if longWhich == BarrierTime {
			longR = (closes[end] - entry) / d
		}
		out.Long.set(t, longR, c/d, longExit, longWhich)

		// SHORT : stop = haute, limite = basse.
		shortR, shortExit, shortWhich := math.NaN(), end, BarrierTime
		for j := t + 1; j <= end; j++ {
			if high[j] >= upper {
				shortR, shortExit, shortWhich = (entry-math.Max(upper, open[j]))/d, j, BarrierSL
				break
			}
			if low[j] <= lower {
				shortR, shortExit, shortWhich = 1, j, BarrierTP
				break
			}
		}
		if shortWhich == BarrierTime {
			shortR = (entry - closes[end]) / d
		}
		out.Short.set(t, shortR, c/d, shortExit, shortWhich)
	}
	return out
}

func newSide(n int) Side {
	s := Side{
		Value:  make([]float64, n),
		GrossR: make([]float64, n),
		Exit:   make([]int, n),
		Which:  make([]Barrier, n),
	}
	for i := 0; i < n; i++ {
		s.Value[i], s.GrossR[i], s.Exit[i] = math.NaN(), math.NaN(), -1
	}
	return s
}

func (s *Side) set(t int, grossR, costR float64, exit int, which Barrier) {
	s.GrossR[t], s.Exit[t], s.Which[t] = grossR, exit, which
	if grossR-costR > 0 {
		s.Value[t] = 1
	} else {
		s.Value[t] = 0
	}
}

// ExecutionWindow renvoie, pour chaque bougie t, l'indice de la bougie au
// close de laquelle le moteur d'exécution liquiderait une position ouverte
// en t si aucune barrière horizontale n'est touchée : la PREMIÈRE, après
// t, qui soit la dernière de sa semaine ISO (core.LastBarsOfWeek) ou dont
// la barrière verticale a expiré (core.HoldExpired, horizon maxHold,
// cadence barDuration). −1 quand le moteur n'ouvrirait rien en t (dernière
// bougie de la semaine) ou quand la fenêtre dépasse la série.
//
// Les deux règles viennent de `core`, celles que le moteur applique : la
// cible et l'exécution ne peuvent pas désigner deux bougies différentes.
func ExecutionWindow(series core.Series, barDuration, maxHold time.Duration) []int {
	n := len(series)
	ends := make([]int, n)
	weekEnd := core.LastBarsOfWeek(series)
	// nextWeekEnd[t] : première bougie j ≥ t marquée fin de semaine.
	nextWeekEnd := make([]int, n+1)
	nextWeekEnd[n] = -1
	for j := n - 1; j >= 0; j-- {
		if weekEnd[j] {
			nextWeekEnd[j] = j
		} else {
			nextWeekEnd[j] = nextWeekEnd[j+1]
		}
	}
	cursor := 0
	for t := 0; t < n; t++ {
		ends[t] = -1
		if weekEnd[t] || t+1 >= n {
			continue
		}
		end := nextWeekEnd[t+1]
		if maxHold > 0 {
			deadline := core.HoldDeadline(series[t].Time, maxHold)
			if cursor < t+1 {
				cursor = t + 1
			}
			// Premier j > t dont la barrière verticale a expiré. Le curseur
			// ne recule jamais : les échéances croissent avec t.
			for cursor < n && !core.HoldExpired(series[cursor].Time, barDuration, deadline) {
				cursor++
			}
			if cursor < n && (end < 0 || cursor < end) {
				end = cursor
			}
		}
		ends[t] = end
	}
	return ends
}

// AverageUniqueness renvoie, pour chaque label, son UNICITÉ moyenne : la
// moyenne, sur les bougies (t, exit[t]] qu'il couvre, de 1 / (nombre de
// labels couvrant cette bougie). Un label isolé vaut 1 ; dix labels qui
// racontent la même hausse valent chacun environ 0,1.
//
// Pourquoi : des labels qui se chevauchent ne sont pas des observations
// indépendantes. Les compter chacun pour un fait peser dix fois la même
// information, et un arbre croit tenir une régularité là où il n'a vu
// qu'un seul épisode. Les labels indéfinis (exit ≤ t) valent NaN.
//
// Coût linéaire : tableau de différences pour le nombre de labels actifs,
// puis sommes cumulées de leur inverse.
func AverageUniqueness(exit []int) []float64 {
	n := len(exit)
	out := make([]float64, n)
	diff := make([]int, n+1)
	for t, e := range exit {
		if e <= t || e >= n {
			continue
		}
		diff[t+1]++
		diff[e+1]--
	}
	inv := make([]float64, n+1) // inv[k] = Σ_{j<k} 1/c_j
	active := 0
	for j := 0; j < n; j++ {
		active += diff[j]
		v := 0.0
		if active > 0 {
			v = 1 / float64(active)
		}
		inv[j+1] = inv[j] + v
	}
	for t, e := range exit {
		if e <= t || e >= n {
			out[t] = math.NaN()
			continue
		}
		out[t] = (inv[e+1] - inv[t+1]) / float64(e-t)
	}
	return out
}
