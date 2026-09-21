package backtest

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// scriptedStrategy joue un scénario prédéfini : elle renvoie le signal
// associé à l'indice de bougie. Elle permet d'éprouver le MOTEUR sans
// dépendre d'un modèle entraîné.
type scriptedStrategy struct {
	script map[int]core.Signal
}

func (s *scriptedStrategy) Describe() strategy.Description {
	return strategy.Description{Name: "scriptee", Version: "test"}
}
func (s *scriptedStrategy) Warmup(context.Context, strategy.WarmupRequest) error { return nil }
func (s *scriptedStrategy) Shutdown() error                                      { return nil }
func (s *scriptedStrategy) Ready() (bool, string)                                { return true, "" }
func (s *scriptedStrategy) OnBar(_ context.Context, symbol string, _ core.Series, i int) (core.Signal, error) {
	if sig, ok := s.script[i]; ok {
		sig.Symbol = symbol
		return sig, nil
	}
	return core.Signal{Symbol: symbol, Action: core.Hold}, nil
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Backtest.InitialCapital = 10000
	cfg.Backtest.Leverage = 30
	cfg.Risk.MaxPositionSize = 1000
	cfg.Risk.MaxPositionsPerSymbol = 1
	cfg.Risk.MaxOpenPositions = 1
	cfg.Costs.CommissionPerUnit = 0
	// Les tests du moteur travaillent sur des symboles fictifs ("TEST") :
	// on les veut exactement convertibles pour isoler ce qui est mesuré.
	cfg.Backtest.AccountCurrency = "USD"
	return cfg
}

func newEngine(cfg config.Config) *Engine {
	return NewEngine(cfg, risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, slog.New(slog.DiscardHandler)))
}

// makeBars construit une série horaire depuis un lundi, sans côté ask
// (donc sans coût modélisé) sauf si spread > 0.
func makeBars(ohlc [][4]float64, spread float64) core.Series {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // lundi
	out := make(core.Series, len(ohlc))
	for i, v := range ohlc {
		b := core.Bar{
			Time:    start.Add(time.Duration(i) * time.Hour),
			BidOpen: v[0], BidHigh: v[1], BidLow: v[2], BidClose: v[3], Volume: 1,
		}
		if spread > 0 {
			b.AskOpen, b.AskHigh = v[0]+spread, v[1]+spread
			b.AskLow, b.AskClose = v[2]+spread, v[3]+spread
		}
		out[i] = b
	}
	return out
}

func TestTakeProfitFillsAtExactBarrier(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100}, // entrée au close = 100, TP 102, SL 98
		{100, 103, 99, 101},  // franchit le TP
		{101, 101, 101, 101},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s), 1 attendu", len(res.Trades))
	}
	tr := res.Trades[0]
	if tr.ExitPrice != 102 {
		t.Fatalf("la barrière doit être remplie au PRIX EXACT : %v", tr.ExitPrice)
	}
	if tr.ExitReason != "tp" {
		t.Fatalf("motif de sortie %q", tr.ExitReason)
	}
	if want := (102 - 100) * cfg.Risk.MaxPositionSize; math.Abs(tr.PnL-want) > 1e-9 {
		t.Fatalf("P&L %v, attendu %v (aucun coût sans côté ask)", tr.PnL, want)
	}
}

// TestBothBarriersInSameBarPrefersStop verrouille la convention
// conservatrice du moteur — la même que celle du labeling.
func TestBothBarriersInSameBarPrefersStop(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 105, 95, 104}, // franchit TP (102) ET SL (98)
		{104, 104, 104, 104},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].ExitReason != "sl" {
		t.Fatalf("les deux barrières franchies → le STOP l'emporte : %+v", res.Trades)
	}
}

func TestShortBarriersAreMirrored(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 101, 97, 98}, // TP short à 98
		{98, 98, 98, 98},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterShort, TakeProfit: 98, StopLoss: 102},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s)", len(res.Trades))
	}
	tr := res.Trades[0]
	if tr.ExitPrice != 98 || tr.ExitReason != "tp" {
		t.Fatalf("sortie short incorrecte : %+v", tr)
	}
	if want := (100 - 98) * cfg.Risk.MaxPositionSize; math.Abs(tr.PnL-want) > 1e-9 {
		t.Fatalf("un short gagne quand le prix BAISSE : P&L %v, attendu %v", tr.PnL, want)
	}
}

