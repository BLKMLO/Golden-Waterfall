package troglodyte

import (
	"math"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// rule : paramètres de décision EFFECTIFS d'un modèle — ceux de la
// révision, ou ceux que le calibrage a retenus.
type rule struct {
	enterZ, exitZ float64
	// stopATR : distance du stop initial ET du stop suiveur, en ATR.
	stopATR float64
	// trail : stop suiveur chandelier actif (v1_1).
	trail bool
}

// barState : ce que la décision voit de la bougie i.
type barState struct {
	z, close, atr float64
	// hh, ll : plus haut et plus bas des trailBars dernières bougies
	// (bougie i comprise). Ignorés sans stop suiveur.
	hh, ll float64
}

// decide est LA règle de décision : OnBar et le calibrage l'appellent
// tous deux, si bien que le calibrage juge exactement ce qui tradera.
// Renvoie l'action et, pour une entrée, le niveau du stop initial.
func decide(b barState, r rule) (core.SignalAction, float64) {
	offset := r.stopATR * b.atr
	if !r.trail {
		// troglodyte_v1_0, inchangée.
		switch {
		case b.z >= r.enterZ:
			return core.EnterLong, b.close - offset
		case b.z <= -r.enterZ:
			return core.EnterShort, b.close + offset
		case math.Abs(b.z) < r.exitZ:
			return core.Exit, 0
		}
		return core.Hold, 0
	}

	// troglodyte_v1_1 : une position vit tant que la pente est nette ET
	// que le prix n'a pas reculé de k ATR depuis son extrême récent.
	longTrail := b.hh - offset
	shortTrail := b.ll + offset
	switch {
	case b.z >= r.enterZ && b.close > longTrail:
		return core.EnterLong, b.close - offset
	case b.z <= -r.enterZ && b.close < shortTrail:
		return core.EnterShort, b.close + offset
	}
	exitLong := b.z < r.exitZ || b.close < longTrail
	exitShort := b.z > -r.exitZ || b.close > shortTrail
	switch {
	case exitLong && exitShort:
		return core.Exit, 0
	case exitLong:
		return core.ExitLong, 0
	case exitShort:
		return core.ExitShort, 0
	}
	return core.Hold, 0
}

// normalizedPath : chemin du prix normalisé par sa volatilité,
//
//	x_0 = 0      x_t = x_{t−1} + r_t / σ_{t−1}      r_t = ln P_t − ln P_{t−1}
//	σ²_t = λ σ²_{t−1} + (1 − λ) r_t²                 λ = 2^(−1/demi-vie)
//
// σ²_0 est la moyenne des r² sur la première demi-vie. Un rendement est
// divisé par la volatilité CONNUE avant lui (σ_{t−1}) : en période
// agitée, les pas se contractent, et une pente de 2 σ par bougie veut dire
// la même chose en 2020 qu'en 2014 — c'est ce qui rend les seuils sur z
// transposables d'un régime à l'autre. Tout est calculé sur la tranche
// reçue seule : appliquée à la fenêtre de décision, la normalisation ne
// dépend d'aucune bougie hors de la fenêtre.
func normalizedPath(logp []float64, halfLife int) ([]float64, bool) {
	n := len(logp)
	if n < halfLife+2 || halfLife < 1 {
		return nil, false
	}
	var seed float64
	for t := 1; t <= halfLife; t++ {
		r := logp[t] - logp[t-1]
		seed += r * r
	}
	variance := seed / float64(halfLife)
	if !(variance > 0) {
		return nil, false
	}
	lambda := math.Pow(2, -1/float64(halfLife))
	x := make([]float64, n)
	for t := 1; t < n; t++ {
		r := logp[t] - logp[t-1]
		x[t] = x[t-1] + r/math.Sqrt(variance)
		variance = lambda*variance + (1-lambda)*r*r
		if !(variance > 0) {
			// Des prix figés sur toute une demi-vie : plus d'échelle.
			return nil, false
		}
	}
	return x, true
}

// extremes : plus haut des hauts et plus bas des bas des n dernières
// bougies de s.
func extremes(s core.Series, n int) (hh, ll float64) {
	if n > len(s) {
		n = len(s)
	}
	hh, ll = math.Inf(-1), math.Inf(1)
	for _, b := range s[len(s)-n:] {
		hh = math.Max(hh, b.High())
		ll = math.Min(ll, b.Low())
	}
	return hh, ll
}
