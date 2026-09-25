package troglodyte

import (
	"math"
	"math/rand"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/indicator"
)

// Implémentation NAÏVE : le modèle écrit comme un vecteur gaussien unique.
// Chaque grandeur (μ_t, β_t, y_t) est une combinaison linéaire des aléas
// indépendants u = (μ_1, β_1, η_1..η_{n−1}, ζ_1..ζ_{n−1}, ε_1..ε_n), avec
// un a priori N(0, κ) sur (μ_1, β_1), κ très grand pour approcher le
// diffus. Tout se calcule alors par conditionnement gaussien dense, sans
// aucune récursion — rien de commun avec le filtre qu'on vérifie.
type naiveModel struct {
	n       int
	varU    []float64
	mu, bet [][]float64 // coefficients de μ_t et β_t sur u
	y       [][]float64
}

const kappa = 1e7

func newNaive(n int, p params) naiveModel {
	dim := 2 + 2*(n-1) + n
	unit := func(k int) []float64 { v := make([]float64, dim); v[k] = 1; return v }
	add := func(a, b []float64) []float64 {
		out := make([]float64, dim)
		for i := range out {
			out[i] = a[i] + b[i]
		}
		return out
	}
	m := naiveModel{n: n, varU: make([]float64, dim)}
	m.varU[0], m.varU[1] = kappa, kappa
	for t := 0; t < n-1; t++ {
		m.varU[2+t] = p.Eta
		m.varU[2+(n-1)+t] = p.Zeta
	}
	for t := 0; t < n; t++ {
		m.varU[2+2*(n-1)+t] = p.Eps
	}
	mu, beta := unit(0), unit(1)
	for t := 0; t < n; t++ {
		m.mu = append(m.mu, mu)
		m.bet = append(m.bet, beta)
		m.y = append(m.y, add(mu, unit(2+2*(n-1)+t)))
		if t < n-1 {
			mu = add(add(mu, beta), unit(2+t))
			beta = add(beta, unit(2+(n-1)+t))
		}
	}
	return m
}

func (m naiveModel) cov(a, b []float64) float64 {
	s := 0.0
	for k := range a {
		s += a[k] * b[k] * m.varU[k]
	}
	return s
}

// solve résout A x = b par élimination de Gauss avec pivot partiel.
func solve(a [][]float64, b []float64) []float64 {
	n := len(b)
	m := make([][]float64, n)
	for i := range a {
		m[i] = append(append([]float64(nil), a[i]...), b[i])
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[p][c]) {
				p = r
			}
		}
		m[c], m[p] = m[p], m[c]
		for r := c + 1; r < n; r++ {
			f := m[r][c] / m[c][c]
			for k := c; k <= n; k++ {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		s := m[r][n]
		for k := r + 1; k < n; k++ {
			s -= m[r][k] * x[k]
		}
		x[r] = s / m[r][r]
	}
	return x
}

// posterior : moyenne et variance de β_n sachant y_1..y_n.
func (m naiveModel) posterior(y []float64) (beta, varBeta float64) {
	n := m.n
	sy := make([][]float64, n)
	for i := range sy {
		sy[i] = make([]float64, n)
		for j := range sy[i] {
			sy[i][j] = m.cov(m.y[i], m.y[j])
		}
	}
	c := make([]float64, n)
	for i := range c {
		c[i] = m.cov(m.bet[n-1], m.y[i])
	}
	w := solve(sy, y)
	for i := range c {
		beta += c[i] * w[i]
	}
	g := solve(sy, c)
	varBeta = m.cov(m.bet[n-1], m.bet[n-1])
	for i := range c {
		varBeta -= c[i] * g[i]
	}
	return beta, varBeta
}

// logDensity : ln N(y_{1:k} ; 0, Σ_k).
func (m naiveModel) logDensity(y []float64, k int) float64 {
	sy := make([][]float64, k)
	for i := range sy {
		sy[i] = make([]float64, k)
		for j := range sy[i] {
			sy[i][j] = m.cov(m.y[i], m.y[j])
		}
	}
	// Cholesky : Σ = L Lᵀ ; ln|Σ| = 2 Σ ln L_ii ; yᵀΣ⁻¹y = |L⁻¹y|².
	l := make([][]float64, k)
	for i := range l {
		l[i] = make([]float64, k)
		for j := 0; j <= i; j++ {
			s := sy[i][j]
			for p := 0; p < j; p++ {
				s -= l[i][p] * l[j][p]
			}
			if i == j {
				l[i][i] = math.Sqrt(s)
			} else {
				l[i][j] = s / l[j][j]
			}
		}
	}
	z := make([]float64, k)
	logDet, quad := 0.0, 0.0
	for i := 0; i < k; i++ {
		s := y[i]
		for p := 0; p < i; p++ {
			s -= l[i][p] * z[p]
		}
		z[i] = s / l[i][i]
		quad += z[i] * z[i]
		logDet += 2 * math.Log(l[i][i])
	}
	return -0.5 * (float64(k)*math.Log(2*math.Pi) + logDet + quad)
}

func sampleY(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	y := make([]float64, n)
	for i := range y {
		y[i] = 0.3*math.Sin(float64(i)/3) + rng.NormFloat64()
	}
	return y
}

func TestFilterMatchesDenseGaussianConditioning(t *testing.T) {
	p := params{Eps: 0.7, Eta: 0.4, Zeta: 0.05}
	for _, n := range []int{3, 5, 9} {
		y := sampleY(n, int64(n))
		got := filterWindow(y, p)
		wantBeta, wantVar := newNaive(n, p).posterior(y)
		if math.Abs(got.beta-wantBeta) > 1e-5 || math.Abs(got.p22-wantVar) > 1e-5 {
			t.Fatalf("n=%d : filtre (β=%v, P=%v), calcul dense (β=%v, P=%v)",
				n, got.beta, got.p22, wantBeta, wantVar)
		}
	}
}

// La vraisemblance DIFFUSE est celle de y_3..y_n sachant y_1, y_2 :
// ln p(y_{1:n}) − ln p(y_{1:2}) sous l'a priori κ, quand κ → ∞.
func TestLikelihoodMatchesDenseGaussianDensity(t *testing.T) {
	n := 9
	y := sampleY(n, 3)
	qEps, qZeta := 1.7, 0.03
	logL, sigma2 := concentrated(y, qEps, qZeta)
	// La vraisemblance concentrée, prise en σ̂²_η, vaut la vraisemblance
	// complète au point (qε σ̂², σ̂², qζ σ̂²).
	m := newNaive(n, params{Eps: qEps * sigma2, Eta: sigma2, Zeta: qZeta * sigma2})
	want := m.logDensity(y, n) - m.logDensity(y, 2)
	if math.Abs(logL-want) > 1e-4 {
		t.Fatalf("log-vraisemblance %v, calcul dense %v", logL, want)
	}
}

func TestWindowATRMatchesTheIndicator(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	s := make(core.Series, 300)
	price := 1.1
	for i := range s {
		o := price
		price += rng.NormFloat64() * 0.001
		s[i] = core.Bar{BidOpen: o, BidClose: price,
			BidHigh: math.Max(o, price) + rng.Float64()*0.0005,
			BidLow:  math.Min(o, price) - rng.Float64()*0.0005}
	}
	ref := indicator.ATR(s.Highs(), s.Lows(), s.Closes(), 14)
	if got, want := windowATR(s, 14), ref[len(ref)-1]; math.Abs(got-want) > 1e-15 {
		t.Fatalf("ATR de fenêtre %v, indicator.ATR %v", got, want)
	}
}
