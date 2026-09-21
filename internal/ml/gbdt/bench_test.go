package gbdt

import (
	"context"
	"testing"
)

// BenchmarkTrain reproduit la forme d'un pli de walk-forward : quelques
// dizaines de milliers de lignes, 35 colonnes, 300 arbres.
func BenchmarkTrain(b *testing.B) {
	train, err := makeProblem(30000, 33, 1, true)
	if err != nil {
		b.Fatal(err)
	}
	p := DefaultParams()
	p.Threads = 4
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Train(context.Background(), train, nil, p, nil); err != nil {
			b.Fatal(err)
		}
	}
}