func TestNoEntryOnBarBeforeItExists(t *testing.T) {
	// Une barrière ne peut pas être franchie par la bougie d'ENTRÉE
	// elle-même : l'ordre est rempli à son close.
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 110, 90, 100}, // amplitude énorme, mais c'est la bougie d'entrée
		{100, 100, 100, 100},
		{100, 100, 100, 100},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s)", len(res.Trades))
	}
	if res.Trades[0].ExitReason != "final" {
		t.Fatalf("aucune barrière ne doit se déclencher sur la bougie d'entrée : %+v", res.Trades[0])
	}
}

func TestWeekendCloseHappens(t *testing.T) {
	cfg := testConfig()
	// Vendredi 5 janvier 2024 22:00 puis lundi 8 janvier 00:00 : la
	// bougie du vendredi est la dernière de sa semaine ISO.
	series := core.Series{
		{Time: time.Date(2024, 1, 5, 20, 0, 0, 0, time.UTC), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100},
		{Time: time.Date(2024, 1, 5, 21, 0, 0, 0, time.UTC), BidOpen: 100, BidHigh: 101, BidLow: 99, BidClose: 100.5},
		{Time: time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100},
		{Time: time.Date(2024, 1, 8, 1, 0, 0, 0, time.UTC), BidOpen: 100, BidHigh: 100, BidLow: 100, BidClose: 100},
	}
	strat := &scriptedStrategy{script: map[int]core.Signal{
		0: {Action: core.EnterLong, TakeProfit: 200, StopLoss: 1},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) == 0 {
		t.Fatal("aucun trade : la clôture de fin de semaine n'a pas eu lieu")
	}
	if res.Trades[0].ExitReason != "weekend" {
		t.Fatalf("la position doit être fermée avant le week-end, motif reçu %q",
			res.Trades[0].ExitReason)
	}
	if !res.Trades[0].ExitTime.Equal(series[1].Time) {
		t.Fatalf("clôture à la MAUVAISE bougie : %s", res.Trades[0].ExitTime)
	}
}

func TestFinalLiquidation(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 100, 100, 101},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		0: {Action: core.EnterLong, TakeProfit: 500, StopLoss: 1},
	}}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if len(res.Trades) != 1 || res.Trades[0].ExitReason != "final" {
		t.Fatalf("toute position résiduelle doit être liquidée à la fin : %+v", res.Trades)
	}
}

func TestSpreadIsMeasuredAndCharged(t *testing.T) {
	cfg := testConfig()
	const spread = 0.0002
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 103, 99, 101},
		{101, 101, 101, 101},
	}, spread)

	measured, ok := MeasureSpread(series)
	if !ok {
		t.Fatal("le spread doit être mesurable quand le côté ask est présent")
	}
	if math.Abs(measured-spread) > 1e-12 {
		t.Fatalf("spread mesuré %v, attendu %v", measured, spread)
	}

	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if !res.Stats.CostsModelled {
		t.Fatal("les coûts doivent être déclarés modélisés")
	}
	// Un aller-retour paie EXACTEMENT un spread par unité.
	wantCost := spread * cfg.Risk.MaxPositionSize
	if math.Abs(res.Trades[0].Cost-wantCost) > 1e-9 {
		t.Fatalf("coût de l'aller-retour %v, attendu un spread complet %v",
			res.Trades[0].Cost, wantCost)
	}
	grossPnL := (102 - 100) * cfg.Risk.MaxPositionSize
	if math.Abs(res.Trades[0].PnL-(grossPnL-wantCost)) > 1e-9 {
		t.Fatalf("le P&L doit être NET de coûts : %v", res.Trades[0].PnL)
	}
}

func TestNoAskSideMeansNoCostsAndItIsSaid(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{{100, 100, 100, 100}, {100, 100, 100, 100}}, 0)
	if _, ok := MeasureSpread(series); ok {
		t.Fatal("sans côté ask, aucun spread ne peut être mesuré")
	}
	strat := &scriptedStrategy{}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if res.Stats.CostsModelled {
		t.Fatal("le résultat doit SIGNALER qu'aucun coût n'est modélisé")
	}
	if res.Stats.Costs != 0 {
		t.Fatalf("aucun coût ne peut être facturé : %v", res.Stats.Costs)
	}
}

