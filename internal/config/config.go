package config

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

//go:embed default_config.yaml
var defaultConfigYAML []byte

// Config agrège tous les réglages fonctionnels du programme.
//
// Principe de validation : une valeur absurde est une ERREUR AU DÉMARRAGE,
// jamais un ajustement silencieux. Un programme qui trade doit refuser de
// se lancer sur une configuration douteuse plutôt que deviner.
type Config struct {
	Broker   BrokerConfig   `yaml:"broker"`
	Strategy StrategyConfig `yaml:"strategy"`
	Risk     RiskConfig     `yaml:"risk"`
	Costs    CostsConfig    `yaml:"costs"`
	Backtest BacktestConfig `yaml:"backtest"`
	History  HistoryConfig  `yaml:"history"`
	Training TrainingConfig `yaml:"training"`
	UI       UIConfig       `yaml:"ui"`
	Logging  LoggingConfig  `yaml:"logging"`

	// Paths n'est pas dans le YAML : il est calculé au démarrage.
	Paths Paths `yaml:"-"`
}

// BrokerConfig : quelle gateway, et dans quel mode.
type BrokerConfig struct {
	// Name doit correspondre à une clé du registre de gateways.
	Name string `yaml:"name"`
	// Mode vaut "paper" ou "live". Passer en live ne demande QUE ça.
	Mode string `yaml:"mode"`
	// Host/Port de la passerelle (TWS, terminal…), quand elle en a une.
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// Symbols : paires suivies au démarrage. Vide = celles de History.
	Symbols []string `yaml:"symbols"`
	// Timeframe des bougies agrégées en live (doit être un timeframe connu).
	Timeframe string `yaml:"timeframe"`
	// ReplaySpeed : bougies M1 rejouées par seconde par la passerelle
	// "replay". 60 ≈ une heure de marché par minute réelle. Sans effet
	// sur une passerelle réelle, dont la cadence est celle du marché.
	ReplaySpeed float64 `yaml:"replay_speed"`
}

// StrategyConfig : moteur de décision actif.
type StrategyConfig struct {
	Name string `yaml:"name"`
	// Enabled est le kill-switch GLOBAL. Le trading par paire se pilote
	// depuis la TUI et est persisté ; une paire ne trade que si CE drapeau
	// ET son interrupteur sont actifs.
	Enabled bool `yaml:"enabled"`
}

// RiskConfig : limites appliquées par le seul risk.Manager.
type RiskConfig struct {
	// MaxPositionSize : taille d'une entrée en UNITÉS de devise de base
	// (1 lot standard = 100 000, 1 mini-lot = 10 000, 1 micro-lot = 1 000).
	// L'unité compte : à 1, tous les P&L affichés deviennent des
	// poussières illisibles qu'on prend pour du bruit.
	MaxPositionSize float64 `yaml:"max_position_size"`
	// RiskPerTradePct : part de l'ÉQUITÉ risquée par entrée, en %.
	//
	// 0 = désactivé : la taille vaut alors `max_position_size`, quels que
	// soient la paire et le régime de volatilité. Au-dessus de 0, la
	// taille est calculée pour que la distance jusqu'au stop coûte
	// exactement ce pourcentage, et `max_position_size` redevient ce que
	// son nom dit : un PLAFOND.
	//
	// Changer ce réglage change le système, pas seulement son échelle :
	// une taille variable modifie les drawdowns, le profit factor et le
	// SQN. À mesurer en walk-forward avant/après, jamais à supposer.
	RiskPerTradePct       float64 `yaml:"risk_per_trade_pct"`
	MaxPositionsPerSymbol int     `yaml:"max_positions_per_symbol"`
	MaxOpenPositions      int     `yaml:"max_open_positions"`
	// MaxDailyLossPct : au-delà, plus aucune ENTRÉE. Les sorties restent
	// toujours autorisées. 0 = limite désactivée. Garde LIVE uniquement
	// (elle exige l'équité réelle du broker) — cf. risk.Manager.
	MaxDailyLossPct float64 `yaml:"max_daily_loss_pct"`
}

// CostsConfig : coûts de transaction du backtest.
//
// Le SPREAD n'est pas réglé ici : il est MESURÉ dans les données (médiane
// de ask_close - bid_close). Sans côté ask dans l'historique, aucun spread
// n'est modélisé et l'interface le SIGNALE au lieu d'afficher un zéro.
type CostsConfig struct {
	CommissionPerUnit float64 `yaml:"commission_per_unit"`
}

