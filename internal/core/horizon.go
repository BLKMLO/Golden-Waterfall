package core

import (
	"math"
	"sort"
	"time"
	// Base des fuseaux EMBARQUÉE : un binaire CGO_ENABLED=0 sous Windows
	// n'en trouve aucune sur la machine, et la clôture hebdomadaire se
	// calcule à l'heure de New York (heure d'été comprise).
	_ "time/tzdata"
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

// WeekendGuard : avance prise sur la clôture hebdomadaire pour fermer une
// position en live. CONVENTION, pas mesure : assez pour qu'un ordre au
// marché soit servi avant la fermeture, sans sortir des heures plus tôt
// que le backtest (qui ferme au close de la dernière bougie).
const WeekendGuard = 5 * time.Minute

// newYork : fuseau de la clôture hebdomadaire du forex.
var newYork = mustLoadLocation("America/New_York")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Base embarquée (time/tzdata) : un échec est une incohérence du
		// binaire, pas une donnée.
		panic("fuseau " + name + " introuvable dans la base embarquée : " + err.Error())
	}
	return loc
}

// WeeklyClose renvoie la clôture hebdomadaire du forex qui suit `t` (ou
// qui tombe exactement sur `t`) : le vendredi à 17 h, heure de New York —
// la convention de place, qui est aussi l'heure de fermeture d'IDEALPRO.
// Elle vaut 21 h UTC en heure d'été américaine et 22 h UTC en heure
// d'hiver ; le fuseau le calcule, rien n'est codé en dur.
//
// Le backtest n'en a pas besoin : l'historique dit lui-même où la semaine
// s'arrête (LastBarsOfWeek). Le live, lui, ne voit la dernière bougie du
// vendredi se clore qu'au premier tick du dimanche soir — trop tard. Il
// lui faut une heure, prise sur le temps du MARCHÉ (horodatage des ticks),
// jamais sur l'horloge de la machine : un rejeu doit fermer au vendredi
// rejoué.
//
// Métaux et indices, qui ne se jouent qu'en rejeu (IB : forex seulement),
// reçoivent la même heure ; la leur n'a pas été vérifiée.
func WeeklyClose(t time.Time) time.Time {
	local := t.In(newYork)
	days := (int(time.Friday) - int(local.Weekday()) + 7) % 7
	close := time.Date(local.Year(), local.Month(), local.Day()+days, 17, 0, 0, 0, newYork)
	if close.Before(t) {
		close = close.AddDate(0, 0, 7)
	}
	return close
}

// LastBarBeforeWeekend : la bougie qui commence à `barTime` est-elle la
// dernière qu'un moteur live puisse clore avant la fermeture du week-end ?
// Une entrée décidée à sa clôture serait portée pendant la fermeture.
func LastBarBeforeWeekend(barTime time.Time, barDuration time.Duration) bool {
	deadline := WeeklyClose(barTime).Add(-WeekendGuard)
	return !barTime.Add(barDuration).Before(deadline)
}
