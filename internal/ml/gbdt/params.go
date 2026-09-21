// Package gbdt implémente un gradient boosting sur arbres de décision, en
// Go PUR, pour la classification binaire (perte logistique).
//
// Pourquoi l'écrire plutôt que lier une bibliothèque existante : Golden
// Waterfall tient dans UN binaire sans dépendance native. Les
// implémentations de référence sont en C++ et imposeraient cgo, une chaîne
// de compilation et une bibliothèque partagée à côté de l'exécutable.
//
// L'algorithme retenu est celui, classique, du boosting par histogrammes :
// croissance FEUILLE PAR FEUILLE guidée par le gain, bins par quantiles,
// soustraction d'histogramme pour le frère, gestion native des valeurs
// manquantes et des features catégorielles.
//
// Ce qui est volontairement ABSENT (et donc non promis) : GOSS, EFB,
// l'apprentissage distribué, les objectifs multiclasses et la régression.
// Le projet n'en a pas besoin ; les ajouter à moitié serait pire que ne
// pas les avoir.
package gbdt

import (
	"fmt"
	"runtime"
)

// Params regroupe les hyperparamètres de l'entraînement.
//
// Les valeurs par défaut (DefaultParams) sont volontairement PRUDENTES :
// les historiques d'une paire de devises sont courts au regard du bruit,
// et un modèle qui mémorise est pire qu'inutile — il donne confiance.
type Params struct {
	// NumRounds : nombre maximum d'arbres. L'arrêt anticipé peut réduire
	// le nombre réellement retenu (BestIteration du modèle).
	NumRounds int
	// LearningRate : contribution de chaque arbre.
	LearningRate float64
	// NumLeaves : complexité d'un arbre (croissance feuille par feuille).
	NumLeaves int
	// MaxDepth : 0 = pas de limite (seul NumLeaves borne alors l'arbre).
	MaxDepth int
	// MinDataInLeaf : nombre minimum d'échantillons dans une feuille.
	MinDataInLeaf int
	// MinSumHessianInLeaf : masse de courbure minimale dans une feuille.
	// Complète MinDataInLeaf : 200 échantillons dont le modèle est déjà
	// certain ne portent presque aucune information.
	MinSumHessianInLeaf float64
	// LambdaL2 : régularisation L2 sur la valeur des feuilles.
	LambdaL2 float64
	// MinGainToSplit : gain minimal pour accepter une coupure.
	MinGainToSplit float64
	// MaxBins : nombre maximum de bins par feature (<= 254, le dernier
	// indice étant réservé aux valeurs manquantes).
	MaxBins int
	// FeatureFraction : proportion de features tirées à chaque arbre.
	FeatureFraction float64
	// BaggingFraction / BaggingFreq : sous-échantillonnage des lignes.
	BaggingFraction float64
	BaggingFreq     int
	// EarlyStoppingRounds : nombre de tours sans amélioration de la perte
	// de validation avant arrêt. 0 = désactivé.
	EarlyStoppingRounds int
	// CatSmooth / MinDataPerGroup : garde-fous des splits catégoriels.
	CatSmooth       float64
	MinDataPerGroup int
	// Seed : graine du tirage (features, bagging). Fixée ⇒ entraînement
	// REPRODUCTIBLE, ce qui est indispensable pour comparer deux runs.
	Seed int64
	// Threads : parallélisme du calcul d'histogrammes. 0 = NumCPU.
	Threads int
	// CategoricalFeatures : indices de colonnes à traiter comme des
	// catégories (partition d'ensemble) et non comme des nombres ordonnés.
	CategoricalFeatures []int
}

// DefaultParams : hyperparamètres prudents, adaptés à des historiques
// courts au regard du bruit.
func DefaultParams() Params {
	return Params{
		NumRounds:           300,
		LearningRate:        0.03,
		NumLeaves:           31,
		MaxDepth:            0,
		MinDataInLeaf:       50,
		MinSumHessianInLeaf: 1e-3,
		LambdaL2:            1.0,
		MinGainToSplit:      0,
		MaxBins:             254,
		FeatureFraction:     0.8,
		BaggingFraction:     0.8,
		BaggingFreq:         1,
		EarlyStoppingRounds: 30,
		CatSmooth:           10,
		MinDataPerGroup:     50,
		Seed:                42,
		Threads:             0,
	}
}

// Validate refuse les combinaisons impossibles AVANT de brûler du temps de
// calcul.
func (p *Params) Validate() error {
	switch {
	case p.NumRounds < 1:
		return fmt.Errorf("num_rounds doit être >= 1")
	case p.LearningRate <= 0 || p.LearningRate > 1:
		return fmt.Errorf("learning_rate doit être dans ]0, 1]")
	case p.NumLeaves < 2:
		return fmt.Errorf("num_leaves doit être >= 2")
	case p.MinDataInLeaf < 1:
		return fmt.Errorf("min_data_in_leaf doit être >= 1")
	case p.MaxBins < 2 || p.MaxBins > 254:
		return fmt.Errorf("max_bins doit être dans [2, 254] (le dernier indice est réservé aux manquants)")
	case p.FeatureFraction <= 0 || p.FeatureFraction > 1:
		return fmt.Errorf("feature_fraction doit être dans ]0, 1]")
	case p.BaggingFraction <= 0 || p.BaggingFraction > 1:
		return fmt.Errorf("bagging_fraction doit être dans ]0, 1]")
	case p.LambdaL2 < 0:
		return fmt.Errorf("lambda_l2 ne peut pas être négatif")
	}
	if p.Threads <= 0 {
		p.Threads = runtime.NumCPU()
	}
	if p.MinDataPerGroup < 1 {
		p.MinDataPerGroup = 1
	}
	return nil
}