// BacktestConfig : compte simulé.
type BacktestConfig struct {
	InitialCapital float64 `yaml:"initial_capital"`
	// AccountCurrency : devise du compte simulé. Elle décide de la
	// conversion du notionnel et du P&L de chaque paire (cf.
	// data.ConversionFor). Une paire dont ni la base ni la cotation n'est
	// cette devise n'est pas convertible sans taux tiers : le résultat
	// reste alors dans la devise de cotation, et l'interface le SIGNALE.
	AccountCurrency string `yaml:"account_currency"`
	// Leverage : un compte forex immobilise une marge (notionnel/levier),
	// pas le notionnel. Sans levier, les entrées longues dont la taille
	// dépasse le capital seraient refusées alors que les ventes passent —
	// un biais directionnel invisible. 30 = plafond retail ESMA.
	Leverage float64 `yaml:"leverage"`
}

// HistoryConfig : données historiques M1.
type HistoryConfig struct {
	StartYear   int      `yaml:"start_year"`
	Instruments []string `yaml:"instruments"`
	// Concurrency : nombre de téléchargements simultanés. Au-delà de 3-4,
	// Dukascopy répond 429 (limite de débit) — la valeur basse est voulue.
	Concurrency int `yaml:"concurrency"`
}

// TrainingConfig : walk-forward.
type TrainingConfig struct {
	Folds int `yaml:"folds"`
	// Timeframe de travail de l'entraînement et du backtest.
	Timeframe string `yaml:"timeframe"`
	// Workers : plis évalués en parallèle. 0 = un par cœur disponible.
	Workers int `yaml:"workers"`
	// Seed : graine du générateur du modèle. Fixée = entraînement
	// REPRODUCTIBLE, et elle est archivée avec chaque run.
	Seed int64 `yaml:"seed"`
}

// UIConfig : réglages d'affichage de la TUI.
type UIConfig struct {
	// Theme vaut "dark" ou "light".
	Theme string `yaml:"theme"`
	// RefreshMillis : cadence de rafraîchissement des vues temps réel.
	RefreshMillis int `yaml:"refresh_millis"`
	// ChartTimeframe : unité de temps du graphique au démarrage.
	ChartTimeframe string `yaml:"chart_timeframe"`
}

// LoggingConfig : journal.
type LoggingConfig struct {
	Level      string `yaml:"level"`
	BufferSize int    `yaml:"buffer_size"`
}

// Default renvoie une configuration complète et VALIDE.
func Default() Config {
	return Config{
		Broker: BrokerConfig{
			Name: "replay", Mode: "paper", Host: "127.0.0.1", Port: 7497,
			Timeframe: "H4", ReplaySpeed: 120,
		},
		Strategy: StrategyConfig{Name: "colibri_v1_1", Enabled: false},
		Risk: RiskConfig{
			MaxPositionSize: 10000, MaxPositionsPerSymbol: 1,
			MaxOpenPositions: 4, MaxDailyLossPct: 2.0,
		},
		Costs:    CostsConfig{CommissionPerUnit: 0},
		Backtest: BacktestConfig{InitialCapital: 10000, Leverage: 30, AccountCurrency: "USD"},
		History: HistoryConfig{
			StartYear: 2010, Concurrency: 3,
			Instruments: []string{
				"EURUSD", "GBPUSD", "USDJPY", "USDCHF", "USDCAD", "AUDUSD", "NZDUSD",
				"EURGBP", "EURJPY", "EURCHF", "EURAUD", "EURCAD", "EURNZD",
				"GBPJPY", "GBPCHF", "GBPAUD", "GBPCAD", "GBPNZD",
				"AUDJPY", "AUDCHF", "AUDCAD", "AUDNZD",
				"NZDJPY", "NZDCHF", "NZDCAD", "CADJPY", "CADCHF", "CHFJPY",
				"XAUUSD", "XAGUSD", "US500",
			},
		},
		Training: TrainingConfig{Folds: 5, Timeframe: "H4", Workers: 0, Seed: 42},
		UI:       UIConfig{Theme: "dark", RefreshMillis: 500, ChartTimeframe: "H1"},
		Logging:  LoggingConfig{Level: "info", BufferSize: 1000},
	}
}

