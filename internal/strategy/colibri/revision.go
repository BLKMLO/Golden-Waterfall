// Package colibri est la PREMIÈRE génération de moteur de décision de
// Golden Waterfall (le logiciel, lui, s'appelle Golden Waterfall — ne
// jamais confondre les deux).
//
// Tout ce qui est propre à Colibri vit ICI : features, cible, règle de
// décision, format des modèles. Rien en dehors de ce paquet ne l'importe,
// hormis le catalogue `internal/strategies`. Les moteurs d'exécution, le
// walk-forward et l'interface ne connaissent que le contrat
// `strategy.Strategy` ; remplacer Colibri par la génération suivante
// consiste à écrire un paquet voisin et à changer une ligne du catalogue.
//
// # Nommage des moteurs
//
// Chaque GÉNÉRATION de moteur porte un nom d'oiseau. Colibri est la
// première ; la suivante, quand elle changera d'approche, portera un autre
// nom d'oiseau plutôt qu'un numéro de plus. Les révisions à l'intérieur
// d'une génération sont numérotées (`colibri_v1_0`, `colibri_v1_1`, …) ;
// seule la DERNIÈRE est livrée (règle du propriétaire, v0.8.0).
//
// # Principe
//
// Un classifieur GBDT apprend, sur des bougies étiquetées par barrières à
// ± k × ATR, la probabilité qu'un trade ouvert au close finisse gagnant ;
// la stratégie entre quand cette probabilité le justifie, avec stop et
// limite aux barrières mêmes de l'étiquetage.
//
// # Révisions
//
// Toute évolution de la DÉFINITION (features, cible, barrières, règle de
// décision) crée une nouvelle révision, jamais une modification en place —
// sans quoi un modèle archivé ne voudrait plus rien dire. Seule la
// dernière révision est livrée : la suivante REMPLACE celle-ci, elle ne
// la retouche pas.
//
//	colibri_v1_0 : un modèle par actif, cible symétrique, seuils 0,55 / 0,45.
//	               Retirée en v0.8.0.
//	colibri_v1_1 : modèle mutualisé, cible symétrique, seuils 0,60 / 0,40.
//	               Retirée en v0.8.0.
//	colibri_v1_2 : mutualisé, DEUX têtes (long, short) apprises sur l'issue
//	               NETTE de coûts que le moteur d'exécution produirait
//	               (stop prioritaire dans les deux sens, gaps, clôture de
//	               fin de semaine), jeu d'entraînement purgé avant la
//	               validation, probabilités calibrées, features exprimées
//	               en unités de volatilité, et entrée décidée sur
//	               l'ESPÉRANCE nette en unités de barrière plutôt que sur
//	               un seuil de probabilité. Détail, mesures et limites :
//	               docs/colibri.md.
package colibri

import (
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// featureSet : un jeu de features FIGÉ — ses colonnes, dans leur ordre
// canonique, et la fonction qui les calcule.
type featureSet struct {
	columns []string
	compute func(core.Series) *feature.Matrix
	// optional : colonnes dont l'absence (NaN) n'empêche pas de décider.
	// Une information honnêtement inconnue (pas de côté ask, symbole
	// inconnu du modèle) n'est pas un trou de calcul.
	optional map[string]bool
}

// revision : la DÉFINITION complète d'une révision. Aucune de ces valeurs
// n'est un réglage runtime : elles n'ont rien à faire dans config.yaml.
type revision struct {
	name    string
	version string
	summary string

	features featureSet

	barrierATRMult float64
	maxHoldDays    int

	// Règle à l'espérance : marge minimale, en unités de barrière, NETTE
	// de coûts.
	//
	// Pour v1_2, 0,10 R : choisie par ablation (ablation_test.go) entre
	// 0, 0,05, 0,10, 0,15 et 0,20 R, sur quatre marchés SYNTHÉTIQUES, huit
	// graines chacun. C'est la seule marge qui fasse au moins aussi bien
	// que colibri_v1_1 (retirée depuis) en P&L total sur les quatre ET
	// perde moins qu'elle sur les deux marchés sans signal exploitable. Ce
	// critère a été formulé APRÈS la mesure. À coût nul et gains/pertes de
	// ± 1 R, elle revient à p ≥ 0,55. ⚠ Mesure sur données
	// synthétiques, pas sur le marché : à re-mesurer sur l'historique réel
	// par une révision suivante, jamais par une retouche de celle-ci.
	minEdgeR float64

	// purge : retirer du jeu d'entraînement les lignes dont le label
	// déborde sur la période de validation de l'arrêt anticipé.
	purge bool
	// calibrate : rétrécir les probabilités de chaque tête vers son taux
	// de base, sur la validation (gbdt.FitShrinkage).
	calibrate bool
	// uniqueness : pondérer chaque ligne par l'unicité moyenne de son
	// label (label.AverageUniqueness).
	uniqueness bool
}

// heads : têtes du modèle, dans l'ordre : une par sens.
func (r revision) heads() []string { return []string{headLong, headShort} }

const (
	headLong  = "long"
	headShort = "short"
)

// headFile : fichier du modèle d'une tête.
func headFile(head string) string { return "model_" + head + ".json" }

func (r revision) maxHold() time.Duration {
	return time.Duration(r.maxHoldDays) * 24 * time.Hour
}

// columns : ordre CANONIQUE des colonnes de la révision, `symbol` compris
// (le modèle est mutualisé). Source unique : entraînement, inférence et
// vérification au chargement l'appellent tous.
func (r revision) columns() []string {
	return append(append([]string(nil), r.features.columns...), symbolColumnName)
}

// symbolColumnName : nom de la feature catégorielle d'identité de l'actif.
// Constante plutôt que littéral répété : elle doit être IDENTIQUE dans la
// matrice, dans metadata.json et dans la vérification au chargement.
const symbolColumnName = "symbol"

var revV12 = revision{
	name:    "colibri_v1_2",
	version: "1.2.0",
	summary: "Mutualisé, deux têtes long/short sur l'issue nette d'exécution — " +
		"entrée à l'espérance nette ≥ 0,10 R.",
	features:       featuresV2,
	barrierATRMult: 1.5,
	maxHoldDays:    5,
	minEdgeR:       0.10,
	purge:          true,
	calibrate:      true,
	// Unicité : mesurée puis ÉCARTÉE (ablation_test.go) — égale ou moins
	// bonne que sans sur les quatre marchés de l'ablation. Un ingrédient
	// heuristique que la mesure ne soutient pas n'entre pas dans une
	// révision ; le mécanisme reste disponible pour une révision qui le
	// mesurerait sur données réelles. (La purge, elle, est gardée sans
	// condition : c'est une correction de FUITE, pas une heuristique.)
	uniqueness: false,
}

// init enregistre la révision livrée, et nomme les retirées.
func init() {
	strategy.Register(revV12.name, func() strategy.Strategy { return newColibri(revV12) })
	strategy.Retire("colibri_v1_0", revV12.name)
	strategy.Retire("colibri_v1_1", revV12.name)
}
