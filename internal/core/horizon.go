package core

import (
	"math"
	"sort"
	"time"
)

// Règles de SORTIE partagées par tous les exécuteurs (backtest, live) et
// par les stratégies qui étiquettent leur cible sur ces mêmes règles.
//
// Elles vivent ici, dans le langage commun, et non dans une stratégie :
// une règle de barrière écrite deux fois finit par diverger, et un moteur
// d'exécution qui importerait le paquet d'une stratégie ne pourrait plus en
// changer sans être réécrit.

// HoldDeadline renvoie l'échéance de la barrière VERTICALE d'une entrée.
// Zéro quand la stratégie ne déclare aucun horizon : aucune sortie forcée
// par le temps.
func HoldDeadline(entry time.Time, maxHold time.Duration) time.Time {
	if maxHold <= 0 {
		return time.Time{}
	}
	return entry.Add(maxHold)
}

// HoldExpired : la bougie qui COMMENCE à `barTime` est-elle la dernière à
// commencer avant l'échéance ?
//
// Formulée sur la bougie courante et la cadence du flux, elle désigne la
// même bougie que la fenêtre (t, t+horizon] d'un étiquetage, tout en
// restant calculable en direct : le live ne connaît pas l'horodatage de la
// bougie suivante. Sans cadence exploitable (unité de temps absente), on
// se rabat sur le dépassement strict — une bougie de retard, jamais une
// bougie d'avance.
func HoldExpired(barTime time.Time, barDuration time.Duration, deadline time.Time) bool {
	if deadline.IsZero() {
		return false
	}
	if barDuration <= 0 {
		return barTime.After(deadline)
	}
	return barTime.Add(barDuration).After(deadline)
}

// LastBarsOfWeek marque, pour chaque bougie, si elle est la dernière de sa
// semaine ISO.
//
// Le forex n'a pas de bougie le week-end : une semaine se termine quand la
// bougie SUIVANTE bascule sur une autre semaine ISO — robuste aux
// frontières d'année. La dernière bougie de la série reste false : on ne
// sait pas encore si la semaine continue.
//
// Le backtest s'en sert pour la clôture de fin de semaine ; une stratégie
// qui veut apprendre la cible que l'exécution délivre s'en sert pour
// borner son étiquetage. Même fonction des deux côtés : même bougie.
func LastBarsOfWeek(series Series) []bool {
	flags := make([]bool, len(series))
	for i := 0; i+1 < len(series); i++ {
		y1, w1 := series[i].Time.UTC().ISOWeek()
		y2, w2 := series[i+1].Time.UTC().ISOWeek()
		if y1 != y2 || w1 != w2 {
			flags[i] = true
		}
	}
	return flags
}

// MedianSpread renvoie le spread MÉDIAN observé (ask_close − bid_close).
//
// La médiane et non la moyenne : le spread s'élargit brutalement à
// l'ouverture, sur les annonces et le week-end ; une moyenne serait tirée
// par ces pointes et surestimerait le coût du régime normal.
//
// Renvoie false si la série n'a pas de côté ask exploitable : aucun spread
// n'est alors connu, et l'appelant doit le dire plutôt que compter zéro.
func (s Series) MedianSpread() (float64, bool) {
	spreads := make([]float64, 0, len(s))
	for _, b := range s {
		if !b.HasAsk() {
			continue
		}
		if v := b.AskClose - b.BidClose; v > 0 {
			spreads = append(spreads, v)
		}
	}
	if len(spreads) == 0 {
		return 0, false
	}
	sort.Float64s(spreads)
	n := len(spreads)
	m := spreads[n/2]
	if n%2 == 0 {
		m = (spreads[n/2-1] + spreads[n/2]) / 2
	}
	if math.IsNaN(m) || m <= 0 {
		return 0, false
	}
	return m, true
}