func TestCommissionIsChargedPerSide(t *testing.T) {
	cfg := testConfig()
	cfg.Costs.CommissionPerUnit = 0.001
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 103, 99, 101},
		{101, 101, 101, 101},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	want := 2 * 0.001 * cfg.Risk.MaxPositionSize // deux côtés
	if math.Abs(res.Trades[0].Cost-want) > 1e-9 {
		t.Fatalf("commission de l'aller-retour %v, attendue %v", res.Trades[0].Cost, want)
	}
}

func TestInsufficientMarginIsCountedNotSilent(t *testing.T) {
	cfg := testConfig()
	cfg.Backtest.InitialCapital = 100
	cfg.Backtest.Leverage = 1 // compte cash strict
	cfg.Risk.MaxPositionSize = 1000
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 100, 100, 100},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 200, StopLoss: 1},
	}}
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if res.Stats.RejectedOrders == 0 {
		t.Fatal("un refus de marge doit être COMPTÉ, pas confondu avec une abstention")
	}
	if len(res.Trades) != 0 {
		t.Fatal("aucun trade ne peut naître d'un ordre refusé")
	}
}

func TestEquityCurveStartsAtInitialCapital(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100}, {100, 100, 100, 100}, {100, 100, 100, 100},
	}, 0)
	res, _ := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: &scriptedStrategy{}, Timeframe: data.H1,
	})
	if len(res.Equity) == 0 {
		t.Fatal("courbe d'équité vide")
	}
	if res.Equity[0].Value != cfg.Backtest.InitialCapital {
		t.Fatalf("la courbe est la VALEUR DU COMPTE : elle part de %v, reçu %v",
			cfg.Backtest.InitialCapital, res.Equity[0].Value)
	}
}

func TestDownsampleKeepsEnds(t *testing.T) {
	points := make([]EquityPoint, 1000)
	for i := range points {
		points[i] = EquityPoint{Value: float64(i)}
	}
	got := Downsample(points, 50)
	if len(got) != 50 {
		t.Fatalf("%d points après réduction, 50 attendus", len(got))
	}
	if got[0].Value != 0 || got[len(got)-1].Value != 999 {
		t.Fatal("la réduction doit conserver le premier et le dernier point")
	}
}

func TestAggregateRecomputesRatiosFromRawTrades(t *testing.T) {
	// Deux blocs de tailles très différentes : moyenner leurs taux de
	// gain donnerait un chiffre qui ne correspond à aucun portefeuille.
	a := &Result{Trades: []core.Trade{{PnL: 10}, {PnL: 10}, {PnL: 10}, {PnL: -5}},
		Stats: Stats{CostsModelled: true}}
	b := &Result{Trades: []core.Trade{{PnL: -20}}, Stats: Stats{CostsModelled: true}}
	agg := AggregateStats([]*Result{a, b}, 1000)
	if agg.Trades != 5 || agg.Wins != 3 {
		t.Fatalf("agrégat incorrect : %d trades, %d gagnants", agg.Trades, agg.Wins)
	}
	if math.Abs(agg.WinRate-60) > 1e-9 {
		t.Fatalf("taux de gain agrégé %v %%, attendu 60 %% (3/5)", agg.WinRate)
	}
	if math.Abs(agg.NetPnL-5) > 1e-9 {
		t.Fatalf("P&L agrégé %v, attendu 5", agg.NetPnL)
	}
	if math.Abs(agg.ProfitFactor-30.0/25.0) > 1e-9 {
		t.Fatalf("profit factor %v, attendu %v", agg.ProfitFactor, 30.0/25.0)
	}
}

func TestProfitFactorInfiniteWithoutLoss(t *testing.T) {
	r := &Result{Trades: []core.Trade{{PnL: 5}, {PnL: 3}}, Stats: Stats{CostsModelled: true}}
	agg := AggregateStats([]*Result{r}, 1000)
	if !math.IsInf(agg.ProfitFactor, 1) {
		t.Fatalf("sans aucune perte, le profit factor est INFINI, reçu %v", agg.ProfitFactor)
	}
}

func TestStatsJSONKeepsNonFiniteMetrics(t *testing.T) {
	cases := []Stats{
		{ProfitFactor: math.Inf(1), Sharpe: math.NaN(), SQN: math.NaN()},
		{ProfitFactor: math.NaN(), Sharpe: 1.5, SQN: -0.25},
		{ProfitFactor: 2.5, Sharpe: math.Inf(-1), SQN: 3},
	}
	for _, want := range cases {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("les métriques non finies doivent être SÉRIALISABLES : %v", err)
		}
		var got Stats
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		same := func(a, b float64, name string) {
			t.Helper()
			if math.IsNaN(a) != math.IsNaN(b) || (!math.IsNaN(a) && a != b) {
				t.Fatalf("%s : %v relu %v", name, a, b)
			}
		}
		same(want.ProfitFactor, got.ProfitFactor, "profit factor")
		same(want.Sharpe, got.Sharpe, "sharpe")
		same(want.SQN, got.SQN, "sqn")
	}
}

