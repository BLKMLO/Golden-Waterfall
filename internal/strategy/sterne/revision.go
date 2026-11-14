package sterne

import (
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// window : une fenêtre de fixing. L'entrée se décide à la clôture de la
// bougie qui FINIT à l'heure locale (hour, minute) de la place `zone` ; la
// position est tenue `hold` puis fermée par la barrière verticale.
type window struct {
	name string
	// zone : fuseau de la place du fixing. L'heure d'été est celle de la
	// place, jamais un décalage UTC codé en dur : le fixing de Francfort
	// est à 14 h 15 de Francfort, été comme hiver.
	zone         string
	loc          *time.Location
	hour, minute int
	// usd : sens pris sur le DOLLAR (+1 achat, −1 vente). Le sens sur la
	// paire s'en déduit (usdSide).
	usd int
}

// revision : définition FIGÉE d'une révision. Une révision publiée ne se
// modifie jamais : toute évolution en crée une autre, qui la REMPLACE.
type revision struct {
	name, version, summary string

	// windows : la grille des fenêtres candidates. Elle est fixée A PRIORI
	// par la littérature (le dollar s'apprécie avant un fixing et se
	// déprécie après : Krohn, Mueller et Whelan, Journal of Finance, 2024)
	// et non choisie sur les données : deux heures avant et deux heures
	// après chacun des trois fixings. Le calibrage ne fait que RETENIR ou
	// ÉCARTER chaque fenêtre, paire par paire.
	windows []window
	// hold : durée de portage, la même pour toutes les fenêtres.
	hold time.Duration
	// atrWindow, atrPeriod : l'ATR de Wilder est calculé sur une fenêtre
	// FIXE de bougies, pour que le live décide comme le backtest.
	atrWindow, atrPeriod int
	// stopATR : distance du stop, en ATR. Le stop est une PROTECTION (et
	// l'unité du dimensionnement au risque), pas une règle de sortie : la
	// sortie normale est l'heure. CONVENTION, jamais mesurée.
	stopATR float64
	// minScore, minTrades : une fenêtre n'est retenue que si le t de
	// Student de ses trades d'entraînement (R nets du spread médian)
	// atteint minScore sur au moins minTrades trades. 2,0 : seuil
	// unilatéral d'environ 2,3 % sous l'hypothèse nulle. 250 : environ
	// une année de jours ouvrés. CONVENTIONS.
	minScore  float64
	minTrades int
	// minTrainBars : en deçà, rien à calibrer. On REFUSE.
	minTrainBars int
	timeframes   []data.Timeframe
}

// revisions : la révision LIVRÉE.
//
//	sterne_v1_0 : six fenêtres de deux heures autour des fixings de
//	              Tokyo, Francfort et Londres ; stop à 4 ATR.
var revisions = []revision{
	{
		name:    "sterne_v1_0",
		version: "1.0",
		summary: "Saisonnalité intrajournalière du dollar autour des fixings : achat du dollar " +
			"dans les deux heures qui précèdent un fixing, vente dans les deux heures qui suivent, " +
			"pour les seules fenêtres que le calibrage retient sur la paire. Sortie à l'heure.",
		windows: []window{
			// Fixing de Tokyo : 9 h 55, heure de Tokyo. En M15, la bougie
			// qui le contient finit à 10 h.
			{name: "tokyo_avant", zone: "Asia/Tokyo", hour: 8, minute: 0, usd: +1},
			{name: "tokyo_apres", zone: "Asia/Tokyo", hour: 10, minute: 0, usd: -1},
			// Fixing de la BCE : 14 h 15, heure de Francfort.
			{name: "francfort_avant", zone: "Europe/Berlin", hour: 12, minute: 15, usd: +1},
			{name: "francfort_apres", zone: "Europe/Berlin", hour: 14, minute: 15, usd: -1},
			// Fixing WM/Reuters : 16 h, heure de Londres.
			{name: "londres_avant", zone: "Europe/London", hour: 14, minute: 0, usd: +1},
			{name: "londres_apres", zone: "Europe/London", hour: 16, minute: 0, usd: -1},
		},
		hold:      2 * time.Hour,
		atrWindow: 100,
		// ATR(14) : la période de Wilder (« New Concepts in Technical
		// Trading Systems », 1978).
		atrPeriod:    14,
		stopATR:      4,
		minScore:     2.0,
		minTrades:    250,
		minTrainBars: 2000,
		// M15 seulement : le fixing de Francfort tombe au quart d'heure,
		// et la durée d'une bougie entre dans la définition des fenêtres.
		timeframes: []data.Timeframe{data.M15},
	},
}

func init() {
	for i := range revisions {
		for k := range revisions[i].windows {
			w := &revisions[i].windows[k]
			loc, err := time.LoadLocation(w.zone)
			if err != nil {
				// Base des fuseaux embarquée (time/tzdata, via core) : un
				// échec est une incohérence du binaire, pas une donnée.
				panic("fuseau " + w.zone + " introuvable dans la base embarquée : " + err.Error())
			}
			w.loc = loc
		}
		r := revisions[i]
		strategy.Register(r.name, func() strategy.Strategy { return newSterne(r) })
	}
}

// barDuration : durée d'une bougie de la révision (une seule unité de
// temps acceptée).
func (r revision) barDuration() time.Duration { return r.timeframes[0].Duration() }

func (r revision) windowByName(name string) (window, bool) {
	for _, w := range r.windows {
		if w.name == name {
			return w, true
		}
	}
	return window{}, false
}
