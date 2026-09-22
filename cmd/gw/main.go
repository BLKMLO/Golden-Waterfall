// Commande gw : le binaire UNIQUE de Golden Waterfall.
//
// Sans argument, il ouvre l'interface terminal (TUI). Les sous-commandes
// existent pour les usages non interactifs — tâche planifiée, conteneur,
// intégration continue — et partagent exactement le même code que la TUI :
// il n'y a pas deux chemins possibles pour un même calcul.
//
//	gw                      interface terminal
//	gw download [PAIRE…]    historique M1 (toutes les paires si aucune)
//	                        --year / --from / --to limitent la période
//	gw train                walk-forward complet
//	gw backtest PAIRE       rejeu d'une paire avec le modèle de production
//	                        (--csv écrit trades, équité et métriques)
//	gw runs                 entraînements archivés
//	gw paths                emplacements de la configuration et des données
//	gw config               configuration effective (--default pour le modèle)
//	gw version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/export"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui"
)

// Version est renseignée à la compilation :
//
//	go build -ldflags "-X main.Version=v0.1.0" ./cmd/gw
var Version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runTUI()
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("Golden Waterfall %s\n", Version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	case "paths":
		return runPaths()
	case "config":
		return runConfig(args[1:])
	case "download":
		return runDownload(args[1:])
	case "train":
		return runTrain(args[1:])
	case "backtest":
		return runBacktest(args[1:])
	case "runs":
		return runRuns()
	default:
		printUsage()
		return fmt.Errorf("sous-commande inconnue %q", args[0])
	}
}

func printUsage() {
	fmt.Print(`Golden Waterfall — expert advisor en terminal.

  gw                      interface terminal (par défaut)
  gw download [PAIRE…]    télécharge l'historique M1 Dukascopy
     --year A             une seule année        (ex. gw download EURUSD --year 2019)
     --from A --to B      une période            (bornes comprises)
  gw train                lance un walk-forward complet
  gw backtest PAIRE       rejoue une paire avec le modèle de production
     --csv                écrit aussi trades, équité et métriques en CSV
  gw runs                 liste les entraînements archivés
  gw paths                affiche les emplacements utilisés
  gw config [--default]   affiche la configuration effective
  gw version

Variables d'environnement :
  GW_CONFIG_DIR, GW_DATA_DIR   forcent les emplacements (installation portable)
  GW_BROKER, GW_MODE, GW_STRATEGY, GW_STRATEGY_ENABLED, GW_LOG_LEVEL,
  GW_THEME, GW_TIMEFRAME, GW_SEED, GW_BROKER_HOST, GW_BROKER_PORT
`)
}

// open construit l'application et installe l'arrêt propre sur signal.
func open() (*app.App, context.Context, context.CancelFunc, error) {
	a, err := app.New(config.DefaultPaths())
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return a, ctx, cancel, nil
}

func runTUI() error {
	a, err := app.New(config.DefaultPaths())
	if err != nil {
		return err
	}
	defer a.Close()
	return tui.Run(a)
}

func runPaths() error {
	p := config.DefaultPaths()
	fmt.Printf("Configuration : %s\n", p.ConfigFile())
	fmt.Printf("Données       : %s\n", p.DataDir)
	fmt.Printf("  historique  : %s\n", p.HistoryDir())
	fmt.Printf("  modèles     : %s\n", p.ModelsDir())
	fmt.Printf("  exports     : %s\n", p.ExportsDir())
	fmt.Printf("  base        : %s\n", p.DatabaseFile())
	fmt.Printf("  journal     : %s\n", p.LogFile())
	return nil
}

func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	showDefault := fs.Bool("default", false, "afficher le modèle commenté livré avec le binaire")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showDefault {
		os.Stdout.Write(config.DefaultYAML())
		return nil
	}
	cfg, err := config.Load(config.DefaultPaths())
	if err != nil {
		return err
	}
	fmt.Printf("fichier            : %s\n", cfg.Paths.ConfigFile())
	fmt.Printf("passerelle         : %s (%s)\n", cfg.Broker.Name, cfg.Broker.Mode)
	fmt.Printf("stratégie          : %s (kill-switch %v)\n", cfg.Strategy.Name, cfg.Strategy.Enabled)
	fmt.Printf("unité de temps     : %s (live %s)\n", cfg.Training.Timeframe, cfg.Broker.Timeframe)
	fmt.Printf("risque             : taille %g · %d/symbole · %d/compte · perte max %.1f %%\n",
		cfg.Risk.MaxPositionSize, cfg.Risk.MaxPositionsPerSymbol,
		cfg.Risk.MaxOpenPositions, cfg.Risk.MaxDailyLossPct)
	fmt.Printf("backtest           : capital %.0f · levier %g×\n",
		cfg.Backtest.InitialCapital, cfg.Backtest.Leverage)
	fmt.Printf("instruments        : %d (%s)\n", len(cfg.History.Instruments),
		strings.Join(cfg.History.Instruments, " "))
	fmt.Printf("stratégies         : %s\n", strings.Join(strategy.List(), ", "))
	return nil
}

