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
	"github.com/BLKMLO/Golden-Waterfall/internal/news"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// declaredStrategy émet `action` à chaque bougie et déclare les règles
// d'exécution qu'on lui donne.
type declaredStrategy struct {
	action            core.SignalAction
	weekend, reversal bool
	usesNews          bool
}

func (s declaredStrategy) Describe() strategy.Description {
	return strategy.Description{Name: "declaree", Version: "test",
		HoldsOverWeekend: s.weekend, ExitOnReversal: s.reversal, UsesNews: s.usesNews}
}
func (declaredStrategy) Warmup(context.Context, strategy.WarmupRequest) error { return nil }
func (declaredStrategy) Shutdown() error                                      { return nil }
func (declaredStrategy) Ready() (bool, string)                                { return true, "" }
func (s declaredStrategy) OnBar(_ context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	sig := core.Signal{Symbol: symbol, Action: s.action, Price: 100, Time: series[i].Time}
	switch s.action {
	case core.EnterLong:
		sig.StopLoss = 98
	case core.EnterShort:
		sig.StopLoss = 102
	}
	return sig, nil
}

// newDeclaredEngine : moteur armé, sur une passerelle qui porte les
// barrières ; `held` est la position que le courtier rapporte (0 = rien).
// Une position est aussi ENTRÉE au journal du moteur à `entry`.
func newDeclaredEngine(t *testing.T, strat strategy.Strategy, held float64, entry time.Time) (*Engine, *recordingGateway) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.SetTrading("TEST", true); err != nil {
		t.Fatal(err)
	}
	gw := &recordingGateway{}
	if held != 0 {
		gw.pos = []core.Position{{Symbol: "TEST", Quantity: held, AveragePrice: 100}}
	}
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()
	cfg.Risk.RiskPerTradePct = 0 // taille fixe : on mesure les règles, pas le dimensionnement
	eng := NewEngine(gw, strat, risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, logger),
		store, core.NewBus(), logger, data.H4)
	eng.SetEnabled(true)
	if held != 0 {
		side := core.Buy
		if held < 0 {
			side = core.Sell
		}
		eng.HandleExecution(core.ExecutionReport{
			Symbol: "TEST", OrderID: "ORD-0", Status: core.Filled,
			Side: side, Quantity: abs(held), FillPrice: 100, Time: entry,
		})
	}
	return eng, gw
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func tickAt(t time.Time) core.Tick {
	return core.Tick{Symbol: "TEST", Bid: 100, Ask: 100.01, Time: t}
}

// Semaine du 4 mars 2024, heure d'hiver à New York : clôture le vendredi
// 8 mars à 22 h UTC, garde de cinq minutes.
var weekOfMarch4 = time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)

func TestWeekendExitLeavesBeforeTheWeeklyClose(t *testing.T) {
	eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.Hold}, 1000, weekOfMarch4)
	ctx := context.Background()

	eng.HandleTick(ctx, tickAt(time.Date(2024, 3, 8, 21, 50, 0, 0, time.UTC)))
	if n := len(gw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) avant la garde : rien ne doit partir", n)
	}
	eng.HandleTick(ctx, tickAt(time.Date(2024, 3, 8, 21, 56, 0, 0, time.UTC)))
	orders := gw.placed()
	if len(orders) != 1 || orders[0].Side != core.Sell || orders[0].Quantity != 1000 {
		t.Fatalf("une sortie de la position rapportée attendue dans la garde : %+v", orders)
	}
	// Ordre en vol : pas de deuxième envoi au tick suivant.
	eng.HandleTick(ctx, tickAt(time.Date(2024, 3, 8, 21, 58, 0, 0, time.UTC)))
	if n := len(gw.placed()); n != 1 {
		t.Fatalf("%d ordres : la sortie ne doit partir qu'une fois", n)
	}
	if got := eng.Stats().WeekendExits; got != 1 {
		t.Fatalf("%d sortie(s) de fin de semaine comptée(s), 1 attendue", got)
	}
}

func TestWeekendExitHappensLateRatherThanNever(t *testing.T) {
	eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.Hold}, 1000, weekOfMarch4)
	// Aucun tick entre la garde et la clôture : le premier tick du
	// dimanche soir doit encore fermer la position.
	eng.HandleTick(context.Background(), tickAt(time.Date(2024, 3, 10, 22, 5, 0, 0, time.UTC)))
	if orders := gw.placed(); len(orders) != 1 || orders[0].Side != core.Sell {
		t.Fatalf("la position doit sortir au premier tick après la fermeture : %+v", orders)
	}
}

