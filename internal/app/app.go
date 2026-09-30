// Package app est le SEUL endroit où les modules sont câblés ensemble.
//
// C'est une règle d'architecture : un module ne va jamais en instancier un
// autre de son côté. Conséquence
// pratique : pour savoir de quoi dépend quoi, il suffit de lire ce
// fichier — et remplacer une brique (base, passerelle, stratégie) ne
// touche qu'à cet endroit.
package app

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/live"
	"github.com/BLKMLO/Golden-Waterfall/internal/news"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"

	// Import à effet de bord : chaque passerelle s'enregistre dans son
	// init() (le paquet broker sert aussi à juger broker.name au
	// démarrage). Même principe pour les moteurs de décision : le
	// catalogue est le seul paquet qui nomme une implémentation de
	// stratégie.
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategies"
)

// App tient les objets partagés de toute l'application.
type App struct {
	Config   config.Config
	Logger   *slog.Logger
	Logging  *core.Logging
	Bus      *core.Bus
	Store    *storage.Store
	Risk     *risk.Manager
	Live     *live.Runtime
	Backtest *backtest.Engine
	Training *training.Runner
	// News : calendrier économique et filtre, pour les stratégies qui le
	// déclarent. Toujours construit (l'écran et `gw news` en montrent
	// l'état), inerte quand news.enabled est faux.
	News *news.Service
}

// Mode : ce que l'application ouvre.
type Mode int

const (
	// Trading : tout, dont la base du journal (bbolt) et le moteur live.
	// C'est `gw`.
	Trading Mode = iota
	// Workshop : historique, entraînement et backtest, SANS la base ni le
	// moteur live (Store et Live restent nil). C'est `gw backtrain` et les
	// commandes de travail (`gw train`, `gw backtest`, `gw download`…).
	//
	// bbolt verrouille son fichier : une seconde instance qui l'ouvrirait
	// échouerait. Rien de ce qui entraîne ou rejoue ne s'en sert — ne pas
	// l'ouvrir permet d'entraîner pendant qu'une séance live tourne.
	Workshop
)

// New construit l'application complète (mode Trading).
func New(paths config.Paths) (*App, error) { return Open(paths, Trading) }

// Open construit l'application à partir des chemins résolus.
//
// Ordre voulu : configuration (qui peut refuser de démarrer), puis
// journal, puis base, puis métier. Échouer tôt et clairement vaut mieux
// qu'un démarrage à moitié réussi.
func Open(paths config.Paths, mode Mode) (*App, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("préparation du dossier de données : %w", err)
	}
	cfg, err := LoadConfig(paths)
	if err != nil {
		return nil, err
	}
	level, err := core.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return nil, err
	}

	bus := core.NewBus()
	logging, err := core.SetupLogging(paths.LogFile(), level, cfg.Logging.BufferSize, bus)
	if err != nil {
		return nil, err
	}
	logger := logging.Logger

	for _, r := range cfg.Repairs {
		logger.Warn("configuration réparée au démarrage", "cle", r.Key, "ancienne", r.Old,
			"nouvelle", r.New, "raison", r.Reason, "variable", r.Env, "sauvegarde", cfg.Backup)
	}

	var store *storage.Store
	if mode == Trading {
		if store, err = storage.Open(paths.DatabaseFile()); err != nil {
			logging.Close()
			return nil, err
		}
	}

	newsSvc, err := news.NewService(NewsOptions(cfg), logger)
	if err != nil {
		if store != nil {
			store.Close()
		}
		logging.Close()
		return nil, err
	}

	rm := risk.New(cfg.Risk, cfg.Backtest.AccountCurrency, logger)
	a := &App{
		Config:   cfg,
		Logger:   logger,
		Logging:  logging,
		Bus:      bus,
		Store:    store,
		Risk:     rm,
		News:     newsSvc,
		Backtest: backtest.NewEngine(cfg, rm).WithNews(newsSvc),
		Training: training.NewRunner(cfg, rm).WithNews(newsSvc),
	}
	if mode == Trading {
		a.Live = live.NewRuntime(cfg, bus, logger, store, rm).WithNews(newsSvc)
	}

	logger.Info("Golden Waterfall démarré", "mode", map[Mode]string{Trading: "trading", Workshop: "atelier"}[mode],
		"config", paths.ConfigFile(), "donnees", paths.DataDir,
		"strategie", cfg.Strategy.Name, "passerelle", cfg.Broker.Name, "mode", cfg.Broker.Mode)
	warnUnsizable(cfg, logger)
	return a, nil
}

// LoadConfig charge la configuration et répare aussi les réglages que
// seuls les registres savent juger (config ne les connaît pas) : une
// révision retirée par une mise à jour passe à sa remplaçante, un nom
// inconnu à sa valeur par défaut. Toute commande qui lit la configuration
// passe par là, pour que `gw config` montre ce que `gw` appliquera.
func LoadConfig(paths config.Paths) (config.Config, error) {
	cfg, err := config.Load(paths)
	if err != nil {
		return cfg, err
	}
	return cfg, repairRegistries(&cfg)
}

