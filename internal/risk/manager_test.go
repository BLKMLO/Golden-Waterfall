package risk

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func newManager(cfg config.RiskConfig) *Manager {
	return New(cfg, slog.New(slog.DiscardHandler))
}

func baseConfig() config.RiskConfig {
	return config.RiskConfig{
		MaxPositionSize: 2, MaxPositionsPerSymbol: 1,
		MaxOpenPositions: 3, MaxDailyLossPct: 2,
	}
}

func TestHoldProducesNoOrder(t *testing.T) {
	m := newManager(baseConfig())
	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Hold}, nil, nil)
	if d.Accepted() {
		t.Fatal("un signal neutre ne doit jamais produire d'ordre")
	}
}

func TestEntryCarriesBarriersAndSize(t *testing.T) {
	m := newManager(baseConfig())
	d := m.Evaluate(core.Signal{
		Symbol: "EURUSD", Action: core.EnterLong, StopLoss: 1.07, TakeProfit: 1.09,
	}, nil, nil)
	if !d.Accepted() {
		t.Fatalf("entrée refusée sans raison : %s", d.Reason)
	}
	o := d.Order
	if o.Side != core.Buy || o.Quantity != 2 {
		t.Fatalf("ordre incorrect : %+v", o)
	}
	if o.StopLoss != 1.07 || o.TakeProfit != 1.09 {
		t.Fatal("les barrières de la stratégie doivent être reportées sur l'ordre")
	}
}

func TestShortEntryUsesSell(t *testing.T) {
	m := newManager(baseConfig())
	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterShort}, nil, nil)
	if !d.Accepted() || d.Order.Side != core.Sell {
		t.Fatalf("une entrée short doit produire un ordre SELL : %+v", d)
	}
}

func TestPerSymbolAndAccountCapsAreDistinct(t *testing.T) {
	// Le piège : confondre les deux plafonds gèle toutes les paires dès
	// qu'une seule position est ouverte.
	cfg := baseConfig()
	cfg.MaxPositionsPerSymbol = 1
	cfg.MaxOpenPositions = 3
	m := newManager(cfg)

	open := []core.Position{{Symbol: "EURUSD", Quantity: 2}}

	// Même symbole : bloqué par le plafond PAR SYMBOLE.
	if d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterLong}, open, nil); d.Accepted() {
		t.Fatal("une deuxième entrée sur la même paire doit être refusée")
	} else if d.Reason != ReasonSymbolFull {
		t.Fatalf("motif attendu %q, reçu %q", ReasonSymbolFull, d.Reason)
	}

	// Autre symbole : il reste de la place sur le COMPTE.
	if d := m.Evaluate(core.Signal{Symbol: "GBPUSD", Action: core.EnterLong}, open, nil); !d.Accepted() {
		t.Fatalf("une autre paire doit pouvoir trader (%s)", d.Reason)
	}
}

func TestAccountCapBlocksNewSymbols(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxOpenPositions = 2
	m := newManager(cfg)
	open := []core.Position{
		{Symbol: "EURUSD", Quantity: 1},
		{Symbol: "GBPUSD", Quantity: 1},
	}
	d := m.Evaluate(core.Signal{Symbol: "USDJPY", Action: core.EnterLong}, open, nil)
	if d.Accepted() || d.Reason != ReasonAccountFull {
		t.Fatalf("plafond du compte non appliqué : %+v", d)
	}
}

func TestExitIsNeverBlocked(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxOpenPositions = 1
	m := newManager(cfg)
	open := []core.Position{{Symbol: "EURUSD", Quantity: 2}}
	// Compte en perte bien au-delà du plafond : une SORTIE doit passer.
	account := &core.AccountState{Equity: 800, DayStartEquity: 1000}

	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Exit}, open, account)
	if !d.Accepted() {
		t.Fatalf("une sortie ne doit JAMAIS être bloquée par une limite (%s)", d.Reason)
	}
	if d.Order.Side != core.Sell || d.Order.Quantity != 2 {
		t.Fatalf("la sortie doit fermer exactement la position : %+v", d.Order)
	}
}

func TestExitOfShortBuysBack(t *testing.T) {
	m := newManager(baseConfig())
	open := []core.Position{{Symbol: "EURUSD", Quantity: -3}}
	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Exit}, open, nil)
	if !d.Accepted() || d.Order.Side != core.Buy || d.Order.Quantity != 3 {
		t.Fatalf("fermer un short = acheter la quantité absolue : %+v", d.Order)
	}
}

func TestExitWithoutPositionIsRejected(t *testing.T) {
	m := newManager(baseConfig())
	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Exit}, nil, nil)
	if d.Accepted() || d.Reason != ReasonNothingToClose {
		t.Fatalf("rien à fermer : %+v", d)
	}
}

func TestDailyLossBlocksEntriesOnly(t *testing.T) {
	m := newManager(baseConfig())                                    // plafond 2 %
	account := &core.AccountState{Equity: 970, DayStartEquity: 1000} // -3 %

	if d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterLong}, nil, account); d.Accepted() {
		t.Fatal("au-delà de la perte journalière, plus aucune ENTRÉE")
	} else if d.Reason != ReasonDailyLoss {
		t.Fatalf("motif attendu %q, reçu %q", ReasonDailyLoss, d.Reason)
	}
}

func TestDailyLossIgnoredWithoutAccount(t *testing.T) {
	// En backtest il n'y a pas d'équité réelle : la règle ne s'applique
	// pas, et on ne fait SURTOUT pas semblant de l'appliquer.
	m := newManager(baseConfig())
	if d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterLong}, nil, nil); !d.Accepted() {
		t.Fatalf("sans état de compte, la limite journalière ne doit pas bloquer (%s)", d.Reason)
	}
}

func TestDailyLossDisabledWhenZero(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxDailyLossPct = 0
	m := newManager(cfg)
	account := &core.AccountState{Equity: 500, DayStartEquity: 1000}
	if d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterLong}, nil, account); !d.Accepted() {
		t.Fatalf("plafond à 0 = limite désactivée (%s)", d.Reason)
	}
}

func TestRejectionsAreCounted(t *testing.T) {
	m := newManager(baseConfig())
	m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Hold}, nil, nil)
	m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Hold}, nil, nil)
	m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Exit}, nil, nil)
	counts := m.Rejections()
	if counts[ReasonHold] != 2 {
		t.Fatalf("2 rejets « neutre » attendus, reçu %d", counts[ReasonHold])
	}
	if counts[ReasonNothingToClose] != 1 {
		t.Fatalf("1 rejet « rien à fermer » attendu, reçu %d", counts[ReasonNothingToClose])
	}
}

// TestConcurrentEvaluateIsSafe : régression.
//
// Un SEUL Manager est câblé dans app.New puis partagé par le backtest, le
// walk-forward (plis parallèles) et le moteur live (une goroutine de rejeu
// par symbole). Les compteurs de rejet étaient écrits sans verrou : le
// détecteur de concurrence le signalait, et le runtime pouvait tuer le
// programme sur « concurrent map writes » pendant un entraînement ou une
// séance. Ce test échoue sous `go test -race` si le verrou disparaît.
func TestConcurrentEvaluateIsSafe(t *testing.T) {
	m := newManager(baseConfig())
	const goroutines, iterations = 4, 500

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.Hold}, nil, nil)
				m.Rejections()
			}
		}()
	}
	wg.Wait()

	if got := m.Rejections()[ReasonHold]; got != goroutines*iterations {
		t.Fatalf("%d rejets comptés, %d attendus", got, goroutines*iterations)
	}
}