func TestStatsJSONKeepsOrdinaryFields(t *testing.T) {
	want := Stats{Symbol: "EURUSD", Trades: 12, WinRate: 58.5, NetPnL: 120.25,
		CostsModelled: true, ExitReasons: map[string]int{"tp": 7}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Stats
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Symbol != want.Symbol || got.Trades != want.Trades ||
		got.WinRate != want.WinRate || got.NetPnL != want.NetPnL ||
		!got.CostsModelled || got.ExitReasons["tp"] != 7 {
		t.Fatalf("champs ordinaires perdus : %+v", got)
	}
}

// TestCurrencyConversionUnblocksQuoteHeavyPairs : sur une paire dont la
// cotation n'est pas la devise du compte (USDJPY sur un compte en USD), le
// notionnel naît en yens. Sans conversion, la marge exigée était ~150 fois
// trop grande et TOUTES les entrées étaient refusées.
func TestCurrencyConversionUnblocksQuoteHeavyPairs(t *testing.T) {
	cfg := testConfig()
	cfg.Backtest.AccountCurrency = "USD"
	cfg.Backtest.InitialCapital = 10000
	cfg.Backtest.Leverage = 30
	cfg.Risk.MaxPositionSize = 10000

	// USDJPY autour de 150 : 10 000 unités = 1,5 M JPY de notionnel, mais
	// seulement 10 000 USD — soit 333 USD de marge à 30×.
	series := makeBars([][4]float64{
		{150, 150, 150, 150},
		{150, 150, 150, 150},
		{150, 152, 149, 151},
		{151, 151, 151, 151},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 151.5, StopLoss: 148.5},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "USDJPY", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.RejectedOrders != 0 {
		t.Fatalf("%d ordre(s) refusé(s) : la conversion de devise n'est pas appliquée",
			res.Stats.RejectedOrders)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s) : l'entrée aurait dû passer", len(res.Trades))
	}
	if !res.Stats.CurrencyExact || res.Stats.Currency != "USD" {
		t.Fatalf("USDJPY sur un compte USD est exactement convertible : %+v",
			res.Stats.Currency)
	}
	// P&L en dollars : un gain de 1 JPY par unité sur 10 000 unités vaut
	// 10 000 JPY, soit ~66 USD au cours de 151.
	want := (151.5 - 150) * 10000 / 151.5
	if math.Abs(res.Trades[0].PnL-want) > 1 {
		t.Fatalf("P&L %v, attendu ~%v USD (converti depuis les yens)", res.Trades[0].PnL, want)
	}
}

func TestQuoteCurrencyAccountNeedsNoConversion(t *testing.T) {
	cfg := testConfig()
	cfg.Backtest.AccountCurrency = "USD"
	cfg.Risk.MaxPositionSize = 10000
	series := makeBars([][4]float64{
		{1.10, 1.10, 1.10, 1.10},
		{1.10, 1.10, 1.10, 1.10},
		{1.10, 1.13, 1.09, 1.12},
		{1.12, 1.12, 1.12, 1.12},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 1.12, StopLoss: 1.08},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "EURUSD", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s)", len(res.Trades))
	}
	want := (1.12 - 1.10) * 10000
	if math.Abs(res.Trades[0].PnL-want) > 1e-6 {
		t.Fatalf("P&L %v, attendu %v (aucune conversion nécessaire)", res.Trades[0].PnL, want)
	}
	if !res.Stats.CurrencyExact {
		t.Fatal("EURUSD sur un compte USD est exactement convertible")
	}
}

