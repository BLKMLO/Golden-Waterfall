package gbdt

import (
	"math"
	"sort"
)

// LogLoss : perte logistique moyenne sur des PROBABILITÉS.
func LogLoss(probs, y []float64) float64 {
	if len(probs) == 0 {
		return math.NaN()
	}
	const eps = 1e-15
	var sum float64
	for i, p := range probs {
		p = math.Min(math.Max(p, eps), 1-eps)
		if y[i] == 1 {
			sum -= math.Log(p)
		} else {
			sum -= math.Log(1 - p)
		}
	}
	return sum / float64(len(probs))
}

// logLossFromScores évite un passage par les probabilités (échelle
// log-odds), pour rester numériquement stable sur des scores extrêmes.
func logLossFromScores(scores, y []float64) float64 {
	if len(scores) == 0 {
		return math.NaN()
	}
	var sum float64
	for i, z := range scores {
		// log(1+exp(-z)) pour y=1, log(1+exp(z)) pour y=0, calculé par
		// softplus stable.
		if y[i] == 1 {
			sum += softplus(-z)
		} else {
			sum += softplus(z)
		}
	}
	return sum / float64(len(scores))
}

// weightedLogLoss : perte logistique pondérée, sur des scores. Sans poids,
// c'est exactement logLossFromScores — même chemin, mêmes bits.
func weightedLogLoss(scores []float64, d *Dataset) float64 {
	if d.W == nil {
		return logLossFromScores(scores, d.Y)
	}
	if len(scores) == 0 {
		return math.NaN()
	}
	var sum, total float64
	for i, z := range scores {
		l := softplus(z)
		if d.Y[i] == 1 {
			l = softplus(-z)
		}
		sum += d.W[i] * l
		total += d.W[i]
	}
	return sum / total
}

func softplus(z float64) float64 {
	if z > 30 {
		return z
	}
	if z < -30 {
		return math.Exp(z)
	}
	return math.Log1p(math.Exp(z))
}

// AUC (aire sous la courbe ROC) calculée par la statistique de
// Mann-Whitney sur les RANGS, avec gestion correcte des ex æquo (rangs
// moyens). C'est la métrique de référence du projet : elle ne dépend
// d'aucun seuil, donc elle mesure le POUVOIR DE CLASSEMENT du modèle, pas
// le hasard d'un point de coupure bien choisi.
//
// Renvoie NaN si une seule classe est présente — l'AUC n'est alors PAS
// définie, et renvoyer 0,5 laisserait croire à une mesure.
func AUC(probs, y []float64) float64 { return AUCFromScores(probs, y) }

// AUCFromScores accepte n'importe quel score monotone (probabilité ou
// log-odds) : l'AUC ne dépend que de l'ordre.
func AUCFromScores(scores, y []float64) float64 {
	n := len(scores)
	if n == 0 || len(y) != n {
		return math.NaN()
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] < scores[idx[b]] })

	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && scores[idx[j+1]] == scores[idx[i]] {
			j++
		}
		// Rangs moyens sur le bloc d'ex æquo (rangs 1-indexés).
		avg := (float64(i+1) + float64(j+1)) / 2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		i = j + 1
	}

	var pos, neg, sumRankPos float64
	for i := 0; i < n; i++ {
		if y[i] == 1 {
			pos++
			sumRankPos += ranks[i]
		} else {
			neg++
		}
	}
	if pos == 0 || neg == 0 {
		return math.NaN()
	}
	return (sumRankPos - pos*(pos+1)/2) / (pos * neg)
}

// Accuracy au seuil donné (diagnostic seulement : sur un jeu déséquilibré
// elle est trompeuse, l'AUC reste la référence).
func Accuracy(probs, y []float64, threshold float64) float64 {
	if len(probs) == 0 {
		return math.NaN()
	}
	var good int
	for i, p := range probs {
		pred := 0.0
		if p >= threshold {
			pred = 1
		}
		if pred == y[i] {
			good++
		}
	}
	return float64(good) / float64(len(probs))
}
