package live

import (
	"context"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// volumeGateway déclare — ou non — publier un volume.
type volumeGateway struct {
	*recordingGateway
	volume bool
}

func (g volumeGateway) Info() broker.Info {
	return broker.Info{Name: "test", SupportsBracket: true, SuppliesVolume: g.volume}
}

// seenStrategy note le volume de la bougie sur laquelle elle décide.
type seenStrategy struct {
	mu   *sync.Mutex
	seen *[]float64
}

func (seenStrategy) Describe() strategy.Description {
	return strategy.Description{Name: "vue", Version: "test", UsesVolume: true}
}
func (seenStrategy) Warmup(context.Context, strategy.WarmupRequest) error { return nil }
func (seenStrategy) Shutdown() error                                      { return nil }
func (seenStrategy) Ready() (bool, string)                                { return true, "" }
func (s seenStrategy) OnBar(_ context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	s.mu.Lock()
	*s.seen = append(*s.seen, series[i].Volume)
	s.mu.Unlock()
	return core.Signal{Symbol: symbol, Action: core.Hold}, nil
}

// TestLiveBarVolumeIsNaNWhenGatewayHasNone : IB ne publie pas de volume ;
// la somme de ses ticks vaut 0, ce qui dirait « aucun échange ». La
// stratégie doit lire NaN, « non mesuré ».
func TestLiveBarVolumeIsNaNWhenGatewayHasNone(t *testing.T) {
	for _, supplies := range []bool{false, true} {
		store, err := storage.Open(filepath.Join(t.TempDir(), "gw.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetTrading("TEST", true); err != nil {
			t.Fatal(err)
		}
		logger := slog.New(slog.DiscardHandler)
		cfg := config.Default()
		cfg.Risk.RiskPerTradePct = 0
		var mu sync.Mutex
		var seen []float64
		eng := NewEngine(volumeGateway{recordingGateway: &recordingGateway{}, volume: supplies},
			seenStrategy{mu: &mu, seen: &seen}, risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, logger),
			store, core.NewBus(), logger, data.H4)
		eng.SetEnabled(true)
		eng.onBarClosed(context.Background(), "TEST", core.Bar{
			Time:    time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC),
			BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100, Volume: 0,
		})
		store.Close()
		mu.Lock()
		if len(seen) != 1 {
			t.Fatalf("passerelle volume=%v : %d décisions", supplies, len(seen))
		}
		if got := seen[0]; math.IsNaN(got) == supplies {
			t.Fatalf("passerelle volume=%v : la stratégie a lu %v", supplies, got)
		}
		mu.Unlock()
	}
}

func TestGatewaysDeclareTheirVolume(t *testing.T) {
	want := map[string]bool{"replay": true, "interactive_brokers": false}
	for _, info := range broker.List() {
		if v, ok := want[info.Name]; ok && info.SuppliesVolume != v {
			t.Errorf("%s : SuppliesVolume = %v, %v attendu", info.Name, info.SuppliesVolume, v)
		}
	}
}
