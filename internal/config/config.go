package config

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

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
	News     NewsConfig     `yaml:"news"`
	UI       UIConfig       `yaml:"ui"`
	Logging  LoggingConfig  `yaml:"logging"`

	// Paths n'est pas dans le YAML : il est calculé au démarrage.
	Paths Paths `yaml:"-"`

	// Repairs : réglages remis d'office au chargement (repair.go). Backup :
	// copie de l'ancien config.yaml si le fichier a été réécrit.
	Repairs []Repair `yaml:"-"`
	Backup  string   `yaml:"-"`
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
	// ClientID : identifiant de connexion auprès de TWS. Deux programmes
	// connectés au même TWS doivent en avoir un différent.
	ClientID int `yaml:"client_id"`
	// Account : compte courtier, requis seulement quand la session TWS en
	// gère plusieurs. Vide = le seul compte de la session.
	Account string `yaml:"account"`
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
	// MaxPositionSize : PLAFOND d'une entrée, en unités de devise de base
	// (1 lot standard = 100 000, 1 mini-lot = 10 000, 1 micro-lot = 1 000).
	//
	// C'est une garde, pas une taille. Elle l'était : la même clé servait
	// de plafond en dimensionnement au risque et de taille exacte en
	// taille fixe — deux sens pour une valeur, donc un piège. Activer le
	// risque par trade obligeait à relever ce nombre, ce qui décuplait en
	// même temps la taille fixe si on le désactivait ensuite.
	MaxPositionSize float64 `yaml:"max_position_size"`
	// FixedPositionSize : la taille utilisée quand `risk_per_trade_pct`
	// vaut 0. L'unité compte : à 1, tous les P&L affichés deviennent des
	// poussières illisibles qu'on prend pour du bruit.
	FixedPositionSize float64 `yaml:"fixed_position_size"`
	// RiskPerTradePct : part de l'ÉQUITÉ risquée par entrée, en %.
	//
	// 0 = désactivé : la taille vaut alors `fixed_position_size`, quels
	// que soient la paire et le régime de volatilité. Au-dessus de 0, la
	// taille est calculée pour que la distance jusqu'au stop coûte
	// exactement ce pourcentage, plafonnée par `max_position_size`.
	//
	// Changer ce réglage change le système, pas seulement son échelle :
	// une taille variable modifie les drawdowns, le profit factor et le
	// SQN. `gw train --risk-per-trade X` le mesure sur ses propres
	// données, deux runs et une comparaison — jamais à supposer.
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
	// Source : fournisseur de l'historique (clé du registre data.Source :
	// « dukascopy », « fxcm »). Vérifiée dans app.New : config ne connaît
	// pas le registre.
	Source      string   `yaml:"source"`
	StartYear   int      `yaml:"start_year"`
	Instruments []string `yaml:"instruments"`
	// Concurrency : nombre de téléchargements simultanés. Au-delà de 3-4,
	// Dukascopy répond 429 (limite de débit) — la valeur basse est voulue.
	// FXCM n'a montré aucune limite, mais rien ne la garantit.
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

// NewsConfig : filtre d'actualités économiques.
//
// Il ne s'applique qu'aux stratégies qui le DÉCLARENT
// (`Description.UsesNews`) ; Colibri n'y a jamais accès, quel que soit ce
// réglage. Le calendrier est récupéré par le programme, archivé, et
// appliqué par les moteurs — jamais par la stratégie elle-même.
type NewsConfig struct {
	// Enabled : filtre actif pour les stratégies qui le déclarent.
	Enabled bool `yaml:"enabled"`
	// Source : clé du registre des sources (« forexfactory », « none »).
	Source string `yaml:"source"`
	// MinImpact : impact minimal d'une annonce filtrante (low, medium,
	// high).
	MinImpact string `yaml:"min_impact"`
	// BeforeMinutes / AfterMinutes : aucune entrée si une annonce tombe
	// dans les N minutes qui suivent la décision, ou est survenue dans les
	// M minutes qui la précèdent.
	BeforeMinutes int `yaml:"before_minutes"`
	AfterMinutes  int `yaml:"after_minutes"`
	// RefreshMinutes : cadence de récupération pendant une séance live.
	RefreshMinutes int `yaml:"refresh_minutes"`
}