// Load lit la configuration : défauts → config.yaml → variables GW_*.
//
// Si le fichier n'existe pas, il est CRÉÉ avec le modèle commenté embarqué
// dans le binaire (premier lancement sans étape manuelle), et les défauts
// s'appliquent.
func Load(paths Paths) (Config, error) {
	cfg := Default()
	cfg.Paths = paths

	file := paths.ConfigFile()
	raw, err := os.ReadFile(file)
	switch {
	case os.IsNotExist(err):
		if err := paths.EnsureDirs(); err != nil {
			return cfg, fmt.Errorf("création du dossier de configuration : %w", err)
		}
		if err := os.WriteFile(file, defaultConfigYAML, 0o644); err != nil {
			return cfg, fmt.Errorf("écriture de la configuration par défaut : %w", err)
		}
	case err != nil:
		return cfg, fmt.Errorf("lecture de %s : %w", file, err)
	default:
		// KnownFields : une clé inconnue est une FAUTE DE FRAPPE, pas une
		// option ignorable. Un « max_positions » écrit au singulier qui
		// passe inaperçu, c'est une limite de risque jamais appliquée.
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && err.Error() != "EOF" {
			return cfg, fmt.Errorf("config.yaml invalide (%s) : %w", file, err)
		}
	}
	cfg.Paths = paths

	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("configuration invalide (%s) : %w", file, err)
	}
	return cfg, nil
}

// envBinding associe une variable d'environnement à la CLÉ de
// configuration qu'elle écrase.
//
// Le chemin (« broker.mode ») n'est pas décoratif : l'écran Paramètres
// s'en sert pour dire qu'un réglage est forcé depuis l'extérieur. Sans
// lui, l'interface proposerait de modifier une valeur que l'environnement
// réécrirait au démarrage suivant — exactement le piège que la règle
// « l'environnement a le dernier mot » est censée rendre visible.
type envBinding struct {
	key   string
	path  string
	apply func(*Config, string) error
}

func envBindings() []envBinding {
	return []envBinding{
		{"GW_BROKER", "broker.name", func(c *Config, v string) error { c.Broker.Name = v; return nil }},
		{"GW_MODE", "broker.mode", func(c *Config, v string) error { c.Broker.Mode = v; return nil }},
		{"GW_BROKER_HOST", "broker.host", func(c *Config, v string) error { c.Broker.Host = v; return nil }},
		{"GW_BROKER_PORT", "broker.port", func(c *Config, v string) error { return setInt(v, &c.Broker.Port) }},
		{"GW_STRATEGY", "strategy.name", func(c *Config, v string) error { c.Strategy.Name = v; return nil }},
		{"GW_STRATEGY_ENABLED", "strategy.enabled", func(c *Config, v string) error { return setBool(v, &c.Strategy.Enabled) }},
		{"GW_LOG_LEVEL", "logging.level", func(c *Config, v string) error { c.Logging.Level = v; return nil }},
		{"GW_THEME", "ui.theme", func(c *Config, v string) error { c.UI.Theme = v; return nil }},
		{"GW_TIMEFRAME", "training.timeframe", func(c *Config, v string) error { c.Training.Timeframe = v; return nil }},
		{"GW_SEED", "training.seed", func(c *Config, v string) error { return setInt64(v, &c.Training.Seed) }},
	}
}

// EnvOverrides renvoie, pour chaque clé de configuration actuellement
// FORCÉE par l'environnement, le nom de la variable responsable.
func EnvOverrides() map[string]string {
	out := map[string]string{}
	for _, b := range envBindings() {
		if v, ok := os.LookupEnv(b.key); ok && v != "" {
			out[b.path] = b.key
		}
	}
	return out
}

// applyEnv applique les surcharges GW_* (dernier mot).
func applyEnv(cfg *Config) error {
	bindings := envBindings()
	for _, b := range bindings {
		v, ok := os.LookupEnv(b.key)
		if !ok || v == "" {
			continue
		}
		if err := b.apply(cfg, v); err != nil {
			return fmt.Errorf("variable %s : %w", b.key, err)
		}
	}
	return nil
}

func setInt(v string, dst *int) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("entier attendu, reçu %q", v)
	}
	*dst = n
	return nil
}

func setInt64(v string, dst *int64) error {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return fmt.Errorf("entier attendu, reçu %q", v)
	}
	*dst = n
	return nil
}

func setBool(v string, dst *bool) error {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("booléen attendu, reçu %q", v)
	}
	*dst = b
	return nil
}

