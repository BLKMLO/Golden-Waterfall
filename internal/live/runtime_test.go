package live

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"

	_ "github.com/BLKMLO/Golden-Waterfall/internal/broker"
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

func setupRuntime(t *testing.T) (*Runtime, config.Config, *storage.Store) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths = config.Paths{ConfigDir: filepath.Join(root, "cfg"), DataDir: filepath.Join(root, "data")}
	if err := cfg.Paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg.Broker.Name = "replay"
	cfg.Broker.Symbols = []string{"EURUSD"}
	cfg.Broker.Timeframe = "M15"
	cfg.Broker.ReplaySpeed = 20000
	cfg.History.Instruments = []string{"EURUSD"}
	cfg.Strategy.Enabled = true

	// Historique M1 sur une journée : assez pour produire des bougies M15.
	start := time.Date(2023, 6, 5, 0, 0, 0, 0, time.UTC)
	var series core.Series
	price := 1.10
	for i := 0; i < 600; i++ {
		price += 0.00002
		series = append(series, core.Bar{
			Time:    start.Add(time.Duration(i) * time.Minute),
			BidOpen: price, BidHigh: price + 0.0003, BidLow: price - 0.0003, BidClose: price,
			AskOpen: price + 0.0001, AskHigh: price + 0.0004,
			AskLow: price - 0.0002, AskClose: price + 0.0001,
			Volume: 5,
		})
	}
	if err := data.WriteSeries(data.FilePath(cfg.Paths.HistoryDir(), "EURUSD", 2023),
		data.FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}

	store, err := storage.Open(cfg.Paths.DatabaseFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	logger := slog.New(slog.DiscardHandler)
	rt := NewRuntime(cfg, core.NewBus(), logger, store, risk.New(cfg.Risk, logger))
	t.Cleanup(rt.Disconnect)
	return rt, cfg, store
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("délai dépassé en attendant : %s", what)
}

func TestRuntimeConnectsAndAggregatesBars(t *testing.T) {
	rt, _, _ := setupRuntime(t)

	snap := rt.Snapshot()
	if snap.Connected {
		t.Fatal("aucune connexion ne doit exister avant Connect")
	}
	if !snap.Simulated {
		t.Fatal("la passerelle de rejeu doit être annoncée comme simulée dès le départ")
	}

	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !rt.Connected() {
		t.Fatal("la passerelle doit être connectée")
	}

	waitFor(t, "arrivée de prix", func() bool { return rt.Snapshot().Stats.Ticks > 0 })
	waitFor(t, "clôture d'une bougie M15", func() bool { return rt.Snapshot().Stats.Bars > 0 })

	snap = rt.Snapshot()
	if len(snap.Symbols) != 1 || snap.Symbols[0].Symbol != "EURUSD" {
		t.Fatalf("paires suivies incorrectes : %+v", snap.Symbols)
	}
	if !snap.Symbols[0].HasQuote || snap.Symbols[0].Bid <= 0 {
		t.Fatal("un prix doit être affiché une fois le flux ouvert")
	}
	if len(rt.Buffer("EURUSD")) == 0 {
		t.Fatal("le tampon de bougies doit se remplir")
	}
	// Sans modèle, la stratégie doit rester muette ET l'expliquer.
	if snap.StrategyReady {
		t.Fatal("aucun modèle n'a été entraîné : la stratégie ne peut pas être prête")
	}
	if snap.Symbols[0].ModelLoaded {
		t.Fatal("aucun modèle n'a été entraîné : la paire ne peut pas en déclarer un")
	}
	if snap.Symbols[0].Notice == "" {
		t.Fatal("l'absence de modèle doit être EXPLIQUÉE paire par paire")
	}

	rt.Disconnect()
	if rt.Connected() {
		t.Fatal("la déconnexion doit être effective")
	}
	if s := rt.Snapshot(); s.HasAccount {
		t.Fatal("hors connexion, AUCUNE donnée de compte ne doit subsister")
	}
}

func TestArmingRequiresConnection(t *testing.T) {
	rt, _, store := setupRuntime(t)

	if _, err := rt.ToggleSymbol("EURUSD"); err == nil {
		t.Fatal("armer une paire sans broker connecté doit être refusé")
	}
	if store.Trading("EURUSD") {
		t.Fatal("la paire ne doit pas avoir été armée")
	}

	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	on, err := rt.ToggleSymbol("EURUSD")
	if err != nil || !on {
		t.Fatalf("armement refusé alors que le broker est connecté : %v", err)
	}
	if !store.Trading("EURUSD") {
		t.Fatal("l'état armé doit être persisté")
	}
	// Le désarmement reste possible en toutes circonstances.
	rt.Disconnect()
	if on, err := rt.ToggleSymbol("EURUSD"); err != nil || on {
		t.Fatalf("désarmer doit toujours être possible : %v / %v", on, err)
	}
}

func TestKillSwitchTogglesOnlyWhenEngineExists(t *testing.T) {
	rt, cfg, _ := setupRuntime(t)
	if rt.ToggleKillSwitch() {
		t.Fatal("sans moteur, le kill-switch ne peut pas s'armer")
	}
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// La configuration l'arme au démarrage : le basculer le désarme.
	if !cfg.Strategy.Enabled {
		t.Fatal("prérequis du test : le kill-switch doit être armé par la configuration")
	}
	if rt.ToggleKillSwitch() {
		t.Fatal("le premier basculement doit DÉSARMER")
	}
	if !rt.ToggleKillSwitch() {
		t.Fatal("le second doit réarmer")
	}
}

func TestConnectTwiceIsClean(t *testing.T) {
	rt, _, _ := setupRuntime(t)
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Une reconnexion doit repartir proprement, sans empiler deux flux.
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "arrivée de prix après reconnexion", func() bool { return rt.Snapshot().Stats.Ticks > 0 })
	rt.Disconnect()
	rt.Disconnect() // appelable deux fois
}

func TestUnknownGatewayIsReported(t *testing.T) {
	rt, cfg, store := setupRuntime(t)
	cfg.Broker.Name = "inexistante"
	logger := slog.New(slog.DiscardHandler)
	rt = NewRuntime(cfg, core.NewBus(), logger, store, risk.New(cfg.Risk, logger))
	if err := rt.Connect(context.Background()); err == nil {
		t.Fatal("une passerelle inconnue doit faire échouer la connexion, clairement")
	}
}
