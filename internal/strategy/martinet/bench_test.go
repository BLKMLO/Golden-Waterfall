package martinet

import (
	"context"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy/strategytest"
)

// Chaque décision relit sa fenêtre (ATR compris) : le coût d'un backtest
// est linéaire en (bougies × fenêtre). Ce banc dit ce que coûte une
// bougie.
func BenchmarkSetupAt(b *testing.B) {
	r := v10()
	series := strategytest.SeriesAt(data.M1, 4000, 1, 1.10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := r.window + i%(len(series)-r.window)
		r.setupAt(series[k+1-r.window:k+1], 3)
	}
}

// Calibrage complet (six points de grille) sur 20 000 bougies M5, environ
// trois mois et demi de marché.
func BenchmarkCalibrate(b *testing.B) {
	r := v10()
	series := strategytest.SeriesAt(data.M5, 20_000, 1, 1.10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := r.calibrate(context.Background(), series, data.M5.Duration()); err != nil {
			b.Fatal(err)
		}
	}
}
