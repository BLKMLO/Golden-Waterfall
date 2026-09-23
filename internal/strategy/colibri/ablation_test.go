package colibri

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
)

// TestAblation rejoue la mesure qui a fixé les choix de colibri_v1_2 : un
// walk-forward complet (4 plis, 3 paires, 4 ans) de chaque variante, sur
// quatre marchés SYNTHÉTIQUES aux propriétés connues, huit graines chacun.
// Lancé seulement sur demande (quelques minutes) :
//
//	GW_ABLATION=1 go test ./internal/strategy/colibri -run Ablation -v
//
// Chaque variante retire UN ingrédient de v1_2, ou change sa marge. Les
// variantes ne sont enregistrées que dans ce binaire de test.
//
// ⚠ Ce n'est PAS une mesure de performance sur le marché réel. Un marché
// synthétique a un signal connu, un spread constant et une volatilité
// homogène entre paires : il dit si une révision se comporte comme sa
// conception le prévoit (s'abstenir quand il n'y a rien, trader quand il
// y a quelque chose), pas ce qu'elle gagnera. En particulier, il ne peut
// RIEN dire des features en unités de volatilité, dont l'intérêt tient à
// l'hétérogénéité des instruments réels.
func TestAblation(t *testing.T) {
	if os.Getenv("GW_ABLATION") == "" {
		t.Skip("mesure longue : GW_ABLATION=1 pour la lancer")
	}
	variant := func(name string, f func(r *revision)) string {
		r := revV12
		r.name = name
		f(&r)
		strategy.Register(name, func() strategy.Strategy { return newColibri(r) })
		return name
	}
	names := []string{
		revV11.name,
		revV12.name,
		variant("v12_marge_0", func(r *revision) { r.minEdgeR = 0 }),
		variant("v12_marge_005", func(r *revision) { r.minEdgeR = 0.05 }),
		variant("v12_marge_015", func(r *revision) { r.minEdgeR = 0.15 }),
		variant("v12_marge_020", func(r *revision) { r.minEdgeR = 0.20 }),
		variant("v12_features_v1", func(r *revision) { r.features = featuresV1 }),
		variant("v12_avec_unicite", func(r *revision) { r.uniqueness = true }),
		variant("v12_sans_purge", func(r *revision) { r.purge = false }),
		variant("v12_sans_calibrage", func(r *revision) { r.calibrate = false }),
	}
	// Trois marchés écrits directement en H4, et un quatrième écrit en H1
	// puis ré-échantillonné par le walk-forward : un rappel horaire de 0,02
	// fait un marché à fort retour à la moyenne, d'une autre texture.
	markets := []market{
		{"rappel fort (0,05)", 0.05, 0.0012, 0.0004, 4 * time.Hour},
		{"rappel faible (0,02)", 0.02, 0.0012, 0.0004, 4 * time.Hour},
		{"marche au hasard", 0, 0.0012, 0.0004, 4 * time.Hour},
		{"rappel horaire (H1 → H4)", 0.02, 0.0006, 0.0002, time.Hour},
	}
	seeds := []int64{100, 200, 300, 400, 500, 600, 700, 800}
	symbols := []string{"EURUSD", "GBPUSD", "AUDUSD"}
	bases := []float64{1.10, 1.27, 0.68}
	years := []int{2019, 2020, 2021, 2022}

	for _, m := range markets {
		fmt.Printf("\n%s, spread 1e-4 — somme sur %d graines\n", m.name, len(seeds))
		fmt.Printf("  %-18s %7s %10s %6s %8s\n", "variante", "trades", "P&L net", "PF", "AUC OOS")
		for _, name := range names {
			var trades int
			var pnl, gp, gl, auc float64
			for _, seed := range seeds {
				root := t.TempDir()
				cfg := config.Default()
				cfg.Paths = config.Paths{ConfigDir: filepath.Join(root, "cfg"), DataDir: filepath.Join(root, "data")}
				if err := cfg.Paths.EnsureDirs(); err != nil {
					t.Fatal(err)
				}
				cfg.Risk.RiskPerTradePct = 0
				cfg.Risk.MaxPositionSize = 1000
				cfg.Risk.FixedPositionSize = 1000
				cfg.Risk.MaxOpenPositions = 2
				cfg.Backtest.InitialCapital = 10000
				for i, s := range symbols {
					abMarket(t, cfg.Paths.HistoryDir(), s, bases[i], seed+int64(i), years, m, 0.0001)
				}
				rm := risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, slog.New(slog.DiscardHandler))
				res, err := training.NewRunner(cfg, rm).Run(context.Background(), training.Request{
					Strategy: name, Symbols: symbols, Timeframe: data.H4, Folds: 4, Seed: 42,
				})
				if err != nil {
					t.Fatal(err)
				}
				trades += res.Aggregate.Trades
				pnl += res.Aggregate.NetPnL
				gp += res.Aggregate.GrossProfit
				gl += res.Aggregate.GrossLoss
				auc += res.MeanOOSAUC / float64(len(seeds))
			}
			fmt.Printf("  %-18s %7d %10.2f %6.2f %8.3f\n", name, trades, pnl, gp/math.Abs(gl), auc)
		}
	}
}

// market : un générateur de marché synthétique.
type market struct {
	name      string
	reversion float64       // force de rappel vers l'ancre, par bougie
	noise     float64       // écart type du bruit, en fraction du prix
	wick      float64       // amplitude des mèches, en fraction du prix
	step      time.Duration // cadence des bougies écrites
}

// abMarket écrit un historique sans week-end : ancre sinusoïdale, rappel
// vers elle, bruit gaussien, spread constant.
func abMarket(t *testing.T, dir, symbol string, base float64, seed int64, years []int, m market, spread float64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	price := base
	for _, year := range years {
		var series core.Series
		for ts := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC); ts.Year() == year; ts = ts.Add(m.step) {
			if ts.Weekday() == time.Saturday || ts.Weekday() == time.Sunday {
				continue
			}
			anchor := base + 0.02*base*math.Sin(float64(ts.YearDay())/28.0)
			open := price
			price = math.Max(price+(anchor-price)*m.reversion+rng.NormFloat64()*base*m.noise, base*0.5)
			high := math.Max(open, price) + math.Abs(rng.NormFloat64())*base*m.wick
			low := math.Min(open, price) - math.Abs(rng.NormFloat64())*base*m.wick
			s := spread * base
			series = append(series, core.Bar{Time: ts, BidOpen: open, BidHigh: high, BidLow: low, BidClose: price,
				AskOpen: open + s, AskHigh: high + s, AskLow: low + s, AskClose: price + s, Volume: 60 + rng.Float64()*80})
		}
		header := data.FileHeader{Symbol: symbol, Year: year, Scale: 100000}
		if err := data.WriteSeries(data.FilePath(dir, symbol, year), header, series); err != nil {
			t.Fatal(err)
		}
	}
}