// UIConfig : réglages d'affichage de la TUI.
type UIConfig struct {
	// Theme vaut "auto", "dark" ou "light". "auto" laisse le terminal
	// décider ; les deux autres FORCENT l'interprétation, ce qui sert
	// quand la détection se trompe (multiplexeur, terminal distant).
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
			Name: "replay", Mode: "paper", Host: "127.0.0.1", Port: 7497, ClientID: 1,
			Timeframe: "H4", ReplaySpeed: 120,
		},
		Strategy: StrategyConfig{Name: "colibri_v1_2", Enabled: false},
		Risk: RiskConfig{
			MaxPositionSize: 100000, FixedPositionSize: 10000,
			RiskPerTradePct:       0.5,
			MaxPositionsPerSymbol: 1,
			MaxOpenPositions:      4, MaxDailyLossPct: 2.0,
		},
		Costs:    CostsConfig{CommissionPerUnit: 0},
		Backtest: BacktestConfig{InitialCapital: 10000, Leverage: 30, AccountCurrency: "USD"},
		History: HistoryConfig{
			Source: "dukascopy", StartYear: 2010, Concurrency: 3,
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
		News: NewsConfig{Enabled: true, Source: "forexfactory", MinImpact: "high",
			BeforeMinutes: 30, AfterMinutes: 30, RefreshMinutes: 60},
		UI:      UIConfig{Theme: "auto", RefreshMillis: 500, ChartTimeframe: "H1"},
		Logging: LoggingConfig{Level: "info", BufferSize: 1000},
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
		if err := os.WriteFile(file, defaultConfigYAML, configPerm); err != nil {
			return cfg, fmt.Errorf("écriture de la configuration par défaut : %w", err)
		}
	case err != nil:
		return cfg, fmt.Errorf("lecture de %s : %w", file, err)
	default:
		// Réparation automatique (repair.go) : une clé inconnue, une valeur
		// illisible ou refusée — typiquement laissées par une version
		// précédente — sont remises d'office et ANNONCÉES, au lieu de
		// bloquer le démarrage. Seule une erreur de syntaxe reste fatale.
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return cfg, fmt.Errorf("config.yaml illisible (%s) : %w — corriger la syntaxe, ou le supprimer "+
				"pour repartir du modèle", file, err)
		}
		if len(doc.Content) == 0 {
			break // fichier vide : défauts
		}
		if doc.Content[0].Kind != yaml.MappingNode {
			// Un fichier qui n'est pas une table ne décrit aucun réglage :
			// il est sauvegardé puis remplacé par le modèle.
			backup := file + "." + time.Now().Format("2006-01-02T15-04-05") + ".bak"
			if err := os.WriteFile(backup, raw, configPerm); err != nil {
				return cfg, err
			}
			if err := os.WriteFile(file, defaultConfigYAML, configPerm); err != nil {
				return cfg, err
			}
			empty := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
			out, err := loadFrom(file, nil, &empty, paths)
			out.Backup = backup
			out.Repairs = append([]Repair{{Key: "config.yaml", Old: summary(doc.Content[0]), New: "modèle par défaut",
				Reason: "le fichier n'est pas une table de réglages"}}, out.Repairs...)
			return out, err
		}
		return loadFrom(file, raw, &doc, paths)
	}
	// Premier lancement ou fichier vide : les défauts, et les variables
	// d'environnement, réparées de même.
	empty := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	return loadFrom(file, nil, &empty, paths)
}

