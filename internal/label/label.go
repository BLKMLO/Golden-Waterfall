// Package label produit des CIBLES d'apprentissage par la méthode des
// barrières. C'est une bibliothèque : elle ne porte aucune constante de
// définition. Chaque révision de stratégie fournit ses propres barrières,
// son horizon et son ATR — les mêmes qu'elle enverra à l'exécution.
//
// Sided étiquette un label PAR CÔTÉ (long, short), net de coûts, en
// rejouant la règle d'exécution du moteur de backtest bougie pour bougie.
// (Le label symétrique de colibri_v1_0 et v1_1 a disparu avec ces
// révisions, en v0.8.0.)
//
// ⚠ Un label REGARDE VERS L'AVANT — c'est sa nature, c'est la cible. Il
// n'est défini que pour les bougies disposant d'une fenêtre avant
// COMPLÈTE : une bougie dont l'horizon dépasse la fin de la série reçoit
// NaN, jamais un label calculé sur une fenêtre tronquée. Les features,
// elles, restent strictement causales.
package label

// Barrier nomme la barrière touchée en premier (diagnostic).
type Barrier string

const (
	BarrierNone Barrier = ""
	BarrierTP   Barrier = "tp"
	BarrierSL   Barrier = "sl"
	BarrierTime Barrier = "time"
)
