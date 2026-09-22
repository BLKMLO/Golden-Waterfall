package risk

import (
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func newManager(cfg config.RiskConfig) *Manager {
	// USD : les tests utilisent des symboles dont la cotation est le
	// dollar, donc une conversion exacte et neutre.
	return New(cfg, "USD", slog.New(slog.DiscardHandler))
}

func baseConfig() config.RiskConfig {
	return config.RiskConfig{
		MaxPositionSize: 1_000_000, FixedPositionSize: 2,
		MaxPositionsPerSymbol: 1,
		MaxOpenPositions:      3, MaxDailyLossPct: 2,
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

// --- Dimensionnement au risque (risk_per_trade_pct > 0) ------------------

func sizingConfig(pct float64) config.RiskConfig {
	cfg := baseConfig()
	cfg.MaxPositionSize = 1_000_000 // plafond volontairement hors de portée
	cfg.RiskPerTradePct = pct
	return cfg
}

func entry(symbol string, price, stop float64) core.Signal {
	return core.Signal{Symbol: symbol, Action: core.EnterLong, Price: price, StopLoss: stop}
}

// TestRiskSizingKeepsTheLossConstant : c'est TOUT l'intérêt du réglage.
// Un stop deux fois plus loin doit donner une position deux fois plus
// petite, pour que la perte au stop reste le même montant.
func TestRiskSizingKeepsTheLossConstant(t *testing.T) {
	m := newManager(sizingConfig(1)) // 1 % de 10 000 = 100 USD de budget
	account := &core.AccountState{Equity: 10000}

	near := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, account)
	far := m.Evaluate(entry("EURUSD", 1.1000, 1.0800), nil, account)
	if !near.Accepted() || !far.Accepted() {
		t.Fatalf("les deux entrées doivent être acceptées (%s / %s)", near.Reason, far.Reason)
	}
	// budget 100 USD / distance 0,0100 = 10 000 unités ; / 0,0200 = 5 000.
	if got := near.Order.Quantity; got != 10000 {
		t.Fatalf("stop à 100 pips : %g unités, 10 000 attendues", got)
	}
	if got := far.Order.Quantity; got != 5000 {
		t.Fatalf("stop à 200 pips : %g unités, 5 000 attendues", got)
	}
	// La perte au stop est la même des deux côtés : 100 USD.
	nearLoss := near.Order.Quantity * (1.1000 - 1.0900)
	farLoss := far.Order.Quantity * (1.1000 - 1.0800)
	if math.Abs(nearLoss-farLoss) > 1e-6 {
		t.Fatalf("perte au stop %g vs %g : le risque doit être constant", nearLoss, farLoss)
	}
}

// TestRiskSizingIsCappedByMaxPositionSize : le plafond s'applique ET se
// COMPTE. Un plafond trop bas neutralise le dimensionnement au risque à
// chaque entrée ; sans compteur, l'écran continuerait d'annoncer « 0,5 %
// par trade » en toute bonne foi.
func TestRiskSizingIsCappedByMaxPositionSize(t *testing.T) {
	cfg := sizingConfig(50) // budget énorme, exprès
	cfg.MaxPositionSize = 10000
	m := newManager(cfg)
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, &core.AccountState{Equity: 100000})
	if !d.Accepted() {
		t.Fatalf("entrée refusée : %s", d.Reason)
	}
	if got := d.Order.Quantity; got != 10000 {
		t.Fatalf("%g unités : max_position_size reste un PLAFOND", got)
	}
	if !d.Capped {
		t.Fatal("une taille ramenée au plafond doit être signalée")
	}
	if m.Capped() != 1 {
		t.Fatalf("%d entrée(s) plafonnée(s) comptée(s), 1 attendue", m.Capped())
	}
}

// TestRiskSizingDisabledKeepsFixedSize : à 0, la taille vaut
// `fixed_position_size` — et surtout PAS `max_position_size`, qui n'est
// qu'une garde. Confondre les deux obligeait à relever le plafond pour
// activer le risque par trade, ce qui décuplait la taille fixe dès qu'on
// le désactivait.
func TestRiskSizingDisabledKeepsFixedSize(t *testing.T) {
	cfg := sizingConfig(0) // désactivé
	cfg.FixedPositionSize = 7777
	cfg.MaxPositionSize = 1_000_000
	m := newManager(cfg)
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, &core.AccountState{Equity: 10000})
	if !d.Accepted() || d.Order.Quantity != 7777 {
		t.Fatalf("à 0, la taille doit valoir fixed_position_size : %+v", d)
	}
	if d.Capped || m.Capped() != 0 {
		t.Fatal("rien n'a été plafonné ici")
	}
}