// TestCrossPairIsFlaggedNotFaked : une croisée EURGBP sur un compte en
// dollars exigerait un taux tiers. Le programme n'en a pas — il le DIT au
// lieu d'inventer une conversion.
func TestCrossPairIsFlaggedNotFaked(t *testing.T) {
	cfg := testConfig()
	cfg.Backtest.AccountCurrency = "USD"
	series := makeBars([][4]float64{
		{0.85, 0.85, 0.85, 0.85}, {0.85, 0.85, 0.85, 0.85}, {0.85, 0.85, 0.85, 0.85},
	}, 0)
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "EURGBP", Series: series, From: 0, Strategy: &scriptedStrategy{}, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.CurrencyExact {
		t.Fatal("EURGBP sur un compte USD n'est PAS convertible sans taux tiers")
	}
	if res.Stats.Currency != "GBP" {
		t.Fatalf("le résultat doit être annoncé en devise de cotation (GBP), reçu %q",
			res.Stats.Currency)
	}
}

func TestAggregateFlagsMixedCurrencies(t *testing.T) {
	a := &Result{Stats: Stats{CostsModelled: true, CurrencyExact: true, Currency: "USD"}}
	b := &Result{Stats: Stats{CostsModelled: true, CurrencyExact: false, Currency: "GBP"}}
	agg := AggregateStats([]*Result{a, b}, 1000)
	if agg.CurrencyExact {
		t.Fatal("un seul actif non convertible rend l'agrégat approximatif : il faut le dire")
	}
}

func TestStopFillsAtGapOpenNotAtBarrier(t *testing.T) {
	cfg := testConfig()
	// La bougie 2 OUVRE à 95, très en dessous du stop à 98 : aucun
	// courtier n'aurait servi 98. Le remplissage doit être 95.
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100}, // entrée au close = 100, SL 98, TP 102
		{95, 96, 94, 95},     // gap SOUS le stop
		{95, 95, 95, 95},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].ExitReason != "sl" {
		t.Fatalf("un stop touché est attendu : %+v", res.Trades)
	}
	if got := res.Trades[0].ExitPrice; got != 95 {
		t.Fatalf("stop rempli à %v : un gap se paie à l'OUVERTURE (95), pas au prix demandé (98)", got)
	}
}

func TestShortStopFillsAtGapOpen(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100}, // entrée short au close = 100, SL 102, TP 98
		{105, 106, 104, 105}, // gap AU-DESSUS du stop
		{105, 105, 105, 105},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterShort, TakeProfit: 98, StopLoss: 102},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].ExitPrice != 105 {
		t.Fatalf("stop d'un short rempli à l'ouverture (105) attendu : %+v", res.Trades)
	}
}

func TestTakeProfitNeverBenefitsFromGap(t *testing.T) {
	cfg := testConfig()
	// La bougie 2 ouvre BIEN AU-DESSUS de la limite : un ordre à cours
	// limité se remplit à son prix, jamais mieux — on ne s'attribue pas
	// une exécution qu'on ne peut pas prouver.
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100}, // entrée au close = 100, TP 102, SL 98
		{110, 111, 109, 110}, // gap AU-DESSUS de la limite
		{110, 110, 110, 110},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, TakeProfit: 102, StopLoss: 98},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].ExitPrice != 102 {
		t.Fatalf("limite remplie à son prix exact (102) attendue : %+v", res.Trades)
	}
}

func TestVerticalBarrierClosesAtHorizon(t *testing.T) {
	cfg := testConfig()
	// Bougies JOURNALIÈRES à partir du lundi 1er janvier 2024, week-end
	// inclus : la série est synthétique, ce qui permet d'atteindre
	// l'horizon de 5 jours sans que la clôture de fin de semaine ISO
	// (dimanche 7) ne s'interpose.
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var series core.Series
	for i := 0; i < 8; i++ {
		series = append(series, core.Bar{
			Time:    start.AddDate(0, 0, i),
			BidOpen: 100, BidHigh: 100.2, BidLow: 99.8, BidClose: 100,
		})
	}
	// Barrières très larges : ni le stop ni la limite ne peuvent être
	// touchés, seule la barrière verticale peut fermer la position.
	strat := &scriptedStrategy{script: map[int]core.Signal{
		0: {Action: core.EnterLong, TakeProfit: 500, StopLoss: 1},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.D1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) == 0 {
		t.Fatal("aucun trade : la barrière verticale n'a pas liquidé la position")
	}
	tr := res.Trades[0]
	if tr.ExitReason != "time" {
		t.Fatalf("motif de sortie %q, « time » attendu", tr.ExitReason)
	}
	// Entrée au close du 1er janvier, horizon de 5 jours calendaires : la
	// dernière bougie COMMENCÉE dans la fenêtre est celle du 6 janvier.
	if want := start.AddDate(0, 0, 5); !tr.ExitTime.Equal(want) {
		t.Fatalf("liquidation à %s, %s attendu (entrée + MaxHoldDays)", tr.ExitTime, want)
	}
}

func TestRejectionCountsAreScopedToOneRun(t *testing.T) {
	cfg := testConfig()
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 100, 100, 100},
		{100, 100, 100, 100},
	}, 0)
	// Aucun signal : chaque bougie évaluée produit un rejet « neutre ».
	strat := &scriptedStrategy{script: map[int]core.Signal{}}
	engine := newEngine(cfg)
	req := Request{Symbol: "TEST", Series: series, From: 0, Strategy: strat, Timeframe: data.H1}

	first, err := engine.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Le MÊME moteur — donc le même gestionnaire de risque — rejoue la
	// même série : les compteurs doivent décrire CE run, pas le cumul
	// depuis le démarrage du programme.
	for reason, got := range second.Stats.Rejections {
		if want := first.Stats.Rejections[reason]; got != want {
			t.Fatalf("motif %q : %d rejets au second run, %d au premier — les compteurs cumulent",
				reason, got, want)
		}
	}
	if len(second.Stats.Rejections) == 0 {
		t.Fatal("aucun rejet compté : le test ne prouve rien")
	}
}