// partitionArgs sépare les options des paires.
//
// Le paquet `flag` s'arrête au premier argument positionnel :
// « gw download EURUSD --from 2019 » lui ferait ignorer les deux options
// en silence, et téléchargerait vingt ans d'historique à la place de
// l'année demandée. On les remet donc devant.
func partitionArgs(args []string, takesValue map[string]bool) (flags, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
		if takesValue[name] && !strings.Contains(a, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, rest
}

func runDownload(args []string) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	from := fs.Int("from", 0, "première année à télécharger (défaut : history.start_year)")
	to := fs.Int("to", 0, "dernière année à télécharger (défaut : année courante)")
	year := fs.Int("year", 0, "une seule année (raccourci pour --from A --to A)")
	flags, symbols := partitionArgs(args, map[string]bool{"from": true, "to": true, "year": true})
	if err := fs.Parse(flags); err != nil {
		return err
	}

	a, ctx, cancel, err := open()
	if err != nil {
		return err
	}
	defer cancel()
	defer a.Close()

	if len(symbols) == 0 {
		symbols = a.Config.History.Instruments
	}
	span := data.FullRange(a.Config.History.StartYear)
	if *year > 0 {
		span = data.YearRange{From: *year, To: *year}.Normalize()
	} else {
		if *from > 0 {
			span.From = *from
		}
		if *to > 0 {
			span.To = *to
		}
		span = span.Normalize()
	}
	fmt.Printf("Période : %s · %d paire(s)\n", span, len(symbols))

	dl := data.NewDownloader(a.Config.Paths.HistoryDir(), a.Config.History.Concurrency, a.Logger)
	endYear := time.Now().UTC().Year()
	total := 0
	for _, sym := range symbols {
		for _, year := range span.Years() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !data.NeedsDownload(a.Config.Paths.HistoryDir(), sym, year, endYear) {
				continue
			}
			last := ""
			n, err := dl.DownloadYear(ctx, sym, year, func(p data.DownloadProgress) {
				line := fmt.Sprintf("\r%s %d : %d/%d jours · %d bougies · %d échecs   ",
					p.Symbol, p.Year, p.DaysDone, p.DaysTotal, p.Bars, p.Failures)
				if line != last {
					fmt.Print(line)
					last = line
				}
			})
			fmt.Println()
			if err != nil {
				return err
			}
			total += n
		}
	}
	fmt.Printf("Terminé : %d bougies écrites dans %s\n", total, a.Config.Paths.HistoryDir())
	return nil
}

