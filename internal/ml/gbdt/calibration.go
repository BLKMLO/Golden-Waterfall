package gbdt

import "math"

// Shrinkage : calibrage d'un score brut (log-odds) par RÉTRÉCISSEMENT vers
// un centre, p = σ(Center + A · (score − Center)), avec A ∈ [0, 1].
//
// Un GBDT arrêté tôt sur la perte logistique est APPROXIMATIVEMENT
// calibré sur son propre jeu d'entraînement, pas sur des données qu'il n'a
// pas vues : sur un marché bruité, il prête à ses meilleures feuilles une
// confiance qu'elles n'ont pas hors échantillon. Le rétrécissement, ajusté
// sur une validation que les arbres n'ont pas apprise, mesure cette
// sur-confiance et la retire. A = 1 : le modèle était honnête. A = 0 : il
// ne sait rien, et toutes ses probabilités retombent sur le centre.
//
// Pourquoi pas un calibrage de Platt complet (pente ET ordonnée libres) :
// essayé, puis écarté sur mesure. L'ordonnée, ajustée sur la validation —
// la période la plus RÉCENTE —, y absorbait la tendance de ces quelques
// mois : sur un walk-forward, elle alternait de signe d'un pli à l'autre
// (+0,27, −0,24, +0,19, −0,32 en log-odds), soit un pari directionnel plus
// grand que tout le pouvoir de discrimination du modèle. Un calibrage ne
// doit que RETIRER de la confiance, jamais ajouter une opinion.
type Shrinkage struct {
	Center float64 `json:"center"`
	A      float64 `json:"a"`
}

// NoShrinkage : calibrage neutre (aucune correction).
func NoShrinkage(center float64) Shrinkage { return Shrinkage{Center: center, A: 1} }

// Apply convertit un score brut en probabilité calibrée.
func (c Shrinkage) Apply(score float64) float64 {
	return sigmoid(c.Center + c.A*(score-c.Center))
}

// FitShrinkage ajuste A ∈ [0, 1] sur la perte logistique pondérée d'une
// validation (w nil = poids unitaires), le centre étant FIXÉ — en pratique
// le score de base du modèle, c'est-à-dire le taux de positifs de son
// entraînement.
//
// A est plafonné à 1 : la validation qui sert au calibrage a déjà servi à
// choisir le nombre d'arbres, elle est donc biaisée en faveur du modèle,
// et « le modèle est trop prudent » n'y serait mesuré que par chance.
//
// La perte est convexe en A (perte logistique d'un paramètre linéaire) :
// une recherche par section dorée sur [0, 1] trouve son minimum sans
// risque de divergence. Renvoie false quand l'ajustement n'a pas de sens
// (moins de deux classes) : l'appelant garde alors NoShrinkage et le dit.
func FitShrinkage(scores, y, w []float64, center float64) (Shrinkage, bool) {
	var pos, total float64
	for i := range y {
		wi := weightAt(w, i)
		pos += wi * y[i]
		total += wi
	}
	if total <= 0 || pos <= 0 || pos >= total || len(scores) != len(y) {
		return NoShrinkage(center), false
	}
	loss := func(a float64) float64 {
		var sum float64
		for i, s := range scores {
			z := center + a*(s-center)
			l := softplus(z)
			if y[i] == 1 {
				l = softplus(-z)
			}
			sum += weightAt(w, i) * l
		}
		return sum
	}
	const phi = 0.6180339887498949
	lo, hi := 0.0, 1.0
	x1, x2 := hi-phi*(hi-lo), lo+phi*(hi-lo)
	f1, f2 := loss(x1), loss(x2)
	for hi-lo > 1e-9 {
		if f1 <= f2 {
			hi, x2, f2 = x2, x1, f1
			x1 = hi - phi*(hi-lo)
			f1 = loss(x1)
		} else {
			lo, x1, f1 = x1, x2, f2
			x2 = lo + phi*(hi-lo)
			f2 = loss(x2)
		}
	}
	a := (lo + hi) / 2
	// Les bornes elles-mêmes : la section dorée ne les évalue jamais.
	best, bestLoss := a, loss(a)
	for _, edge := range []float64{0, 1} {
		if l := loss(edge); l < bestLoss {
			best, bestLoss = edge, l
		}
	}
	if math.IsNaN(best) {
		return NoShrinkage(center), false
	}
	return Shrinkage{Center: center, A: best}, true
}

func weightAt(w []float64, i int) float64 {
	if w == nil {
		return 1
	}
	return w[i]
}
