// Package label produit la CIBLE d'apprentissage de Colibri par la
// méthode des triples barrières.
//
// Pour chaque bougie t, trois barrières sont placées depuis son close :
//   - HAUTE     : close_t + mult · ATR_t   (un long y gagnerait) ;
//   - BASSE     : close_t − mult · ATR_t   (un long y perdrait) ;
//   - VERTICALE : horizon de MaxHoldDays jours calendaires.
//
// Le label est BINAIRE et SYMÉTRIQUE : 1 si la barrière haute est touchée
// en premier, 0 sinon. Un même modèle sert donc les deux sens :
// P(haute d'abord) élevée → long, faible → short. Si l'horizon expire sans
// barrière touchée, on étiquette par le signe du retour sur l'horizon.
//
// ⚠ Le label REGARDE VERS L'AVANT — c'est sa nature, c'est la cible. Il
// n'est défini que pour les bougies disposant d'une fenêtre avant
// COMPLÈTE : une bougie dont l'horizon dépasse la fin de la série reçoit
// NaN, jamais un label calculé sur une fenêtre tronquée. Les features,
// elles, restent strictement causales.
package label

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
)

// Constantes de DÉFINITION du modèle Colibri (barrières identiques au
// labeling et à l'exécution). Pas des réglages runtime.
const (
	BarrierATRMult = 1.5
	MaxHoldDays    = 5
)

// Barrier nomme la barrière touchée en premier (diagnostic).
type Barrier string

const (
	BarrierNone Barrier = ""
	BarrierTP   Barrier = "tp"
	BarrierSL   Barrier = "sl"
	BarrierTime Barrier = "time"
)

// Labels est le résultat de l'étiquetage, aligné sur la série d'entrée.
type Labels struct {
	// Value vaut 1, 0, ou NaN quand le label est indéfini (ATR manquant,
	// fenêtre avant incomplète).
	Value []float64
	// Which indique la barrière retenue, pour le diagnostic.
	Which []Barrier
	// ATR au moment t, réutilisé tel quel par l'inférence.
	ATR []float64
}

// Defined indique si la bougie porte un label exploitable.
func (l Labels) Defined(i int) bool { return !math.IsNaN(l.Value[i]) }

// TripleBarrier étiquette chaque bougie par la première barrière touchée.
//
// Départage quand les DEUX barrières tombent dans la MÊME bougie : on
// retient la BASSE (label 0). C'est exactement la convention du moteur
// d'exécution, qui suppose le stop touché d'abord faute de connaître
// l'ordre intrabar réel. Entraîner sur une règle plus optimiste
// apprendrait au modèle des gains que l'exécution ne délivre jamais : la
// cible doit décrire ce que le programme obtiendra vraiment.
func TripleBarrier(series core.Series, atrMult float64, maxHoldDays int) Labels {
	n := len(series)
	out := Labels{
		Value: make([]float64, n),
		Which: make([]Barrier, n),
		ATR:   feature.ATR(series),
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
	horizon := time.Duration(maxHoldDays) * 24 * time.Hour
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
		atr := out.ATR[t]
		if math.IsNaN(atr) || atr <= 0 {
			continue
		}
		end := ends[t]
		if end <= t {
			continue
		}
		upper := closes[t] + atrMult*atr
		lower := closes[t] - atrMult*atr

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

// Default applique les constantes de définition du modèle.
func Default(series core.Series) Labels {
	return TripleBarrier(series, BarrierATRMult, MaxHoldDays)
}