// repairRegistries remet sur une valeur connue les réglages jugés par les
// registres : moteur, source d'historique, passerelle, source
// d'actualités.
func repairRegistries(cfg *config.Config) error {
	def := config.Default()
	if _, err := strategy.New(cfg.Strategy.Name); err != nil {
		name, reason := def.Strategy.Name, "moteur inconnu de cette version"
		if next, ok := strategy.Successor(cfg.Strategy.Name); ok {
			// Sa remplaçante, pas le moteur par défaut : c'est le même
			// moteur dans sa dernière révision. Elle demande un nouvel
			// entraînement — l'écran Live le dit dans ses prérequis.
			name, reason = next, "révision retirée : remplacée par sa dernière révision, à réentraîner"
		}
		if err := config.Fix(cfg, "strategy.name", name, reason); err != nil {
			return err
		}
	}
	if _, err := data.DescribeSource(cfg.History.Source); err != nil {
		if err := config.Fix(cfg, "history.source", def.History.Source, "source d'historique inconnue de cette version"); err != nil {
			return err
		}
	}
	if !contains(broker.ListNames(), cfg.Broker.Name) {
		if err := config.Fix(cfg, "broker.name", def.Broker.Name, "passerelle inconnue de cette version"); err != nil {
			return err
		}
	}
	if !contains(news.List(), cfg.News.Source) {
		if err := config.Fix(cfg, "news.source", def.News.Source, "source d'actualités inconnue de cette version"); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// warnUnsizable prévient au démarrage quand le dimensionnement au risque
// est actif et que des instruments suivis ne peuvent pas être
// dimensionnés.
//
// Sur un compte en dollars, les paires croisées (EURGBP, AUDJPY…) ont un
// P&L dans une devise tierce : sans taux, il n'y a pas de budget de
// risque, et le moteur refuse TOUTES leurs entrées. C'est le
// comportement voulu — on ne dimensionne pas au jugé — mais sans cet
// avertissement il ressemble à une stratégie simplement muette.
func warnUnsizable(cfg config.Config, logger *slog.Logger) {
	if cfg.Risk.RiskPerTradePct <= 0 {
		return
	}
	_, inexact := data.SplitByConversion(cfg.History.Instruments, cfg.Backtest.AccountCurrency)
	if len(inexact) == 0 {
		return
	}
	logger.Warn("dimensionnement au risque impossible sur une partie des instruments",
		"devise_compte", cfg.Backtest.AccountCurrency,
		"instruments", len(inexact),
		"sur", len(cfg.History.Instruments),
		"exemples", strings.Join(inexact[:minInt(4, len(inexact))], " "),
		"consequence", "toutes leurs entrées seront refusées")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SetRiskPerTrade rebâtit le dimensionnement AVANT que quoi que ce soit
// ne tourne.
//
// Elle n'existe que pour la ligne de commande : mesurer l'effet de
// `risk_per_trade_pct` demande de lancer deux walk-forwards qui ne
// diffèrent que par ce réglage, et éditer config.yaml entre les deux
// serait une façon pénible et faillible de s'y prendre.
//
// Elle REFUSE de s'appliquer à un moteur live déjà démarré : le risque,
// le backtest et le live reçoivent leur gestionnaire au câblage, et en
// changer à chaud donnerait un programme dont une moitié obéit à un
// réglage et l'autre à un autre — exactement ce que l'écran Paramètres
// s'interdit avec son brouillon.
func (a *App) SetRiskPerTrade(pct float64) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("risque par trade hors bornes : %g %% (attendu 0 à 100)", pct)
	}
	if a.Live != nil && a.Live.Snapshot().Connected {
		return fmt.Errorf("passerelle connectée : le dimensionnement ne se change pas en cours de séance")
	}
	a.Config.Risk.RiskPerTradePct = pct
	rm := risk.New(a.Config.Risk, a.Config.Backtest.AccountCurrency, a.Logger)
	a.Risk = rm
	a.Backtest = backtest.NewEngine(a.Config, rm).WithNews(a.News)
	a.Training = training.NewRunner(a.Config, rm).WithNews(a.News)
	if a.Live != nil {
		a.Live = live.NewRuntime(a.Config, a.Bus, a.Logger, a.Store, rm).WithNews(a.News)
	}
	return nil
}

// NewsOptions traduit la section `news` de la configuration (déjà
// validée) pour le paquet news, qui n'importe pas config.
func NewsOptions(cfg config.Config) news.Options {
	impact, _ := news.ParseImpact(cfg.News.MinImpact)
	return news.Options{
		Enabled:   cfg.News.Enabled,
		Source:    cfg.News.Source,
		MinImpact: impact,
		Before:    time.Duration(cfg.News.BeforeMinutes) * time.Minute,
		After:     time.Duration(cfg.News.AfterMinutes) * time.Minute,
		Refresh:   time.Duration(cfg.News.RefreshMinutes) * time.Minute,
		Dir:       cfg.Paths.NewsDir(),
	}
}

// Close arrête proprement ce qui doit l'être.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	if a.Live != nil {
		a.Live.Disconnect()
	}
	var firstErr error
	if a.Store != nil {
		if err := a.Store.Close(); err != nil {
			firstErr = err
		}
	}
	if a.Logging != nil {
		if err := a.Logging.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
