package live

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// executionHarness monte un moteur live minimal pour éprouver le chemin
// RETOUR (comptes rendus d'exécution → journal) sans passerelle réelle.
type executionHarness struct {
	engine *Engine
	store  *storage.Store
}

// stubGateway : passerelle inerte. Le chemin testé ici ne l'appelle pas ;
// elle existe pour satisfaire le contrat du moteur.
type stubGateway struct{ broker.Gateway }

func (stubGateway) Connected() bool { return true }

// muteStrategy : stratégie qui ne décide jamais rien.
type muteStrategy struct{}

func (muteStrategy) Describe() strategy.Description {
	return strategy.Description{Name: "muette", Version: "test"}
}
func (muteStrategy) Warmup(ctx context.Context, req strategy.WarmupRequest) error { return nil }
func (muteStrategy) Shutdown() error                                              { return nil }
func (muteStrategy) Ready() (bool, string)                                        { return false, "stratégie de test" }
func (muteStrategy) OnBar(ctx context.Context, symbol string, s core.Series, i int) (core.Signal, error) {
	return core.Signal{Symbol: symbol, Action: core.Hold}, nil
}

func newExecutionHarness(t *testing.T) *executionHarness {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()
	// Taille FIXE : ces tests portent sur les barrières et le compte
	// rendu d'exécution, pas sur le dimensionnement. Sur un symbole
	// fictif, le risque par trade refuserait toute entrée faute de
	// conversion de devise — ce qui est le comportement voulu, mais pas
	// ce qui est mesuré ici.
	cfg.Risk.RiskPerTradePct = 0
	engine := NewEngine(stubGateway{}, muteStrategy{}, risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, logger),
		store, core.NewBus(), logger, data.H4)
	return &executionHarness{engine: engine, store: store}
}
