package troglodyte

import "github.com/BLKMLO/Golden-Waterfall/internal/strategy"

// revision : définition FIGÉE d'une révision. Une révision publiée ne se
// modifie jamais : toute évolution en crée une autre.
type revision struct {
	name, version, summary string
	// window : bougies refiltrées à chaque décision.
	window int
	// enterZ, exitZ : seuils s_in et s_out sur z de REPLI, gardés quand
	// le calibrage n'a pas assez de trades pour trancher.
	enterZ, exitZ float64
	// stopATR, atrPeriod : stop initial à k × ATR de Wilder (k de repli).
	stopATR   float64
	atrPeriod int
	// minTrainBars : en deçà, l'estimation des variances n'a pas assez
	// d'innovations pour être autre chose que du bruit. On REFUSE.
	minTrainBars int

	// volHalfLife : le filtre travaille sur le prix NORMALISÉ par sa
	// volatilité (demi-vie, en bougies, de la moyenne exponentielle des
	// rendements au carré).
	volHalfLife int
	// trailBars : stop suiveur « chandelier » : une position longue
	// sort quand le close passe sous (plus haut des trailBars dernières
	// bougies − k × ATR), et symétriquement. k = stopATR (ou sa valeur
	// calibrée).
	trailBars int
	// enterGrid, stopGrid : grilles du CALIBRAGE. exitRatio :
	// s_out = s_in × exitRatio.
	enterGrid, stopGrid []float64
	exitRatio           float64
	// minCalibTrades : en deçà, un point de grille n'est pas jugé.
	minCalibTrades int
	// usesNews : déclare le filtre d'actualités.
	usesNews bool
}

// revisions : la révision LIVRÉE (règle du propriétaire, v0.8.0 : une
// génération ne garde que sa dernière révision).
var revisions = []revision{
	{
		name:    "troglodyte_v1_1",
		version: "1.1",
		summary: "Tendance structurelle sur prix normalisé par sa volatilité, stop suiveur " +
			"chandelier, seuils calibrés à l'entraînement, filtre d'actualités.",
		window:    500,
		enterZ:    1.5,
		exitZ:     0.5,
		stopATR:   3,
		atrPeriod: 14,
		// minTrainBars : fenêtre + de quoi calibrer sur au moins autant de
		// décisions que la fenêtre compte de bougies.
		minTrainBars: 1000,
		// Demi-vie de 30 bougies : CONVENTION (≈ cinq jours de marché en
		// H4), pas une mesure.
		volHalfLife: 30,
		// 22 bougies : période du « chandelier exit » attribué à Chuck
		// LeBeau (décrit par Alexander Elder, « Come Into My Trading
		// Room », 2002) — là-bas des jours ; ici des bougies, par
		// convention.
		trailBars:      22,
		enterGrid:      []float64{1.0, 1.5, 2.0, 2.5},
		stopGrid:       []float64{2, 3, 4},
		exitRatio:      1.0 / 3,
		minCalibTrades: 20,
		usesNews:       true,
	},
}

func init() {
	for _, r := range revisions {
		strategy.Register(r.name, func() strategy.Strategy { return newTroglodyte(r) })
	}
	strategy.Retire("troglodyte_v1_0", "troglodyte_v1_1")
}
