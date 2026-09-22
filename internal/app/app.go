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

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/live"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"

	// Import à effet de bord : chaque passerelle s'enregistre dans son
	// init(). Sans cet import, le registre serait vide et le programme
	// annoncerait « passerelle inconnue » pour une passerelle qui existe.
	_ "github.com/BLKMLO/Golden-Waterfall/internal/broker"
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
}

// New construit l'application à partir des chemins résolus.
//
// Ordre voulu : configuration (qui peut refuser de démarrer), puis
// journal, puis base, puis métier. Échouer tôt et clairement vaut mieux
// qu'un démarrage à moitié réussi.
func New(paths config.Paths) (*App, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("préparation du dossier de données : %w", err)
	}
	cfg, err := config.Load(paths)
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

	// La stratégie configurée doit exister AVANT d'ouvrir quoi que ce
	// soit : un nom mal orthographié se découvre au démarrage, pas à la
	// première bougie.
	if _, err := strategy.New(cfg.Strategy.Name); err != nil {
		logging.Close()
		return nil, err
	}

	store, err := storage.Open(paths.DatabaseFile())
	if err != nil {
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
		Backtest: backtest.NewEngine(cfg, rm),
		Training: training.NewRunner(cfg, rm),
	}
	a.Live = live.NewRuntime(cfg, bus, logger, store, rm)

	logger.Info("Golden Waterfall démarré",
		"config", paths.ConfigFile(), "donnees", paths.DataDir,
		"strategie", cfg.Strategy.Name, "passerelle", cfg.Broker.Name, "mode", cfg.Broker.Mode)
	warnUnsizable(cfg, logger)
	return a, nil
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
	a.Backtest = backtest.NewEngine(a.Config, rm)
	a.Training = training.NewRunner(a.Config, rm)
	a.Live = live.NewRuntime(a.Config, a.Bus, a.Logger, a.Store, rm)
	return nil
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