func TestHoldingStrategyKeepsItsPositionOverTheWeekend(t *testing.T) {
	eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.Hold, weekend: true}, 1000, weekOfMarch4)
	eng.HandleTick(context.Background(), tickAt(time.Date(2024, 3, 8, 21, 56, 0, 0, time.UTC)))
	if n := len(gw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) : une stratégie qui porte le week-end ne doit pas être sortie", n)
	}
}

func TestNoEntryOnTheLastBarBeforeTheWeekend(t *testing.T) {
	ctx := context.Background()
	lastBar := core.Bar{Time: time.Date(2024, 3, 8, 20, 0, 0, 0, time.UTC),
		BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100}

	eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.EnterLong}, 0, time.Time{})
	eng.onBarClosed(ctx, "TEST", lastBar)
	if n := len(gw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) : une entrée sur la dernière bougie serait portée pendant la fermeture", n)
	}
	if got := eng.Stats().WeekendSkipped; got != 1 {
		t.Fatalf("%d entrée(s) écartée(s) comptée(s), 1 attendue", got)
	}

	// La bougie précédente se clôt avant la garde : l'entrée part.
	earlier := lastBar
	earlier.Time = lastBar.Time.Add(-4 * time.Hour)
	eng.onBarClosed(ctx, "TEST", earlier)
	if n := len(gw.placed()); n != 1 {
		t.Fatalf("%d ordre(s), 1 entrée attendue sur une bougie ordinaire", n)
	}

	// Une stratégie qui porte le week-end entre aussi sur la dernière.
	holder, hgw := newDeclaredEngine(t, declaredStrategy{action: core.EnterLong, weekend: true}, 0, time.Time{})
	holder.onBarClosed(ctx, "TEST", lastBar)
	if n := len(hgw.placed()); n != 1 {
		t.Fatalf("%d ordre(s) : une stratégie qui porte le week-end peut entrer le vendredi", n)
	}
}

func TestReversalClosesOnlyWhenDeclared(t *testing.T) {
	ctx := context.Background()
	bar := core.Bar{Time: time.Date(2024, 3, 5, 8, 0, 0, 0, time.UTC),
		BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100}

	eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.EnterShort, reversal: true}, 1000, weekOfMarch4)
	eng.onBarClosed(ctx, "TEST", bar)
	orders := gw.placed()
	if len(orders) != 1 || orders[0].Side != core.Sell || orders[0].Quantity != 1000 || orders[0].StopLoss != 0 {
		t.Fatalf("un signal opposé doit FERMER la position longue, sans barrières : %+v", orders)
	}

	silent, sgw := newDeclaredEngine(t, declaredStrategy{action: core.EnterShort}, 1000, weekOfMarch4)
	silent.onBarClosed(ctx, "TEST", bar)
	if n := len(sgw.placed()); n != 0 {
		t.Fatalf("%d ordre(s) : sans ExitOnReversal, le signal opposé est ignoré tant que la position vit", n)
	}
}

type fixedNews struct{ gate *news.Gate }

func (f fixedNews) Gate() *news.Gate { return f.gate }

func TestLiveNewsFilterMatchesTheBacktestRule(t *testing.T) {
	events, err := news.ParseFeedJSON([]byte(`[{"title":"CPI","country":"USD","date":"2024-03-05T12:15:00Z","impact":"High"}]`))
	if err != nil {
		t.Fatal(err)
	}
	a := news.NewArchive(t.TempDir())
	if _, err := a.Save(news.Batch{Events: events, Weeks: news.WeeksOf(events)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cal, _ := a.Load()
	gate := fixedNews{&news.Gate{Calendar: cal, Before: 30 * time.Minute, After: 30 * time.Minute, MinImpact: news.ImpactHigh}}
	// Bougie H4 de 8 h : décision à sa clôture, 12 h ; annonce à 12 h 15.
	bar := core.Bar{Time: time.Date(2024, 3, 5, 8, 0, 0, 0, time.UTC), BidOpen: 1, BidHigh: 1, BidLow: 1, BidClose: 1}

	for _, declares := range []bool{true, false} {
		eng, gw := newDeclaredEngine(t, declaredStrategy{action: core.EnterLong, usesNews: declares}, 0, time.Time{})
		eng.SetNews(gate)
		if err := eng.store.SetTrading("EURUSD", true); err != nil {
			t.Fatal(err)
		}
		eng.onBarClosed(context.Background(), "EURUSD", bar)
		n := len(gw.placed())
		if declares && (n != 0 || eng.Stats().NewsBlocked != 1) {
			t.Fatalf("stratégie qui déclare le filtre : entrée écartée attendue (%d ordre(s), %d écartée(s))", n, eng.Stats().NewsBlocked)
		}
		if !declares && n != 1 {
			t.Fatalf("stratégie qui ne le déclare pas : l'entrée doit partir (%d ordre(s))", n)
		}
	}
}
