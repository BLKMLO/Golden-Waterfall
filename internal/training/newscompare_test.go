package training

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/news"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
)

// calendarFile écrit un calendrier au format du flux : une annonce USD à
// fort impact chaque jour ouvré à 12 h 30 UTC, de `from` à `to`. Il sert
// aux tests : ce n'est pas un calendrier réel.
func calendarFile(t *testing.T, from, to time.Time) string {
	t.Helper()
	var events []map[string]string
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		events = append(events, map[string]string{
			"title": "Annonce de test", "country": "USD", "impact": "High",
			"date": time.Date(d.Year(), d.Month(), d.Day(), 12, 30, 0, 0, time.UTC).Format(time.RFC3339),
		})
	}
	raw, _ := json.Marshal(events)
	path := filepath.Join(t.TempDir(), "calendrier.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNewsComparisonReplaysTheSameFoldsWithoutTheFilter : pour une
// stratégie qui déclare le filtre, chaque pli est rejoué sans lui ; la
// branche « avec » est l'agrégat publié, l'écart se lit sur la période
// couverte, et une stratégie sans filtre (Colibri) n'a pas de comparaison.
func TestNewsComparisonReplaysTheSameFoldsWithoutTheFilter(t *testing.T) {
	cfg, _ := testSetup(t)
	writeHistory(t, cfg.Paths.HistoryDir(), "EURUSD", 1.10, 131, []int{2020, 2021, 2022})
	svc, err := news.NewService(news.Options{Enabled: true, Source: "none", MinImpact: news.ImpactHigh,
		Before: 30 * time.Minute, After: 30 * time.Minute, Dir: cfg.Paths.NewsDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Calendrier sur la seule année 2022 : une partie des blocs de test
	// est couverte, l'autre non.
	if _, _, err := svc.Import(calendarFile(t,
		time.Date(2022, 1, 3, 0, 0, 0, 0, time.UTC), time.Date(2022, 12, 24, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(cfg, testRisk(cfg)).WithNews(svc)

	res, err := runner.Run(context.Background(), Request{
		Strategy: "troglodyte_v1_1", Symbols: []string{"EURUSD"}, Timeframe: data.H4,
		Folds: 3, Seed: 1, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := res.News
	if c == nil || !c.Measured() {
		t.Fatalf("comparaison attendue et mesurable : %+v", c)
	}
	if c.Blocked == 0 || c.Uncovered == 0 {
		t.Fatalf("des entrées écartées ET des entrées hors couverture étaient attendues : %+v", c)
	}
	if c.Total.With.Trades != res.Aggregate.Trades || math.Abs(c.Total.With.NetPnL-res.Aggregate.NetPnL) > 1e-6 {
		t.Fatalf("la branche avec filtre doit être l'agrégat publié : %+v contre %d trades / %v",
			c.Total.With, res.Aggregate.Trades, res.Aggregate.NetPnL)
	}
	if c.Covered.With.Trades > c.Total.With.Trades || c.Covered.Without.Trades > c.Total.Without.Trades {
		t.Fatalf("la période couverte ne peut pas compter plus de trades que le total : %+v", c)
	}
	sum := NewsComparison{CurrencyExact: true}
	for _, f := range res.Folds {
		if f.News == nil {
			t.Fatalf("pli %d sans comparaison", f.Index)
		}
		sum.add(*f.News)
	}
	if sum.Covered != c.Covered || sum.Total != c.Total || sum.Blocked != c.Blocked {
		t.Fatal("la comparaison du run doit être la somme de celles des plis")
	}
	t.Logf("couverte : avec %d trades %.2f, sans %d trades %.2f, écart %.2f, %d écartées",
		c.Covered.With.Trades, c.Covered.With.NetPnL, c.Covered.Without.Trades, c.Covered.Without.NetPnL,
		c.Covered.Delta(), c.Blocked)

	// Colibri ne déclare pas le filtre : pas de comparaison.
	colibri, err := runner.Run(context.Background(), Request{
		Strategy: "colibri_v1_2", Symbols: []string{"EURUSD"}, Timeframe: data.H4, Folds: 3, Seed: 1, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if colibri.News != nil {
		t.Fatal("une stratégie sans filtre d'actualités n'a rien à comparer")
	}
}

// TestNewsComparisonWithoutCalendarSaysItMeasuresNothing : sans semaine
// archivée, la comparaison existe mais ne mesure rien, et le dit.
func TestNewsComparisonWithoutCalendarSaysItMeasuresNothing(t *testing.T) {
	cfg, _ := testSetup(t)
	writeHistory(t, cfg.Paths.HistoryDir(), "EURUSD", 1.10, 131, []int{2020, 2021, 2022})
	svc, _ := news.NewService(news.Options{Enabled: true, Source: "none", MinImpact: news.ImpactHigh,
		Before: 30 * time.Minute, After: 30 * time.Minute, Dir: cfg.Paths.NewsDir()}, nil)
	res, err := NewRunner(cfg, testRisk(cfg)).WithNews(svc).Run(context.Background(), Request{
		Strategy: "troglodyte_v1_1", Symbols: []string{"EURUSD"}, Timeframe: data.H4, Folds: 3, Seed: 1, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.News == nil || res.News.Measured() || res.News.CoveredWeeks != 0 {
		t.Fatalf("comparaison présente mais non mesurante attendue : %+v", res.News)
	}
	if res.News.Total.Delta() != 0 {
		t.Fatalf("sans calendrier, les deux branches sont identiques : écart %v", res.News.Total.Delta())
	}
}

func TestProfitFactorOfASide(t *testing.T) {
	if pf := (NewsSide{}).ProfitFactor(); !math.IsNaN(pf) {
		t.Fatalf("sans trade : NaN attendu, %v", pf)
	}
	if pf := (NewsSide{Trades: 1, GrossProfit: 5}).ProfitFactor(); !math.IsInf(pf, 1) {
		t.Fatalf("sans perte : +∞ attendu, %v", pf)
	}
	if pf := (NewsSide{Trades: 2, GrossProfit: 6, GrossLoss: -3}).ProfitFactor(); pf != 2 {
		t.Fatalf("6 / 3 = 2 attendu, %v", pf)
	}
}

func testRisk(cfg config.Config) *risk.Manager {
	return risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, slog.New(slog.DiscardHandler))
}
