package indicator

import (
	"math"
	"math/rand"
	"testing"
)

func benchSeries(n int) []float64 {
	rng := rand.New(rand.NewSource(1))
	out := make([]float64, n)
	v := 1.10
	for i := range out {
		v = math.Max(v+rng.NormFloat64()*0.0008, 0.5)
		out[i] = v
	}
	return out
}

// Les tailles reflètent l'usage réel : une année de M1 sur une paire forex
// vaut environ 372 000 bougies.
const benchBars = 400_000

func BenchmarkRollingMean20(b *testing.B) {
	x := benchSeries(benchBars)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RollingMean(x, 20)
	}
}

func BenchmarkRollingStd20(b *testing.B) {
	x := benchSeries(benchBars)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RollingStd(x, 20)
	}
}

func BenchmarkRollingMin20(b *testing.B) {
	x := benchSeries(benchBars)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RollingMin(x, 20)
	}
}

func BenchmarkRollingMax50(b *testing.B) {
	x := benchSeries(benchBars)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RollingMax(x, 50)
	}
}
