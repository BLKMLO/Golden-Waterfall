package troglodyte

import (
	"context"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// Chaque décision refiltre sa fenêtre : le coût d'un backtest est donc
// linéaire en (bougies × fenêtre). Ce banc dit ce que coûte une bougie.
func BenchmarkOnBar(b *testing.B) {
	series := driftSeries(3000, 0.0002, 1)
	s := newTroglodyte(revisions[0])
	dir := b.TempDir()
	if _, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series[:1500]}, Timeframe: data.H4, OutputDir: dir,
	}); err != nil {
		b.Fatal(err)
	}
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{
		Symbol: "EURUSD", Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.OnBar(context.Background(), "EURUSD", series, 1500+i%1500); err != nil {
			b.Fatal(err)
		}
	}
}

// Estimation sur 10 000 bougies (environ six ans et demi de H4).
func BenchmarkEstimate(b *testing.B) {
	y := simulate(10_000, truth, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := estimate(y); err != nil {
			b.Fatal(err)
		}
	}
}

// v1_1 : normalisation et extrêmes en plus du filtre, à chaque décision.
func BenchmarkOnBarV11(b *testing.B) {
	series := driftSeries(3000, 0.0002, 1)
	s := newTroglodyte(v11())
	dir := b.TempDir()
	if _, err := s.Train(context.Background(), strategy.TrainRequest{
		Datasets: map[string]core.Series{"EURUSD": series[:1500]}, Timeframe: data.H4, OutputDir: dir,
	}); err != nil {
		b.Fatal(err)
	}
	if err := s.Warmup(context.Background(), strategy.WarmupRequest{Symbol: "EURUSD", Timeframe: data.H4, ModelDir: dir}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.OnBar(context.Background(), "EURUSD", series, 1500+i%1500); err != nil {
			b.Fatal(err)
		}
	}
}

// Entraînement complet de v1_1 sur 10 000 bougies : estimation, puis une
// fenêtre refiltrée par bougie pour le calibrage (le poste dominant).
func BenchmarkTrainV11(b *testing.B) {
	series := driftSeries(10_000, 0.0002, 1)
	s := newTroglodyte(v11())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Train(context.Background(), strategy.TrainRequest{
			Datasets: map[string]core.Series{"EURUSD": series}, Timeframe: data.H4, OutputDir: b.TempDir(),
		}); err != nil {
			b.Fatal(err)
		}
	}
}
