package broker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

func TestRegistryListsGateways(t *testing.T) {
	names := ListNames()
	want := map[string]bool{"replay": false, "interactive_brokers": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Fatalf("passerelle %q absente du registre : %v", n, names)
		}
	}
	if _, err := New("inexistante", Options{}); err == nil {
		t.Fatal("une passerelle inconnue doit être refusée")
	}
}

// TestSimulatedGatewayDeclaresItself : l'interface porte un bandeau REJEU
// à partir de ce drapeau. S'il mentait, l'utilisateur pourrait confondre
// un compte fictif avec un compte réel.
func TestSimulatedGatewayDeclaresItself(t *testing.T) {
	for _, info := range List() {
		switch info.Name {
		case "replay":
			if !info.Simulated {
				t.Fatal("la passerelle de rejeu DOIT se déclarer simulée")
			}
		case "interactive_brokers":
			if info.Simulated {
				t.Fatal("Interactive Brokers n'est pas une simulation")
			}
		}
	}
}

func writeReplayHistory(t *testing.T, dir, symbol string) {
	t.Helper()
	start := time.Date(2023, 6, 5, 0, 0, 0, 0, time.UTC)
	var series core.Series
	price := 1.10
	for i := 0; i < 240; i++ {
		price += 0.0001
		series = append(series, core.Bar{
			Time:    start.Add(time.Duration(i) * time.Minute),
			BidOpen: price, BidHigh: price + 0.0005, BidLow: price - 0.0005, BidClose: price,
			AskOpen: price + 0.0001, AskHigh: price + 0.0006,
			AskLow: price - 0.0004, AskClose: price + 0.0001,
			Volume: 10,
		})
	}
	if err := data.WriteSeries(data.FilePath(dir, symbol, 2023),
		data.FileHeader{Symbol: symbol, Year: 2023, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}
}

func TestReplayGatewayFullCycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	writeReplayHistory(t, dir, "EURUSD")

	gw, err := New("replay", Options{
		HistoryDir: dir, InitialCapital: 10000, Leverage: 30, Speed: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var ticks []core.Tick
	var reports []core.ExecutionReport
	gw.OnTick(func(tk core.Tick) {
		mu.Lock()
		ticks = append(ticks, tk)
		mu.Unlock()
	})
	gw.OnExecution(func(r core.ExecutionReport) {
		mu.Lock()
		reports = append(reports, r)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := gw.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer gw.Disconnect()
	if !gw.Connected() {
		t.Fatal("la passerelle doit se déclarer connectée")
	}
	if err := gw.Subscribe(ctx, []string{"EURUSD"}); err != nil {
		t.Fatal(err)
	}

	// Attente du premier prix, sans jamais dormir aveuglément.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(ticks)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("aucun prix rejoué en 5 s")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := gw.PlaceOrder(ctx, core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1000,
		Type: core.Market, StopLoss: 0.5, TakeProfit: 5,
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotEntry := len(reports) == 1 && reports[0].Status == core.Filled
	mu.Unlock()
	if !gotEntry {
		t.Fatal("un ordre exécuté DOIT produire un compte rendu — sans lui, aucun trade n'existe")
	}

	pos, err := gw.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || !pos[0].IsLong() {
		t.Fatalf("position attendue après exécution : %+v", pos)
	}

	acc, err := gw.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Equity <= 0 {
		t.Fatalf("équité simulée incohérente : %v", acc.Equity)
	}

	// Ordre inverse = fermeture, avec P&L RAPPORTÉ.
	if _, err := gw.PlaceOrder(ctx, core.OrderRequest{
		Symbol: "EURUSD", Side: core.Sell, Quantity: 1000, Type: core.Market,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	last := reports[len(reports)-1]
	mu.Unlock()
	if !last.Realized {
		t.Fatal("une sortie doit rapporter un P&L réalisé")
	}
	if pos, _ := gw.Positions(ctx); len(pos) != 0 {
		t.Fatalf("la position doit être fermée : %+v", pos)
	}
}

func TestReplayRejectsOrderWithoutPrice(t *testing.T) {
	gw, err := New("replay", Options{HistoryDir: t.TempDir(), InitialCapital: 1000, Leverage: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gw.Connect(ctx)
	defer gw.Disconnect()

	var reports []core.ExecutionReport
	gw.OnExecution(func(r core.ExecutionReport) { reports = append(reports, r) })
	if _, err := gw.PlaceOrder(ctx, core.OrderRequest{Symbol: "EURUSD", Side: core.Buy, Quantity: 1}); err == nil {
		t.Fatal("sans prix connu, l'ordre doit être refusé")
	}
	if len(reports) != 1 || reports[0].Status != core.Rejected {
		t.Fatal("un refus doit être RAPPORTÉ, pas silencieux")
	}
}

func TestReplayRefusesOrderWhenDisconnected(t *testing.T) {
	gw, _ := New("replay", Options{HistoryDir: t.TempDir(), InitialCapital: 1000, Leverage: 1})
	if _, err := gw.PlaceOrder(context.Background(), core.OrderRequest{Symbol: "EURUSD"}); err == nil {
		t.Fatal("aucun ordre ne peut partir d'une passerelle déconnectée")
	}
	if _, err := gw.Account(context.Background()); err == nil {
		t.Fatal("aucune donnée de compte hors connexion")
	}
}

func TestReplayDisconnectTwiceIsSafe(t *testing.T) {
	gw, _ := New("replay", Options{HistoryDir: t.TempDir(), InitialCapital: 1000, Leverage: 1})
	gw.Connect(context.Background())
	if err := gw.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Disconnect(); err != nil {
		t.Fatal("une séquence d'arrêt jouée deux fois ne doit pas échouer")
	}
}

// TestReplayStampsMarketTime : un rejeu de 2023 doit dater ses comptes
// rendus de 2023. Mélanger l'heure réelle (entrées) et l'heure de marché
// (sorties) produisait un journal incohérent et des durées de trade
// absurdes.
func TestReplayStampsMarketTime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	writeReplayHistory(t, dir, "EURUSD")

	gw, err := New("replay", Options{
		HistoryDir: dir, InitialCapital: 10000, Leverage: 30, Speed: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var reports []core.ExecutionReport
	var ticks int
	gw.OnTick(func(core.Tick) { mu.Lock(); ticks++; mu.Unlock() })
	gw.OnExecution(func(r core.ExecutionReport) {
		mu.Lock()
		reports = append(reports, r)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := gw.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer gw.Disconnect()
	if err := gw.Subscribe(ctx, []string{"EURUSD"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := ticks
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := gw.PlaceOrder(ctx, core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 100, Type: core.Market,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) == 0 {
		t.Fatal("aucun compte rendu")
	}
	if reports[0].Time.Year() != 2023 {
		t.Fatalf("le compte rendu doit porter l'heure du MARCHÉ rejoué (2023), reçu %s",
			reports[0].Time)
	}
}
