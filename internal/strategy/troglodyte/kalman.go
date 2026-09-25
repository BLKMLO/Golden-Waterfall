package troglodyte

import "math"

// Modèle à TENDANCE LOCALE LINÉAIRE (Harvey, 1989, « Forecasting,
// Structural Time Series Models and the Kalman Filter », Cambridge
// University Press, ch. 2 et 3), sur y_t = ln(close bid) :
//
//	y_t     = μ_t + ε_t           ε_t ~ N(0, σ²_ε)   bruit d'observation
//	μ_{t+1} = μ_t + β_t + η_t     η_t ~ N(0, σ²_η)   niveau
//	β_{t+1} = β_t + ζ_t           ζ_t ~ N(0, σ²_ζ)   pente : LA tendance
//
// Le filtre de Kalman donne, à chaque bougie, la loi a posteriori de la
// pente β_t sachant les observations jusqu'à t — et rien d'après : il est
// causal par construction.

// params : les trois variances du modèle, en unités de ln(prix)².
type params struct {
	Eps  float64 `json:"sigma2_eps"`
	Eta  float64 `json:"sigma2_eta"`
	Zeta float64 `json:"sigma2_zeta"`
}

// state : moyenne et covariance (symétrique, 2×2) de (μ_t, β_t) sachant
// y_1..y_t.
type state struct {
	mu, beta      float64
	p11, p12, p22 float64
}

// initState : initialisation DIFFUSE exacte sur les deux premières
// observations.
//
// Avec un a priori plat sur (μ_1, β_1), deux observations déterminent
// l'état : μ_2 = y_2 − ε_2 et β_2 = y_2 − y_1 − ε_2 + ε_1 − η_1 + ζ_1.
// D'où la moyenne (y_2, y_2 − y_1) et la covariance
//
//	Var μ_2 = σ²_ε   Cov(μ_2, β_2) = σ²_ε   Var β_2 = 2σ²_ε + σ²_η + σ²_ζ
//
// C'est exact, là où l'artifice courant d'une variance initiale « très
// grande » perd des chiffres significatifs par soustraction.
func initState(y1, y2 float64, p params) state {
	return state{
		mu:   y2,
		beta: y2 - y1,
		p11:  p.Eps,
		p12:  p.Eps,
		p22:  2*p.Eps + p.Eta + p.Zeta,
	}
}

// step : prédiction puis mise à jour par l'observation y. Renvoie
// l'innovation v = y − ŷ et sa variance F, dont la vraisemblance est faite.
func (s *state) step(y float64, p params) (v, f float64) {
	// Prédiction : x ← T x, P ← T P Tᵀ + Q, avec T = [[1, 1], [0, 1]].
	mu := s.mu + s.beta
	beta := s.beta
	p11 := s.p11 + 2*s.p12 + s.p22 + p.Eta
	p12 := s.p12 + s.p22
	p22 := s.p22 + p.Zeta

	// Mise à jour : H = [1, 0], gain K = P Hᵀ / F.
	v = y - mu
	f = p11 + p.Eps
	k1, k2 := p11/f, p12/f
	s.mu = mu + k1*v
	s.beta = beta + k2*v
	// P ← P − K F Kᵀ, écrit sous la forme qui évite la soustraction de
	// deux grandeurs voisines pour p11 et p12 (p11 − p11²/F = p11·σ²_ε/F).
	s.p11 = p11 * p.Eps / f
	s.p12 = p12 * p.Eps / f
	s.p22 = p22 - k2*p12
	return v, f
}

// slopeZ : pente filtrée rapportée à son écart-type a posteriori,
// z = β̂_{t|t} / √P_ββ — le « t de Student » de la tendance.
func (s state) slopeZ() float64 {
	if s.p22 <= 0 {
		return math.NaN()
	}
	return s.beta / math.Sqrt(s.p22)
}

// filterWindow filtre les observations d'une fenêtre, de la première à la
// dernière, et renvoie l'état final. Au moins deux observations.
func filterWindow(y []float64, p params) state {
	s := initState(y[0], y[1], p)
	for _, obs := range y[2:] {
		s.step(obs, p)
	}
	return s
}

// concentrated : log-vraisemblance CONCENTRÉE en σ²_η, pour les rapports
// qε = σ²_ε/σ²_η et qζ = σ²_ζ/σ²_η (Harvey, 1989, § 3.4).
//
// Par décomposition de l'erreur de prédiction, sur m = n − 2 innovations
// (les deux premières observations servent à l'initialisation diffuse) :
//
//	ln L = −½ Σ_t [ ln(2π F_t) + v_t² / F_t ]
//
// Filtrer avec σ²_η = 1 donne F*_t = F_t / σ²_η ; σ²_η s'estime alors en
// forme fermée, σ̂²_η = (1/m) Σ v_t² / F*_t, et
//
//	ln L_c = −(m/2) (ln 2π + 1 + ln σ̂²_η) − ½ Σ_t ln F*_t
//
// Une dimension d'optimisation de moins, sans approximation.
//
// L'échelle est σ²_η, la variance du NIVEAU, et non σ²_ε : un prix de
// change ressemble à une marche aléatoire, dont le bruit d'observation est
// presque nul. Rapportées à σ²_ε (première version, jamais publiée), les
// deux autres variances partaient vers l'infini et butaient sur les bornes
// de recherche : σ²_ζ ne restait juste que parce que l'optimiseur
// compensait en déplaçant un σ²_ε que la vraisemblance détermine à peine,
// et le drapeau « borne atteinte » ne disait plus rien. Rapportée à σ²_η,
// une variance nulle est un rapport qui tend vers zéro : une solution au
// bord, légitime, testée (mle.go).
func concentrated(y []float64, qEps, qZeta float64) (logL, sigma2Eta float64) {
	m := len(y) - 2
	if m < 1 {
		return math.Inf(-1), math.NaN()
	}
	p := params{Eps: qEps, Eta: 1, Zeta: qZeta}
	s := initState(y[0], y[1], p)
	var sumLogF, sumV2F float64
	for _, obs := range y[2:] {
		v, f := s.step(obs, p)
		sumLogF += math.Log(f)
		sumV2F += v * v / f
	}
	sigma2Eta = sumV2F / float64(m)
	if !(sigma2Eta > 0) || math.IsInf(sigma2Eta, 0) {
		return math.Inf(-1), sigma2Eta
	}
	logL = -0.5*float64(m)*(math.Log(2*math.Pi)+1+math.Log(sigma2Eta)) - 0.5*sumLogF
	return logL, sigma2Eta
}
