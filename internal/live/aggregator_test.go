package live

import (
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

func TestAggregatorClosesBarOnBucketChange(t *testing.T) {
	agg := NewAggregator(data.H1)
	base := time.Date(2024, 4, 2, 10, 0, 0, 0, time.UTC)

	if _, ok := agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.10, Ask: 1.1002, Time: base}); ok {
		t.Fatal("la toute première bougie ne peut pas déjà être close")
	}
	agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.12, Ask: 1.1202, Time: base.Add(10 * time.Minute)})
	agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.09, Ask: 1.0902, Time: base.Add(20 * time.Minute)})

	closed, ok := agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.11, Ask: 1.1102, Time: base.Add(time.Hour)})
	if !ok {
		t.Fatal("un tick du bucket suivant doit clore la bougie précédente")
	}
	if !closed.Time.Equal(base) {
		t.Fatalf("bougie close mal datée : %s", closed.Time)
	}
	if closed.BidOpen != 1.10 || closed.BidHigh != 1.12 || closed.BidLow != 1.09 || closed.BidClose != 1.09 {
		t.Fatalf("OHLC agrégé incorrect : %+v", closed)
	}
	if !closed.HasAsk() {
		t.Fatal("le côté ask doit être conservé")
	}
}

// TestAggregatorNeverInventsABar : sur un marché silencieux, la bougie
// reste ouverte. La clore sur une minuterie inventerait une bougie que le
// marché n'a pas produite.
func TestAggregatorNeverInventsABar(t *testing.T) {
	agg := NewAggregator(data.H1)
	base := time.Date(2024, 4, 2, 10, 0, 0, 0, time.UTC)
	agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.10, Time: base})
	for i := 1; i < 10; i++ {
		if _, ok := agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.10, Time: base.Add(time.Duration(i) * time.Minute)}); ok {
			t.Fatal("aucune bougie ne doit être close tant qu'on reste dans le bucket")
		}
	}
	if _, ok := agg.Current("EURUSD"); !ok {
		t.Fatal("la bougie en cours doit être consultable")
	}
}

func TestAggregatorIsolatesSymbols(t *testing.T) {
	agg := NewAggregator(data.H1)
	base := time.Date(2024, 4, 2, 10, 0, 0, 0, time.UTC)
	agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.10, Time: base})
	agg.Add(core.Tick{Symbol: "GBPUSD", Bid: 1.25, Time: base})

	closed, ok := agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.11, Time: base.Add(time.Hour)})
	if !ok || closed.BidClose != 1.10 {
		t.Fatalf("la bougie close doit être celle d'EURUSD : %+v", closed)
	}
	if cur, ok := agg.Current("GBPUSD"); !ok || cur.BidClose != 1.25 {
		t.Fatal("GBPUSD ne doit pas être affecté par un tick d'EURUSD")
	}
}

func TestAggregatorResetForgetsEverything(t *testing.T) {
	agg := NewAggregator(data.M15)
	agg.Add(core.Tick{Symbol: "EURUSD", Bid: 1.1, Time: time.Now()})
	agg.Reset()
	if _, ok := agg.Current("EURUSD"); ok {
		t.Fatal("après une reconnexion, aucune bougie partielle ne doit subsister")
	}
}

// --- Appariement des comptes rendus d'exécution ---------------------------

// TestSameSideFillsDoNotFabricateATrade : deux entrées du même sens sans
// sortie entre les deux ne forment PAS un aller-retour. Les apparier
// journaliserait un trade inexistant, avec un P&L calculé entre deux
// entrées — un chiffre qui aurait l'air d'un résultat.
func TestSameSideFillsDoNotFabricateATrade(t *testing.T) {
	h := newExecutionHarness(t)

	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.10, OrderID: "A", Time: time.Now().UTC(),
	})
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.12, OrderID: "B", Time: time.Now().UTC(),
	})

	trades, err := h.store.Trades(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Fatalf("aucun aller-retour ne doit être journalisé, reçu %d : %+v", len(trades), trades)
	}

	// La sortie qui suit doit s'apparier à la DERNIÈRE entrée connue.
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Sell, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.13, PnL: 10, Realized: true, Reason: "sortie", Time: time.Now().UTC(),
	})
	trades, _ = h.store.Trades(0)
	if len(trades) != 1 {
		t.Fatalf("%d trade(s) journalisé(s), 1 attendu", len(trades))
	}
	if trades[0].EntryPrice != 1.12 {
		t.Fatalf("la sortie doit s'apparier à la dernière entrée (1.12), reçu %v",
			trades[0].EntryPrice)
	}
}

func TestExecutionReportReleasesInFlight(t *testing.T) {
	h := newExecutionHarness(t)
	h.engine.mu.Lock()
	h.engine.inFlight["EURUSD"] = "A"
	h.engine.mu.Unlock()

	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1, Status: core.Rejected,
		OrderID: "A", Reason: "refusé", Time: time.Now().UTC(),
	})
	if len(h.engine.InFlight()) != 0 {
		t.Fatal("un REFUS est terminal : il doit libérer l'ordre en vol, sinon le symbole est gelé pour toujours")
	}
}

// TestUnreportedPnLIsNotComputed : quand le broker ne fournit pas de P&L,
// le programme ne le calcule PAS à sa place (frais, swap et conversion lui
// échappent). Le trade est journalisé avec un P&L nul et le motif le dit.
func TestUnreportedPnLIsNotComputed(t *testing.T) {
	h := newExecutionHarness(t)
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.10, OrderID: "A", Time: time.Now().UTC(),
	})
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Sell, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.20, Realized: false, Reason: "sortie", Time: time.Now().UTC(),
	})
	trades, _ := h.store.Trades(0)
	if len(trades) != 1 {
		t.Fatalf("%d trade(s)", len(trades))
	}
	if trades[0].PnL != 0 {
		t.Fatalf("un P&L non rapporté ne doit pas être inventé, reçu %v", trades[0].PnL)
	}
	if !strings.Contains(trades[0].ExitReason, "non rapporté") {
		t.Fatalf("le motif doit signaler l'absence de P&L : %q", trades[0].ExitReason)
	}
}

// TestExitWithoutKnownEntryIsNotAnEntry : une sortie dont le moteur n'a
// pas vu l'entrée (position ouverte avant le démarrage, stop d'une séance
// précédente) ne devient PAS une entrée. Sinon la sortie suivante
// journaliserait un trade entre deux ordres sans rapport.
func TestExitWithoutKnownEntryIsNotAnEntry(t *testing.T) {
	h := newExecutionHarness(t)
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Sell, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.10, PnL: -5, Realized: true, Closing: true, Reason: "stop", Time: time.Now().UTC(),
	})
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.12, OrderID: "B", Time: time.Now().UTC(),
	})
	h.engine.HandleExecution(core.ExecutionReport{
		Symbol: "EURUSD", Side: core.Sell, Quantity: 1000, Status: core.Filled,
		FillPrice: 1.13, PnL: 10, Realized: true, Closing: true, Reason: "sortie", Time: time.Now().UTC(),
	})
	trades, _ := h.store.Trades(0)
	if len(trades) != 1 || trades[0].EntryPrice != 1.12 {
		t.Fatalf("un seul trade attendu, entré à 1.12 : %+v", trades)
	}
}
