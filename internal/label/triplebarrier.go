// Package label produit des CIBLES d'apprentissage par la méthode des
// barrières. C'est une bibliothèque : elle ne porte aucune constante de
// définition. Chaque révision de stratégie fournit ses propres barrières,
// son horizon et son ATR — les mêmes qu'elle enverra à l'exécution.
//
// Deux étiquetages cohabitent :
//
//   - TripleBarrier : label SYMÉTRIQUE historique (1 si la barrière haute
//     est touchée d'abord, 0 sinon), celui de colibri_v1_0 et v1_1. Il est
//     figé : le modifier changerait des modèles publiés.
//   - Sided : un label PAR CÔTÉ (long, short), net de coûts, qui rejoue la
//     règle d'exécution du moteur de backtest bougie pour bougie.
//
// ⚠ Un label REGARDE VERS L'AVANT — c'est sa nature, c'est la cible. Il
// n'est défini que pour les bougies disposant d'une fenêtre avant
// COMPLÈTE : une bougie dont l'horizon dépasse la fin de la série reçoit
// NaN, jamais un label calculé sur une fenêtre tronquée. Les features,
// elles, restent strictement causales.
package label

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Barrier nomme la barrière touchée en premier (diagnostic).
type Barrier string

const (
	BarrierNone Barrier = ""
	BarrierTP   Barrier = "tp"
	BarrierSL   Barrier = "sl"
	BarrierTime Barrier = "time"
)

// Labels est le résultat de l'étiquetage symétrique, aligné sur la série.
type Labels struct {
	// Value vaut 1, 0, ou NaN quand le label est indéfini (ATR manquant,
	// fenêtre avant incomplète).
	Value []float64
	// Which indique la barrière retenue, pour le diagnostic.
	Which []Barrier
	// ATR au moment t, tel que fourni par l'appelant.
	ATR []float64
}

// Defined indique si la bougie porte un label exploitable.
func (l Labels) Defined(i int) bool { return !math.IsNaN(l.Value[i]) }

// TripleBarrier étiquette chaque bougie par la première barrière touchée,
// barrières à ± atrMult × atr[t] du close, horizon calendaire `horizon`.
//
// Départage quand les DEUX barrières tombent dans la MÊME bougie : on
// retient la BASSE (label 0). C'est la convention du moteur d'exécution
// pour un LONG, qui suppose le stop touché d'abord faute de connaître
// l'ordre intrabar réel.
//
// Si l'horizon expire sans barrière touchée, on étiquette par le signe du
// retour sur l'horizon.
func TripleBarrier(series core.Series, atr []float64, atrMult float64, horizon time.Duration) Labels {
	n := len(series)
	out := Labels{
		Value: make([]float64, n),
		Which: make([]Barrier, n),
		ATR:   atr,
	}
	for i := range out.Value {
		out.Value[i] = math.NaN()
	}
	if n == 0 {
		return out
	}

	high := series.Highs()
	low := series.Lows()
	closes := series.Closes()
	times := series.Times()
	last := times[n-1]

	// Fin de fenêtre (barrière verticale) : pour chaque t, la dernière
	// bougie dans (t, t+horizon]. Calculée par un curseur glissant — les
	// bornes sont croissantes, une dichotomie par bougie serait du gaspillage.
	ends := make([]int, n)
	cursor := 0
	for t := 0; t < n; t++ {
		deadline := times[t].Add(horizon)
		if cursor < t {
			cursor = t
		}
		for cursor+1 < n && !times[cursor+1].After(deadline) {
			cursor++
		}
		if deadline.After(last) {
			// Fenêtre INCOMPLÈTE : pas de label. Sans cette règle, les
			// dernières bougies seraient étiquetées sur quelques minutes
			// au lieu de plusieurs jours — un label faux, conservé par le
			// filtrage puisque non-NaN. Ce n'est pas une fuite, c'est du
			// bruit appris comme un signal.
			ends[t] = -1
			continue
		}
		ends[t] = cursor
	}

	for t := 0; t < n; t++ {
		a := atr[t]
		if math.IsNaN(a) || a <= 0 {
			continue
		}
		end := ends[t]
		if end <= t {
			continue
		}
		upper := closes[t] + atrMult*a
		lower := closes[t] - atrMult*a

		hit := false
		for j := t + 1; j <= end; j++ {
			up := high[j] >= upper
			down := low[j] <= lower
			if !up && !down {
				continue
			}
			if up && !down {
				out.Value[t], out.Which[t] = 1, BarrierTP
			} else {
				// down seul, ou les deux dans la même bougie → BASSE.
				out.Value[t], out.Which[t] = 0, BarrierSL
			}
			hit = true
			break
		}
		if !hit {
			if closes[end] >= closes[t] {
				out.Value[t] = 1
			} else {
				out.Value[t] = 0
			}
			out.Which[t] = BarrierTime
		}
	}
	return out
}
