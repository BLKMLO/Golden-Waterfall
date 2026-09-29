package martinet

import (
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// revision : définition FIGÉE d'une révision. Une révision publiée ne se
// modifie jamais : toute évolution en crée une autre, qui la REMPLACE
// (seule la dernière révision d'un moteur est livrée).
//
// Toutes les valeurs ci-dessous sont des CONVENTIONS de départ, choisies
// pour leur sens de métier et jamais mesurées sur le marché. Seuls
// `rrGrid` et `pivotGrid` sont confrontés aux données, par le calibrage
// in-sample (calibrate.go), et c'est le walk-forward qui juge.
type revision struct {
	name, version, summary string

	// window : bougies lues à chaque décision (ATR compris). Décider sur
	// une fenêtre FIXE rend la décision identique en backtest (début de
	// bloc) et en live (tampon glissant).
	window int
	// lookback : âge maximal, en bougies, d'un plus haut ou d'un plus bas
	// pour qu'il compte encore comme une zone de liquidité.
	lookback int
	// atrPeriod : ATR de Wilder, calculé sur la fenêtre seule.
	atrPeriod int
	// maxSweepATR : un dépassement de la zone plus profond que ce multiple
	// d'ATR n'est plus une chasse aux stops mais une cassure : on
	// s'abstient.
	maxSweepATR float64
	// stopBufferATR : le stop est posé à ce multiple d'ATR AU-DELÀ de
	// l'extrême de la mèche qui a balayé la zone.
	stopBufferATR float64
	// maxRiskATR : distance entrée-stop maximale, en ATR. Une bougie de
	// balayage démesurée donnerait un « scalp » au risque de swing.
	maxRiskATR float64
	// maxSpreadR : spread de la bougie de décision, rapporté au risque,
	// au-delà duquel on s'abstient — un scalp qui paie plus du quart de
	// son risque en spread est perdu d'avance. Sans côté ask, le filtre
	// ne s'applique pas (spread inconnu, pas nul).
	maxSpreadR float64
	// sessionFrom, sessionTo : heures UTC (début de bougie) où l'on
	// trade, [from, to). Hors séances européenne et américaine, la
	// liquidité est mince et les zones balayées ne se retournent pas.
	sessionFrom, sessionTo int
	// volWindow : bougies de la médiane de référence du volume relatif.
	volWindow int
	// maxHold : barrière verticale. Un scalp qui n'a touché ni stop ni
	// cible au bout de ce temps a raté son idée.
	maxHold time.Duration
	// timeframes : unités de temps acceptées (scalping).
	timeframes []data.Timeframe

	// Calibrage : multiples de R visés, forces de pivot essayées, seuils
	// de volume relatif essayés (0 = sans filtre), et valeurs de repli
	// quand aucun point n'a assez de trades. Le repli est SANS filtre de
	// volume : il doit valoir partout, FXCM et IB compris.
	rrGrid, pivotGrid, volGrid []float64
	fallbackRR                 float64
	fallbackPivot              int
	minCalibTrades             int
	// minTrainBars : en deçà, pas assez de balayages pour calibrer quoi
	// que ce soit. On REFUSE.
	minTrainBars int
	// usesNews : déclare le filtre d'actualités. Un scalpeur ne veut pas
	// d'une annonce : le spread s'écarte et le balayage n'en est plus un.
	usesNews bool
}

// revisions : la révision LIVRÉE (une génération ne garde que sa
// dernière révision).
//
//	martinet_v1_0 : balayage rejeté, ATR seul. Retirée en v0.8.1.
//	martinet_v1_1 : la même règle, plus un filtre de VOLUME RELATIF
//	                calibré (sans filtre, ou volume de la bougie de
//	                balayage ≥ 1,5 × la médiane des 20 précédentes).
var revisions = []revision{
	{
		name:    "martinet_v1_1",
		version: "1.1",
		summary: "Scalping de zones de liquidité : balayage d'un plus haut ou d'un plus bas " +
			"de swing rejeté à la clôture, confirmé par un pic de volume si le calibrage le retient, " +
			"stop au-delà de la mèche, cible en R calibrée.",
		window:   300,
		lookback: 120,
		// ATR(14) : la période de Wilder (« New Concepts in Technical
		// Trading Systems », 1978).
		atrPeriod:     14,
		maxSweepATR:   1.0,
		stopBufferATR: 0.1,
		maxRiskATR:    2.0,
		maxSpreadR:    0.25,
		// 7 h – 20 h UTC : de l'ouverture de Londres à l'après-midi de
		// New York.
		sessionFrom: 7,
		sessionTo:   20,
		// 20 bougies : même fenêtre que le volume relatif de Colibri
		// (vol_rel_20). CONVENTION.
		volWindow:  20,
		maxHold:    2 * time.Hour,
		timeframes: []data.Timeframe{data.M1, data.M5, data.M15},
		// Grille volontairement petite : plus on essaie de réglages, plus
		// le meilleur doit sa place au hasard.
		rrGrid:    []float64{1.0, 1.5, 2.0},
		pivotGrid: []float64{3, 5},
		// Seuil 1,5 : un balayage qui déclenche des stops fait un pic de
		// volume ; 1,5 fois la médiane est un pic net sans être rare.
		// CONVENTION. La grille passe à 3 × 2 × 2 = 12 points.
		volGrid:        []float64{0, 1.5},
		fallbackRR:     1.5,
		fallbackPivot:  3,
		minCalibTrades: 30,
		minTrainBars:   2000,
		usesNews:       true,
	},
}

func init() {
	for _, r := range revisions {
		strategy.Register(r.name, func() strategy.Strategy { return newMartinet(r) })
	}
	strategy.Retire("martinet_v1_0", "martinet_v1_1")
}
