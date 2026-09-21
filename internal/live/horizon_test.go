package live

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/label"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// recordingGateway rapporte une position ouverte et NOTE les ordres reçus.
type recordingGateway struct {
	broker.Gateway

	mu     sync.Mutex
	pos    []core.Position
	orders []core.OrderRequest
}

func (g *recordingGateway) Connected() bool { return true }

func (g *recordingGateway) Positions(context.Context) ([]core.Position, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]core.Position(nil), g.pos...), nil
}

func (g *recordingGateway) Account(context.Context) (core.AccountState, error) {
	return core.AccountState{Equity: 10000, Currency: "USD"}, nil
}

func (g *recordingGateway) PlaceOrder(_ context.Context, req core.OrderRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.orders = append(g.orders, req)
	return "ORD-1", nil
}

func (g *recordingGateway) placed() []core.OrderRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]core.OrderRequest(nil), g.orders...)
}

// newHorizonEngine monte un moteur armé, avec une position déjà ouverte et
// rapportée par le courtier.
func newHorizonEngine(t *testing.T, entry time.Time) (*Engine, *recordingGateway) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.SetTrading("TEST", true); err != nil {
		t.Fatal(err)
	}

	gw := &recordingGateway{pos: []core.Position{{Symbol: "TEST", Quantity: 1000, AveragePrice: 100}}}
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()
	// muteStrategy n'est JAMAIS prête : si une sortie part quand même,
	// c'est bien que l'horizon ne dépend pas du modèle.
	eng := NewEngine(gw, muteStrategy{}, risk.New(cfg.Risk, logger),
		store, core.NewBus(), logger, data.H4)
	eng.SetEnabled(true)

	// Compte rendu d'ENTRÉE : c'est lui, et lui seul, qui fait naître la
	// jambe ouverte que l'horizon mesure.
	eng.HandleExecution(core.ExecutionReport{
		Symbol: "TEST", OrderID: "ORD-0", Status: core.Filled,
		Side: core.Buy, Quantity: 1000, FillPrice: 100, Time: entry,
	})
	return eng, gw
}

func TestHorizonForcesExitEvenWhenStrategyIsNotReady(t *testing.T) {
	entry := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	eng, gw := newHorizonEngine(t, entry)

	// Bougie AU-DELÀ de l'horizon : la dernière à commencer dans la
	// fenêtre est celle qui précède l'échéance de MaxHoldDays.
	bar := core.Bar{Time: label.Deadline(entry), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100}
	eng.onBarClosed(context.Background(), "TEST", bar)

	orders := gw.placed()
	if len(orders) != 1 {
		t.Fatalf("%d ordre(s) soumis, 1 sortie attendue à l'échéance de l'horizon", len(orders))
	}
	if orders[0].Side != core.Sell || orders[0].Quantity != 1000 {
		t.Fatalf("la sortie doit fermer la position rapportée : %+v", orders[0])
	}
}

func TestBeforeHorizonNothingIsForced(t *testing.T) {
	entry := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	eng, gw := newHorizonEngine(t, entry)

	bar := core.Bar{Time: entry.Add(4 * time.Hour), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100}
	eng.onBarClosed(context.Background(), "TEST", bar)

	if n := len(gw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) soumis avant l'échéance : rien ne doit partir", n)
	}
}

func TestDisarmedKillSwitchForcesNothing(t *testing.T) {
	entry := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	eng, gw := newHorizonEngine(t, entry)
	eng.SetEnabled(false)

	bar := core.Bar{Time: label.Deadline(entry), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100}
	eng.onBarClosed(context.Background(), "TEST", bar)

	// « Kill-switch désarmé » doit vouloir dire « le programme ne touche
	// plus au compte », horizon dépassé ou non. Le journal le signale.
	if n := len(gw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) soumis kill-switch désarmé : le moteur ne doit plus rien envoyer", n)
	}
}

var _ strategy.Strategy = muteStrategy{}
