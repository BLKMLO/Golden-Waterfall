package label

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// benchSeries fabrique une série M1 à volatilité RÉGLABLE.
//
// La volatilité est le paramètre qui compte pour ce calcul : plus le
// marché est calme, plus les barrières mettent de bougies à être touchées,
// et plus le balayage avant est long. Le cas « calme » est donc le pire
// cas, et c'est celui qu'il faut mesurer.
func benchSeries(n int, vol float64) core.Series {
	rng := rand.New(rand.NewSource(3))
	start := time.Date(2022, 1, 3, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, n)
	price := 1.10
	for i := range out {
		price = math.Max(price+rng.NormFloat64()*vol, 0.5)
		out[i] = core.Bar{
			Time:     start.Add(time.Duration(i) * time.Minute),
			BidOpen:  price,
			BidHigh:  price + vol,
			BidLow:   price - vol,
			BidClose: price,
			Volume:   10,
		}
	}
	return out
}

const labelBars = 120_000

func BenchmarkTripleBarrierAgite(b *testing.B) {
	s := benchSeries(labelBars, 0.0004)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Default(s)
	}
}

// Marché calme : les barrières sont rarement touchées, le balayage va donc
// jusqu'au bout de l'horizon de cinq jours (7 200 bougies M1).
func BenchmarkTripleBarrierCalme(b *testing.B) {
	s := benchSeries(labelBars, 0.000002)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Default(s)
	}
}