// TestRiskSizingFlowsThroughTheEngine : le moteur doit FOURNIR l'équité au
// risque, sinon le dimensionnement marcherait en live et pas en backtest —
// exactement la divergence que l'architecture interdit.
func TestRiskSizingFlowsThroughTheEngine(t *testing.T) {
	cfg := testConfig()
	cfg.Backtest.InitialCapital = 10000
	cfg.Risk.MaxPositionSize = 1_000_000 // plafond hors de portée
	cfg.Risk.RiskPerTradePct = 1         // 1 % de 10 000 = 100 USD
	series := makeBars([][4]float64{
		{100, 100, 100, 100},
		{100, 100, 100, 100}, // entrée au close = 100, stop 98 → distance 2
		{100, 103, 99, 101},
		{101, 101, 101, 101},
	}, 0)
	strat := &scriptedStrategy{script: map[int]core.Signal{
		1: {Action: core.EnterLong, Price: 100, TakeProfit: 102, StopLoss: 98},
	}}
	res, err := newEngine(cfg).Run(context.Background(), Request{
		Symbol: "EURUSD", Series: series, From: 0, Strategy: strat, Timeframe: data.H1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("%d trade(s), 1 attendu", len(res.Trades))
	}
	// quantité = plancher(budget / distance) = plancher(100 / 2) = 50.
	if got := res.Trades[0].Quantity; got != 50 {
		t.Fatalf("%g unités, 50 attendues (budget 100 USD, stop à 2)", got)
	}
}

// TestAggregateDoesNotInventADrawdown : drawdown et Sharpe exigent une
// courbe de valeur. Plusieurs actifs rejoués chacun sur son propre compte
// n'en forment pas une — et zéro se lirait « aucun drawdown » au lieu de
// « non mesuré ».
func TestAggregateDoesNotInventADrawdown(t *testing.T) {
	res := []*Result{{Stats: Stats{
		Symbol: "EURUSD", Trades: 2, Wins: 1, Losses: 1, NetPnL: 10,
		MaxDrawdownPct: 12.5, Sharpe: 1.4, CostsModelled: true, CurrencyExact: true,
		Currency: "USD",
	}, Trades: []core.Trade{{PnL: 30}, {PnL: -20}}}}

	agg := AggregateStats(res, 10000)
	if !math.IsNaN(agg.MaxDrawdownPct) {
		t.Fatalf("drawdown agrégé = %v : il doit valoir NaN (non mesuré)", agg.MaxDrawdownPct)
	}
	if !math.IsNaN(agg.Sharpe) {
		t.Fatalf("Sharpe agrégé = %v : il doit valoir NaN (non mesuré)", agg.Sharpe)
	}

	// Et run.json doit rester écrivable : c'est exactement ce que le
	// MarshalJSON dédié garantit.
	raw, err := json.Marshal(agg)
	if err != nil {
		t.Fatalf("agrégat non sérialisable : %v", err)
	}
	if !strings.Contains(string(raw), `"max_drawdown_pct":null`) {
		t.Fatalf("un drawdown non mesuré doit s'écrire null :\n%s", raw)
	}
	var back Stats
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(back.MaxDrawdownPct) {
		t.Fatal("la relecture doit rétablir NaN, pas zéro")
	}
}