// TestFixedSizeIsStillCappedAndCounted : le plafond est une garde, pas un
// réglage du seul mode « risque ». Une taille fixe qui le dépasse est
// rabotée — et comptée, sinon le programme n'enverrait pas la taille
// demandée sans que rien ne le dise.
func TestFixedSizeIsStillCappedAndCounted(t *testing.T) {
	cfg := sizingConfig(0)
	cfg.FixedPositionSize = 50000
	cfg.MaxPositionSize = 20000
	m := newManager(cfg)
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, &core.AccountState{Equity: 10000})
	if !d.Accepted() || d.Order.Quantity != 20000 {
		t.Fatalf("la taille fixe doit être rabotée au plafond : %+v", d)
	}
	if !d.Capped || m.Capped() != 1 {
		t.Fatalf("le rabotage doit être compté : capped=%v total=%d", d.Capped, m.Capped())
	}
}

func TestRiskSizingRefusesWithoutEquity(t *testing.T) {
	m := newManager(sizingConfig(1))
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, nil)
	if d.Accepted() || d.Reason != ReasonNoEquity {
		t.Fatalf("sans équité, l'entrée doit être refusée et dire pourquoi : %+v", d)
	}
}

func TestRiskSizingRefusesWithoutStop(t *testing.T) {
	m := newManager(sizingConfig(1))
	d := m.Evaluate(core.Signal{Symbol: "EURUSD", Action: core.EnterLong, Price: 1.1},
		nil, &core.AccountState{Equity: 10000})
	if d.Accepted() || d.Reason != ReasonNoStop {
		t.Fatalf("sans stop, aucune distance à risquer : %+v", d)
	}
}

// TestRiskSizingRefusesUnconvertibleCurrency : EURGBP sur un compte en
// dollars exigerait un taux TIERS. On ne l'invente pas, donc on n'entre
// pas — le contraire risquerait un montant inconnu.
func TestRiskSizingRefusesUnconvertibleCurrency(t *testing.T) {
	m := newManager(sizingConfig(1))
	d := m.Evaluate(entry("EURGBP", 0.8500, 0.8400), nil, &core.AccountState{Equity: 10000})
	if d.Accepted() || d.Reason != ReasonUnconvertible {
		t.Fatalf("paire croisée non convertible : refus attendu, reçu %+v", d)
	}
}

func TestRiskSizingRefusesWhenBudgetBuysNothing(t *testing.T) {
	m := newManager(sizingConfig(0.001)) // 0,001 % de 100 = 0,001 USD
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, &core.AccountState{Equity: 100})
	if d.Accepted() || d.Reason != ReasonRiskBudgetSmall {
		t.Fatalf("budget trop petit pour une unité : refus attendu, reçu %+v", d)
	}
}

// TestRiskSizingConvertsWhenAccountIsTheBaseCurrency : compte en EUR sur
// EURUSD. La distance naît en dollars et doit être ramenée en euros au
// prix courant, sinon on risque 1,10 fois ce qu'on croit.
func TestRiskSizingConvertsWhenAccountIsTheBaseCurrency(t *testing.T) {
	m := New(sizingConfig(1), "EUR", slog.New(slog.DiscardHandler))
	d := m.Evaluate(entry("EURUSD", 1.1000, 1.0900), nil, &core.AccountState{Equity: 10000})
	if !d.Accepted() {
		t.Fatalf("entrée refusée : %s", d.Reason)
	}
	// budget 100 EUR ; unitaire = 0,0100 USD / 1,1000 = 0,00909… EUR
	// quantité = plancher(100 / 0,009090…) = 11 000.
	if got := d.Order.Quantity; got != 11000 {
		t.Fatalf("%g unités, 11 000 attendues (distance convertie en euros)", got)
	}
}

// TestSizingRefusalsSeparatesWhatItShould : un refus de DIMENSIONNEMENT
// ne se voit pas dans les chiffres — la stratégie paraît simplement
// muette. Le distinguer d'un refus normal (signal neutre, plafond de
// positions) est ce qui permet à l'écran de le dire.
func TestSizingRefusalsSeparatesWhatItShould(t *testing.T) {
	counts := map[string]int{
		ReasonHold:            900, // normal, et de loin le plus fréquent
		ReasonSymbolFull:      12,  // normal
		ReasonUnconvertible:   40,
		ReasonNoEquity:        7,
		ReasonRiskBudgetSmall: 1,
	}
	n, detail := SizingRefusals(counts)
	if n != 48 {
		t.Fatalf("%d refus de dimensionnement, 48 attendus (40 + 7 + 1)", n)
	}
	if strings.Contains(detail, ReasonHold) || strings.Contains(detail, ReasonSymbolFull) {
		t.Fatalf("un refus normal s'est glissé dans le détail : %s", detail)
	}
	// Du plus fréquent au moins fréquent : c'est l'ordre qui fait agir.
	if !strings.HasPrefix(detail, "40 × ") || !strings.HasSuffix(detail, "1 × "+ReasonRiskBudgetSmall) {
		t.Fatalf("détail mal ordonné : %s", detail)
	}

	if n, detail := SizingRefusals(map[string]int{ReasonHold: 5}); n != 0 || detail != "" {
		t.Fatalf("sans refus de dimensionnement, il n'y a rien à dire : %d %q", n, detail)
	}
}