// loadFrom répare et décode une racine YAML ; réécrit le fichier (après
// sauvegarde) si une réparation le touche.
func loadFrom(file string, raw []byte, doc *yaml.Node, paths Paths) (Config, error) {
	cfg, repairs, changed, err := loadRepaired(doc.Content[0], paths)
	if err != nil {
		return cfg, fmt.Errorf("configuration invalide (%s) : %w", file, err)
	}
	cfg.Repairs = completeRepairs(cfg, repairs)
	if changed && raw != nil {
		backup, err := writeRepaired(file, raw, doc)
		if err != nil {
			return cfg, err
		}
		cfg.Backup = backup
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
		{"GW_BROKER_CLIENT_ID", "broker.client_id", func(c *Config, v string) error { return setInt(v, &c.Broker.ClientID) }},
		{"GW_BROKER_ACCOUNT", "broker.account", func(c *Config, v string) error { c.Broker.Account = v; return nil }},
		{"GW_STRATEGY", "strategy.name", func(c *Config, v string) error { c.Strategy.Name = v; return nil }},
		{"GW_STRATEGY_ENABLED", "strategy.enabled", func(c *Config, v string) error { return setBool(v, &c.Strategy.Enabled) }},
		{"GW_LOG_LEVEL", "logging.level", func(c *Config, v string) error { c.Logging.Level = v; return nil }},
		{"GW_THEME", "ui.theme", func(c *Config, v string) error { c.UI.Theme = v; return nil }},
		{"GW_TIMEFRAME", "training.timeframe", func(c *Config, v string) error { c.Training.Timeframe = v; return nil }},
		{"GW_SEED", "training.seed", func(c *Config, v string) error { return setInt64(v, &c.Training.Seed) }},
		{"GW_NEWS", "news.enabled", func(c *Config, v string) error { return setBool(v, &c.News.Enabled) }},
		{"GW_HISTORY_SOURCE", "history.source", func(c *Config, v string) error { c.History.Source = v; return nil }},
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

// Validate refuse toute configuration dangereuse ou incohérente. Au
// chargement, ses refus sont réparés (repair.go) ; l'écran Paramètres, lui,
// refuse d'écrire un brouillon invalide.
func (c Config) Validate() error {
	if errs := c.problems(); len(errs) > 0 {
		return fmt.Errorf("\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// problems : les refus de Validate, un par phrase. Chaque phrase cite en
// PREMIER la clé à remettre à son défaut (repair.go s'en sert).
func (c Config) problems() []string {
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
	if c.Broker.ClientID < 0 || c.Broker.ClientID > 2147483647 {
		add("broker.client_id hors bornes : %d (0 à 2147483647)", c.Broker.ClientID)
	}
	if !positive(c.Broker.ReplaySpeed) {
		add("broker.replay_speed doit être un nombre fini > 0 (bougies par seconde)")
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
	// Tous les nombres à virgule doivent être FINIS. YAML lit « .nan » et
	// « .inf », l'écran Paramètres lisait « nan » : NaN n'étant ni « <= 0 »
	// ni « > plafond », il traversait chaque contrôle — un plafond NaN ne
	// plafonnait plus rien, une perte journalière NaN bloquait toutes les
	// entrées, un levier NaN supprimait le contrôle de marge.
	if !positive(c.Risk.MaxPositionSize) {
		add("risk.max_position_size doit être un nombre fini > 0 (reçu %g)", c.Risk.MaxPositionSize)
	}
	if !positive(c.Risk.FixedPositionSize) {
		add("risk.fixed_position_size doit être un nombre fini > 0 (reçu %g)", c.Risk.FixedPositionSize)
	}
	if c.Risk.FixedPositionSize > c.Risk.MaxPositionSize {
		// Sans ce refus, la taille fixe serait silencieusement rabotée au
		// plafond : le programme n'enverrait pas la taille demandée et
		// rien ne le dirait.
		add("risk.fixed_position_size (%g) dépasse risk.max_position_size (%g)",
			c.Risk.FixedPositionSize, c.Risk.MaxPositionSize)
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
	if !(c.Risk.MaxDailyLossPct >= 0 && c.Risk.MaxDailyLossPct <= 100) {
		add("risk.max_daily_loss_pct doit être dans [0, 100] (reçu %g)", c.Risk.MaxDailyLossPct)
	}
	if !(c.Risk.RiskPerTradePct >= 0 && c.Risk.RiskPerTradePct <= 100) {
		add("risk.risk_per_trade_pct doit être dans [0, 100] (reçu %g)", c.Risk.RiskPerTradePct)
	}
	if !(c.Costs.CommissionPerUnit >= 0) || math.IsInf(c.Costs.CommissionPerUnit, 0) {
		add("costs.commission_per_unit doit être un nombre fini >= 0 (reçu %g)", c.Costs.CommissionPerUnit)
	}
	if !positive(c.Backtest.InitialCapital) {
		add("backtest.initial_capital doit être un nombre fini > 0 (reçu %g)", c.Backtest.InitialCapital)
	}
	if !(c.Backtest.Leverage >= 1) || math.IsInf(c.Backtest.Leverage, 0) {
		add("backtest.leverage doit être un nombre fini >= 1 (1 = compte cash strict ; reçu %g)", c.Backtest.Leverage)
	}
	if !isCurrencyCode(c.Backtest.AccountCurrency) {
		add("backtest.account_currency doit être un code ISO de 3 lettres (reçu %q)",
			c.Backtest.AccountCurrency)
	}
	if strings.TrimSpace(c.History.Source) == "" {
		add("history.source est vide (« dukascopy » ou « fxcm »)")
	}
	if c.History.StartYear < 1990 || c.History.StartYear > 2100 {
		add("history.start_year invraisemblable : %d", c.History.StartYear)
	}
	if len(c.History.Instruments) == 0 {
		add("history.instruments est vide : rien à télécharger ni à backtester")
	}
	if c.History.Concurrency < 1 || c.History.Concurrency > 16 {
		add("history.concurrency doit être dans [1, 16] (Dukascopy répond 429 au-delà de 3-4) (reçu %d)", c.History.Concurrency)
	}
	if c.Training.Folds < 2 {
		add("training.folds doit être >= 2 (un seul pli n'est pas un walk-forward)")
	}
	if c.Training.Workers < 0 {
		add("training.workers ne peut pas être négatif (0 = un par cœur)")
	}
	if strings.TrimSpace(c.News.Source) == "" {
		add("news.source est vide (« none » pour n'utiliser que l'archive)")
	}
	switch c.News.MinImpact {
	case "low", "medium", "high":
	default:
		add("news.min_impact doit valoir \"low\", \"medium\" ou \"high\" (reçu %q)", c.News.MinImpact)
	}
	if c.News.BeforeMinutes < 0 || c.News.BeforeMinutes > 24*60 || c.News.AfterMinutes < 0 || c.News.AfterMinutes > 24*60 {
		add("news.before_minutes et news.after_minutes doivent être dans [0, 1440]")
	}
	if c.News.RefreshMinutes < 5 {
		add("news.refresh_minutes doit être >= 5 (le flux ne change pas à la minute ; au-delà on le surcharge)")
	}
	if c.UI.RefreshMillis < 50 || c.UI.RefreshMillis > 60000 {
		add("ui.refresh_millis doit être dans [50, 60000] (en deçà, la TUI brûle du CPU pour rien ; "+
			"au-delà, l'écran ne vit plus) (reçu %d)", c.UI.RefreshMillis)
	}
	if c.UI.Theme != "auto" && c.UI.Theme != "dark" && c.UI.Theme != "light" {
		add("ui.theme doit valoir \"auto\", \"dark\" ou \"light\" (reçu %q)", c.UI.Theme)
	}
	if _, err := core.ParseLevel(c.Logging.Level); err != nil {
		add("logging.level : %v", err)
	}
	if c.Logging.BufferSize < 10 || c.Logging.BufferSize > 1_000_000 {
		// Le tampon est réservé d'un bloc au démarrage : une valeur
		// démesurée faisait tomber le programme avant même la réparation.
		add("logging.buffer_size doit être dans [10, 1000000] (reçu %d)", c.Logging.BufferSize)
	}
	return errs
}

// positive : un nombre fini strictement positif (faux pour NaN et ±∞).
func positive(v float64) bool { return v > 0 && !math.IsInf(v, 1) }

// isCurrencyCode : trois lettres, comme un code ISO 4217 (« USD », « eur »).
func isCurrencyCode(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
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
//
// Une clé FORCÉE par une variable GW_* garde la valeur du FICHIER : la
// configuration courante porte celle de l'environnement, et l'écrire
// l'aurait rendue permanente. Un `GW_MODE=live gw` lancé une fois, suivi
// d'un réglage de thème enregistré, laissait sinon `broker.mode: live`
// dans config.yaml pour toutes les séances suivantes.
func (c Config) Save() error {
	out := c
	if forced := EnvOverrides(); len(forced) > 0 {
		file := fileConfig(c.Paths)
		for path := range forced {
			restorePath(&out, file, path)
		}
	}
	raw, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	header := "# Configuration de Golden Waterfall — réécrite depuis l'interface.\n" +
		"# Le modèle commenté d'origine est consultable avec `gw config --default`.\n"
	return os.WriteFile(c.Paths.ConfigFile(), append([]byte(header), raw...), configPerm)
}

// configPerm : droits d'un fichier de configuration CRÉÉ par le programme.
// Il peut nommer le compte courtier (broker.account) : lecture réservée à
// l'utilisateur. Un fichier existant garde ses droits.
const configPerm = 0o600

// fileConfig : les défauts recouverts par le seul FICHIER, sans
// environnement ni réparation. Un fichier illisible donne les défauts.
func fileConfig(paths Paths) Config {
	cfg := Default()
	if raw, err := os.ReadFile(paths.ConfigFile()); err == nil {
		_ = yaml.Unmarshal(raw, &cfg)
	}
	return cfg
}

// restorePath recopie dans dst la valeur que src porte au chemin
// « section.clé ».
func restorePath(dst *Config, src Config, path string) {
	var doc yaml.Node
	raw, err := yaml.Marshal(src)
	if err != nil || yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return
	}
	secName, key := splitPath(path)
	n := child(child(doc.Content[0], secName), key)
	if n == nil {
		return
	}
	probe := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setNode(probe, path, n)
	_ = decodeStrict(probe, dst)
}

// DefaultYAML renvoie le modèle commenté embarqué dans le binaire.
func DefaultYAML() []byte { return defaultConfigYAML }