func runTrain(args []string) error {
	fs := flag.NewFlagSet("train", flag.ContinueOnError)
	folds := fs.Int("folds", 0, "nombre de plis (défaut : config)")
	tfName := fs.String("tf", "", "unité de temps (défaut : config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, ctx, cancel, err := open()
	if err != nil {
		return err
	}
	defer cancel()
	defer a.Close()

	name := a.Config.Training.Timeframe
	if *tfName != "" {
		name = *tfName
	}
	tf, err := data.ParseTimeframe(name)
	if err != nil {
		return err
	}
	n := a.Config.Training.Folds
	if *folds > 0 {
		n = *folds
	}

	res, err := a.Training.Run(ctx, training.Request{
		Strategy:   a.Config.Strategy.Name,
		Symbols:    a.Config.History.Instruments,
		Timeframe:  tf,
		Folds:      n,
		Seed:       a.Config.Training.Seed,
		Workers:    a.Config.Training.Workers,
		TrainFinal: true,
		Progress: func(p training.Progress) {
			fmt.Printf("\r[%3.0f %%] %-12s %-60s", p.Ratio*100, p.Phase, truncate(p.Message, 60))
		},
	})
	fmt.Println()
	if err != nil {
		return err
	}
	auc := "—"
	if res.HasMeanAUC {
		auc = fmt.Sprintf("%.3f", res.MeanOOSAUC)
	}
	fmt.Printf("Run %s · %d plis · %s\n", res.RunID, len(res.Folds), res.Duration.Round(time.Second))
	fmt.Printf("AUC out-of-sample moyenne : %s (0,50 = hasard)\n", auc)
	fmt.Printf("Trades OOS : %d · taux de gain %.1f %% · P&L %.2f %s · profit factor %s\n",
		res.Aggregate.Trades, res.Aggregate.WinRate, res.Aggregate.NetPnL,
		res.Aggregate.Currency, ratio(res.Aggregate.ProfitFactor))
	if !res.Aggregate.CurrencyExact {
		fmt.Println("⚠ Des actifs ne sont pas convertibles vers la devise du compte : " +
			"la somme des P&L mélange des devises. Les ratios restent exacts.")
	}
	if res.Aggregate.RejectedOrders > 0 {
		fmt.Printf("⚠ %d ordre(s) refusé(s) faute de marge : ce ne sont pas des abstentions du modèle.\n",
			res.Aggregate.RejectedOrders)
	}
	if res.FinalDir != "" {
		fmt.Printf("Modèle de production : %s\n", res.FinalDir)
	} else {
		fmt.Println("Aucun modèle de production écrit.")
	}
	return nil
}

func runBacktest(args []string) error {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	tfName := fs.String("tf", "", "unité de temps (défaut : config)")
	toCSV := fs.Bool("csv", false, "écrire trades, courbe de valeur et métriques en CSV")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage : gw backtest [-tf H4] [--csv] PAIRE")
	}
	symbol := strings.ToUpper(fs.Arg(0))

	a, ctx, cancel, err := open()
	if err != nil {
		return err
	}
	defer cancel()
	defer a.Close()

	name := a.Config.Training.Timeframe
	if *tfName != "" {
		name = *tfName
	}
	tf, err := data.ParseTimeframe(name)
	if err != nil {
		return err
	}
	modelDir, why := training.SelectModel(a.Config.Paths.ModelsDir(), a.Config.Strategy.Name, symbol)
	if modelDir == "" {
		return fmt.Errorf("aucun modèle utilisable pour %s : %s — lancer `gw train` d'abord", symbol, why)
	}
	raw, err := data.Load(a.Config.Paths.HistoryDir(), symbol, time.Time{}, time.Time{})
	if err != nil {
		return err
	}
	series := data.Resample(raw, tf)
	strat, err := strategy.New(a.Config.Strategy.Name)
	if err != nil {
		return err
	}
	defer strat.Shutdown()
	if err := strat.Warmup(ctx, strategy.WarmupRequest{
		Symbol: symbol, Series: series, Timeframe: tf, ModelDir: modelDir,
	}); err != nil {
		return err
	}
	res, err := a.Backtest.Run(ctx, backtest.Request{
		Symbol: symbol, Series: series, From: feature.ContextBars, Strategy: strat, Timeframe: tf,
	})
	if err != nil {
		return err
	}
	s := res.Stats
	fmt.Printf("%s en %s · %s → %s · %d bougies\n", symbol, tf,
		s.Start.Format("2006-01-02"), s.End.Format("2006-01-02"), s.Bars)
	fmt.Printf("Trades %d (%d gagnants, %.1f %%) · P&L %.2f %s · rendement %.2f %%\n",
		s.Trades, s.Wins, s.WinRate, s.NetPnL, s.Currency, s.ReturnPct)
	if !s.CurrencyExact {
		fmt.Printf("⚠ Montants en %s, NON convertis vers %s : cette paire exigerait un taux tiers.\n",
			s.Currency, a.Config.Backtest.AccountCurrency)
	}
	fmt.Printf("Profit factor %s · Sharpe %s · SQN %s · drawdown max %s %%\n",
		ratio(s.ProfitFactor), ratio(s.Sharpe), ratio(s.SQN), ratio(s.MaxDrawdownPct))
	if s.CostsModelled {
		fmt.Printf("Coûts %.2f (spread médian mesuré %.6f)\n", s.Costs, s.Spread)
	} else {
		fmt.Println("⚠ Aucun coût modélisé : l'historique n'a pas de côté ask. Résultat optimiste.")
	}
	if s.RejectedOrders > 0 {
		fmt.Printf("⚠ %d ordre(s) refusé(s) faute de marge.\n", s.RejectedOrders)
	}
	if *toCSV {
		paths, err := export.Backtest(a.Config.Paths.ExportsDir(), symbol, string(tf), res, time.Now())
		if err != nil {
			return err
		}
		for _, p := range paths {
			fmt.Printf("écrit %s\n", p)
		}
	}
	fmt.Println(inSampleWarning)
	return nil
}

// inSampleWarning : le modèle de PRODUCTION est entraîné sur tout
// l'historique disponible, cette période comprise. Un rejeu manuel est
// donc IN-SAMPLE et flatte le modèle — le taux de gain qu'il affiche est
// celui d'un examen dont on a vu le corrigé. Le seul chiffre honnête est
// l'agrégat out-of-sample du walk-forward.
const inSampleWarning = "\n⚠ Rejeu IN-SAMPLE : le modèle de production a été entraîné sur tout " +
	"l'historique,\n  cette période comprise. Le chiffre honnête est l'agrégat out-of-sample " +
	"de `gw train`."

func runRuns() error {
	cfg, err := config.Load(config.DefaultPaths())
	if err != nil {
		return err
	}
	runs, err := training.ListRuns(cfg.Paths.ModelsDir())
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("Aucun entraînement archivé. Lancer `gw train`.")
		return nil
	}
	fmt.Printf("%-16s %-16s %-4s %6s %8s %9s %12s\n", "RUN", "STRATÉGIE", "TF", "PAIRES", "TRADES", "AUC OOS", "P&L")
	for _, r := range runs {
		auc := "—"
		if r.HasAUC {
			auc = fmt.Sprintf("%.3f", r.MeanOOSAUC)
		}
		fmt.Printf("%-16s %-16s %-4s %6d %8d %9s %12.2f\n",
			r.RunID, r.Strategy, r.Timeframe, len(r.Symbols), r.Trades, auc, r.NetPnL)
	}
	return nil
}

func ratio(v float64) string {
	switch {
	case v != v:
		return "—"
	case v > 1e308:
		return "∞"
	}
	return fmt.Sprintf("%.2f", v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