// Validate refuse toute configuration dangereuse ou incohérente.
func (c Config) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if c.Broker.Name == "" {
		add("broker.name est vide")
	}
	if c.Broker.Mode != "paper" && c.Broker.Mode != "live" {
		add("broker.mode doit valoir \"paper\" ou \"live\" (reçu %q)", c.Broker.Mode)
	}
	if c.Broker.Port < 0 || c.Broker.Port > 65535 {
		add("broker.port hors bornes : %d", c.Broker.Port)
	}
	if c.Broker.ReplaySpeed <= 0 {
		add("broker.replay_speed doit être > 0 (bougies par seconde)")
	}
	if _, err := parseTimeframeName(c.Broker.Timeframe); err != nil {
		add("broker.timeframe : %v", err)
	}
	if _, err := parseTimeframeName(c.Training.Timeframe); err != nil {
		add("training.timeframe : %v", err)
	}
	if _, err := parseTimeframeName(c.UI.ChartTimeframe); err != nil {
		add("ui.chart_timeframe : %v", err)
	}
	if c.Strategy.Name == "" {
		add("strategy.name est vide")
	}
	if c.Risk.MaxPositionSize <= 0 {
		add("risk.max_position_size doit être > 0 (reçu %g)", c.Risk.MaxPositionSize)
	}
	if c.Risk.MaxPositionsPerSymbol < 1 {
		add("risk.max_positions_per_symbol doit être >= 1")
	}
	if c.Risk.MaxOpenPositions < 1 {
		add("risk.max_open_positions doit être >= 1")
	}
	if c.Risk.MaxOpenPositions < c.Risk.MaxPositionsPerSymbol {
		add("risk.max_open_positions (%d) est inférieur à risk.max_positions_per_symbol (%d) : "+
			"le plafond par symbole ne pourrait jamais être atteint",
			c.Risk.MaxOpenPositions, c.Risk.MaxPositionsPerSymbol)
	}
	if c.Risk.MaxDailyLossPct < 0 || c.Risk.MaxDailyLossPct > 100 {
		add("risk.max_daily_loss_pct doit être dans [0, 100] (reçu %g)", c.Risk.MaxDailyLossPct)
	}
	if c.Risk.RiskPerTradePct < 0 || c.Risk.RiskPerTradePct > 100 {
		add("risk.risk_per_trade_pct doit être dans [0, 100] (reçu %g)", c.Risk.RiskPerTradePct)
	}
	if c.Costs.CommissionPerUnit < 0 {
		add("costs.commission_per_unit ne peut pas être négatif")
	}
	if c.Backtest.InitialCapital <= 0 {
		add("backtest.initial_capital doit être > 0")
	}
	if c.Backtest.Leverage < 1 {
		add("backtest.leverage doit être >= 1 (1 = compte cash strict)")
	}
	if len(strings.TrimSpace(c.Backtest.AccountCurrency)) != 3 {
		add("backtest.account_currency doit être un code ISO de 3 lettres (reçu %q)",
			c.Backtest.AccountCurrency)
	}
	if c.History.StartYear < 1990 || c.History.StartYear > 2100 {
		add("history.start_year invraisemblable : %d", c.History.StartYear)
	}
	if len(c.History.Instruments) == 0 {
		add("history.instruments est vide : rien à télécharger ni à backtester")
	}
	if c.History.Concurrency < 1 || c.History.Concurrency > 16 {
		add("history.concurrency doit être dans [1, 16] (Dukascopy répond 429 au-delà de 3-4)")
	}
	if c.Training.Folds < 2 {
		add("training.folds doit être >= 2 (un seul pli n'est pas un walk-forward)")
	}
	if c.Training.Workers < 0 {
		add("training.workers ne peut pas être négatif (0 = un par cœur)")
	}
	if c.UI.RefreshMillis < 50 {
		add("ui.refresh_millis doit être >= 50 (en deçà, la TUI brûle du CPU pour rien)")
	}
	if c.UI.Theme != "dark" && c.UI.Theme != "light" {
		add("ui.theme doit valoir \"dark\" ou \"light\" (reçu %q)", c.UI.Theme)
	}
	if _, err := core.ParseLevel(c.Logging.Level); err != nil {
		add("logging.level : %v", err)
	}
	if c.Logging.BufferSize < 10 {
		add("logging.buffer_size doit être >= 10")
	}
	if len(errs) > 0 {
		return fmt.Errorf("\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// Live indique si le mode réel est armé.
func (c Config) Live() bool { return c.Broker.Mode == "live" }

// LiveSymbols renvoie les paires suivies en live (repli sur l'historique).
func (c Config) LiveSymbols() []string {
	if len(c.Broker.Symbols) > 0 {
		return c.Broker.Symbols
	}
	return c.History.Instruments
}

// Save réécrit le fichier config.yaml à partir de la configuration courante.
// Les commentaires du modèle sont perdus : la TUI n'écrit donc que sur
// demande explicite (touche de sauvegarde), jamais en tâche de fond.
func (c Config) Save() error {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# Configuration de Golden Waterfall — réécrite depuis l'interface.\n" +
		"# Le modèle commenté d'origine est consultable avec `gw config --print-default`.\n"
	return os.WriteFile(c.Paths.ConfigFile(), append([]byte(header), raw...), 0o644)
}

// DefaultYAML renvoie le modèle commenté embarqué dans le binaire.
func DefaultYAML() []byte { return defaultConfigYAML }
