package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{ConfigDir: filepath.Join(dir, "cfg"), DataDir: filepath.Join(dir, "data")}
}

func TestLoadCreatesDefaultFileOnFirstRun(t *testing.T) {
	p := tempPaths(t)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.ConfigFile()); err != nil {
		t.Fatal("le fichier de configuration doit être créé au premier lancement")
	}
	if cfg.Strategy.Name != "colibri_v1_2" {
		t.Fatalf("stratégie par défaut inattendue : %q", cfg.Strategy.Name)
	}
	raw, _ := os.ReadFile(p.ConfigFile())
	if !strings.Contains(string(raw), "#") {
		t.Fatal("le modèle écrit doit être COMMENTÉ (c'est sa principale utilité)")
	}
}

// TestUnknownKeyIsRemovedAndAnnounced : une clé que cette version ne
// connaît pas (retirée par une mise à jour, ou faute de frappe) est
// supprimée du fichier — sauvegardé avant — et la réparation NOMME la clé.
// Rien n'est ignoré en silence : « max_position » au singulier, qui
// laisserait croire à une limite de risque, apparaît dans les réparations.
func TestUnknownKeyIsRemovedAndAnnounced(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte("risk:\n  max_position: 3\n  max_open_positions: 3\n"), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repairs) != 1 || cfg.Repairs[0].Key != "risk.max_position" || cfg.Repairs[0].New != "supprimée" {
		t.Fatalf("réparation attendue nommant risk.max_position : %+v", cfg.Repairs)
	}
	if cfg.Risk.MaxOpenPositions != 3 {
		t.Fatal("les réglages valides du fichier doivent être gardés")
	}
	raw, _ := os.ReadFile(p.ConfigFile())
	if strings.Contains(string(raw), "max_position:") || !strings.Contains(string(raw), "max_open_positions: 3") {
		t.Fatalf("fichier mal réparé :\n%s", raw)
	}
	if old, err := os.ReadFile(cfg.Backup); err != nil || !strings.Contains(string(old), "max_position: 3") {
		t.Fatalf("l'ancien fichier doit être sauvegardé (%q) : %v", cfg.Backup, err)
	}
	// Au lancement suivant, plus rien à réparer.
	again, err := Load(p)
	if err != nil || len(again.Repairs) != 0 || again.Backup != "" {
		t.Fatalf("un fichier réparé ne doit plus rien réparer : %+v, %v", again.Repairs, err)
	}
}

// TestRefusedValuesAreResetToDefaultKeepingComments : une valeur refusée
// revient à son défaut, une valeur illisible aussi ; les commentaires et
// les autres réglages restent.
func TestRefusedValuesAreResetToDefaultKeepingComments(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte(`# mon réglage
broker:
  mode: "demo"      # valeur d'une vieille version
  port: "abc"
  host: "10.0.0.9"
risk:
  fixed_position_size: 50000
  max_position_size: 20000
`), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if cfg.Broker.Mode != d.Broker.Mode || cfg.Broker.Port != d.Broker.Port || cfg.Broker.Host != "10.0.0.9" {
		t.Fatalf("broker mal réparé : %+v", cfg.Broker)
	}
	// Incohérence entre deux clés : la PREMIÈRE citée revient à son défaut
	// (10 000 ≤ 20 000), la seconde, cohérente alors, est gardée.
	if cfg.Risk.FixedPositionSize != d.Risk.FixedPositionSize || cfg.Risk.MaxPositionSize != 20000 {
		t.Fatalf("risque mal réparé : %+v", cfg.Risk)
	}
	keys := strings.Join(RepairKeys(cfg.Repairs), ",")
	if keys != "broker.mode,broker.port,risk.fixed_position_size" {
		t.Fatalf("réparations : %s", keys)
	}
	raw, _ := os.ReadFile(p.ConfigFile())
	for _, want := range []string{"# mon réglage", "valeur d'une vieille version", "mode: paper", "host: \"10.0.0.9\""} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("« %s » absent du fichier réparé :\n%s", want, raw)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestSyntaxErrorStillStops : on ne peut pas savoir ce qu'un fichier
// illisible voulait dire — refus, avec le remède.
func TestSyntaxErrorStillStops(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte("broker:\n  mode: [paper\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "syntaxe") {
		t.Fatalf("erreur de syntaxe : refus nommant la syntaxe attendu, reçu %v", err)
	}
}

// TestNotATableIsReplacedByTheTemplate : un fichier qui n'est pas une
// table est sauvegardé puis remplacé par le modèle.
func TestNotATableIsReplacedByTheTemplate(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte("bonjour\n"), 0o644)
	cfg, err := Load(p)
	if err != nil || len(cfg.Repairs) != 1 || cfg.Backup == "" {
		t.Fatalf("remplacement annoncé attendu : %+v, %v", cfg.Repairs, err)
	}
	raw, _ := os.ReadFile(p.ConfigFile())
	if string(raw) != string(DefaultYAML()) {
		t.Fatal("le modèle doit remplacer le fichier")
	}
}

// TestFixPersistsAndAnnounces : Fix (appelé par app.New pour les registres)
// écrit la nouvelle valeur dans le fichier, après sauvegarde.
func TestFixPersistsAndAnnounces(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte("strategy:\n  name: martinet_v1_0 # ancien\n"), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Fix(&cfg, "strategy.name", "martinet_v1_1", "révision retirée"); err != nil {
		t.Fatal(err)
	}
	if cfg.Strategy.Name != "martinet_v1_1" || len(cfg.Repairs) != 1 || cfg.Backup == "" {
		t.Fatalf("réparation mal appliquée : %+v %+v", cfg.Strategy, cfg.Repairs)
	}
	back, _ := Load(p)
	if back.Strategy.Name != "martinet_v1_1" {
		t.Fatal("la réparation doit être écrite dans config.yaml")
	}
	if err := Fix(&back, "strategy.name", "troglodyte_v1_1", "deuxième réparation"); err != nil {
		t.Fatal(err)
	}
	twice, _ := os.ReadFile(p.ConfigFile())
	if n := strings.Count(string(twice), repairHeader); n != 1 {
		t.Fatalf("une seule ligne d'en-tête de réparation attendue, %d :\n%s", n, twice)
	}
	Fix(&back, "strategy.name", "martinet_v1_1", "retour")
	raw, _ := os.ReadFile(p.ConfigFile())
	if !strings.Contains(string(raw), "# ancien") {
		t.Fatalf("le commentaire de la ligne doit rester :\n%s", raw)
	}
	// Valeur forcée par l'environnement : ignorée, fichier intact.
	t.Setenv("GW_STRATEGY", "martinet_v1_0")
	env, _ := Load(p)
	before, _ := os.ReadFile(p.ConfigFile())
	if err := Fix(&env, "strategy.name", "martinet_v1_1", "révision retirée"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p.ConfigFile())
	if string(before) != string(after) || env.Repairs[len(env.Repairs)-1].Env != "GW_STRATEGY" {
		t.Fatal("une valeur venue de l'environnement ne doit pas réécrire le fichier")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	os.WriteFile(p.ConfigFile(), []byte("broker:\n  mode: paper\n"), 0o644)

	t.Setenv("GW_MODE", "live")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// L'environnement a le dernier mot : une variable qu'on prend la peine
	// d'exporter doit agir. L'inverse — un fichier qui prime en silence —
	// rend une variable exportée inopérante sans jamais le dire.
	if cfg.Broker.Mode != "live" {
		t.Fatalf("GW_MODE doit primer sur le fichier, reçu %q", cfg.Broker.Mode)
	}
	if !cfg.Live() {
		t.Fatal("Live() doit suivre le mode effectif")
	}
}

// TestInvalidEnvValueIsIgnoredAndAnnounced : une variable GW_* refusée est
// ignorée pour cette exécution, en le disant ; le fichier n'est pas touché.
func TestInvalidEnvValueIsIgnoredAndAnnounced(t *testing.T) {
	p := tempPaths(t)
	t.Setenv("GW_BROKER_PORT", "pas-un-nombre")
	t.Setenv("GW_MODE", "demo")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Broker.Port != Default().Broker.Port || cfg.Broker.Mode != Default().Broker.Mode {
		t.Fatalf("valeurs du fichier attendues : %+v", cfg.Broker)
	}
	envs := map[string]bool{}
	for _, r := range cfg.Repairs {
		envs[r.Env] = true
	}
	if !envs["GW_BROKER_PORT"] || !envs["GW_MODE"] || cfg.Backup != "" {
		t.Fatalf("variables ignorées ET annoncées, fichier intact, attendus : %+v", cfg.Repairs)
	}
}

func TestValidateRejectsDangerousValues(t *testing.T) {
	cases := map[string]func(*Config){
		"taille de position nulle":     func(c *Config) { c.Risk.MaxPositionSize = 0 },
		"mode inconnu":                 func(c *Config) { c.Broker.Mode = "demo" },
		"capital négatif":              func(c *Config) { c.Backtest.InitialCapital = -1 },
		"levier inférieur à 1":         func(c *Config) { c.Backtest.Leverage = 0.5 },
		"un seul pli":                  func(c *Config) { c.Training.Folds = 1 },
		"aucun instrument":             func(c *Config) { c.History.Instruments = nil },
		"concurrence excessive":        func(c *Config) { c.History.Concurrency = 50 },
		"perte journalière > 100 %":    func(c *Config) { c.Risk.MaxDailyLossPct = 150 },
		"niveau de journal inconnu":    func(c *Config) { c.Logging.Level = "bavard" },
		"rafraîchissement trop rapide": func(c *Config) { c.UI.RefreshMillis = 1 },
		"thème inconnu":                func(c *Config) { c.UI.Theme = "néon" },
	}
	for name, mutate := range cases {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s : doit être refusé au démarrage", name)
		}
	}
}

func TestAccountCapMustCoverSymbolCap(t *testing.T) {
	cfg := Default()
	cfg.Risk.MaxPositionsPerSymbol = 3
	cfg.Risk.MaxOpenPositions = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("un plafond de compte inférieur au plafond par symbole est incohérent")
	}
}

func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := Default()
	cfg.Broker.Mode = "demo"
	cfg.Risk.MaxPositionSize = -1
	cfg.Training.Folds = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("configuration invalide acceptée")
	}
	msg := err.Error()
	for _, want := range []string{"broker.mode", "max_position_size", "training.folds"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("le rapport doit citer %q : %s", want, msg)
		}
	}
}

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("la configuration par défaut doit être valide : %v", err)
	}
}

func TestEmbeddedTemplateParsesToValidConfig(t *testing.T) {
	// Le modèle livré dans le binaire DOIT se relire lui-même : sinon le
	// premier lancement crée un fichier que le second refuse.
	p := tempPaths(t)
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p) // second chargement : relit le fichier écrit
	if err != nil {
		t.Fatalf("le modèle embarqué ne se relit pas : %v", err)
	}
	if len(cfg.History.Instruments) != 31 {
		t.Fatalf("%d instruments relus depuis le modèle", len(cfg.History.Instruments))
	}
}

func TestPathsHonourEnvironment(t *testing.T) {
	t.Setenv("GW_CONFIG_DIR", "/tmp/cfg-test")
	t.Setenv("GW_DATA_DIR", "/tmp/data-test")
	p := DefaultPaths()
	if p.ConfigDir != "/tmp/cfg-test" || p.DataDir != "/tmp/data-test" {
		t.Fatalf("les variables d'environnement doivent primer : %+v", p)
	}
	if p.HistoryDir() != filepath.Join("/tmp/data-test", "history") {
		t.Fatalf("chemin d'historique inattendu : %s", p.HistoryDir())
	}
}

func TestLiveSymbolsFallBackToInstruments(t *testing.T) {
	cfg := Default()
	if len(cfg.LiveSymbols()) != len(cfg.History.Instruments) {
		t.Fatal("sans liste dédiée, les paires live sont celles de l'historique")
	}
	cfg.Broker.Symbols = []string{"EURUSD"}
	if len(cfg.LiveSymbols()) != 1 {
		t.Fatal("la liste dédiée doit primer")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := tempPaths(t)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Strategy.Enabled = true
	cfg.Risk.MaxOpenPositions = 7
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatalf("la configuration réécrite doit se relire : %v", err)
	}
	if !back.Strategy.Enabled || back.Risk.MaxOpenPositions != 7 {
		t.Fatalf("valeurs perdues à la sauvegarde : %+v", back.Risk)
	}
}

func TestHistorySourceKey(t *testing.T) {
	if Default().History.Source != "dukascopy" {
		t.Fatal("la source par défaut doit rester dukascopy : changer de source change l'historique")
	}
	c := Default()
	c.History.Source = " "
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "history.source") {
		t.Fatalf("source vide : refus nommant la clé attendu (%v)", err)
	}
	dir := t.TempDir()
	t.Setenv("GW_CONFIG_DIR", dir)
	t.Setenv("GW_DATA_DIR", dir)
	t.Setenv("GW_HISTORY_SOURCE", "fxcm")
	cfg, err := Load(DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.History.Source != "fxcm" {
		t.Fatalf("GW_HISTORY_SOURCE ignorée : %q", cfg.History.Source)
	}
	if EnvOverrides()["history.source"] != "GW_HISTORY_SOURCE" {
		t.Fatal("l'écran Paramètres doit savoir que history.source est forcée par l'environnement")
	}
}

// TestEveryEnvironmentVariableActs : une variable GW_* documentée qui
// n'agirait pas serait un piège (cf. ui.theme).
func TestEveryEnvironmentVariableActs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GW_CONFIG_DIR", dir)
	t.Setenv("GW_DATA_DIR", dir)
	env := map[string]string{
		"GW_BROKER": "interactive_brokers", "GW_MODE": "live", "GW_BROKER_HOST": "10.0.0.2",
		"GW_BROKER_PORT": "4002", "GW_BROKER_CLIENT_ID": "7", "GW_BROKER_ACCOUNT": "DU123",
		"GW_STRATEGY": "troglodyte_v1_1", "GW_STRATEGY_ENABLED": "true", "GW_LOG_LEVEL": "debug",
		"GW_THEME": "light", "GW_TIMEFRAME": "H1", "GW_SEED": "9", "GW_NEWS": "false",
		"GW_HISTORY_SOURCE": "fxcm",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := Load(DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Broker
	if b.Name != "interactive_brokers" || b.Mode != "live" || b.Host != "10.0.0.2" || b.Port != 4002 ||
		b.ClientID != 7 || b.Account != "DU123" {
		t.Fatalf("broker : %+v", b)
	}
	if cfg.Strategy.Name != "troglodyte_v1_1" || !cfg.Strategy.Enabled || cfg.Logging.Level != "debug" ||
		cfg.UI.Theme != "light" || cfg.Training.Timeframe != "H1" || cfg.Training.Seed != 9 ||
		cfg.News.Enabled || cfg.History.Source != "fxcm" {
		t.Fatalf("configuration : %+v", cfg)
	}
	if len(EnvOverrides()) != len(env) {
		t.Fatalf("chaque variable doit être déclarée comme surcharge : %v", EnvOverrides())
	}
	for _, bad := range []struct{ key, value string }{
		{"GW_SEED", "x"}, {"GW_NEWS", "peut-être"}, {"GW_BROKER_PORT", "port"},
	} {
		t.Setenv(bad.key, bad.value)
		cfg, err := Load(DefaultPaths())
		found := false
		for _, r := range cfg.Repairs {
			found = found || r.Env == bad.key
		}
		if err != nil || !found {
			t.Errorf("%s=%s : variable ignorée ET annoncée attendue (%v, %+v)", bad.key, bad.value, err, cfg.Repairs)
		}
		t.Setenv(bad.key, env[bad.key])
	}
}

func TestDefaultPathsWithoutOverride(t *testing.T) {
	t.Setenv("GW_CONFIG_DIR", "")
	t.Setenv("GW_DATA_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	p := DefaultPaths()
	if p.ConfigDir == "" || p.DataDir == "" {
		t.Fatalf("chemins vides : %+v", p)
	}
	for _, f := range []string{p.ConfigFile(), p.DatabaseFile(), p.HistoryDir(), p.ModelsDir(), p.LogFile()} {
		if !strings.Contains(f, "olden") {
			t.Errorf("%s : hors du dossier de l'application", f)
		}
	}
}

func TestKnownTimeframesAreAllValid(t *testing.T) {
	names := KnownTimeframes()
	if len(names) == 0 {
		t.Fatal("aucune unité de temps connue")
	}
	for _, n := range names {
		if _, err := parseTimeframeName(n); err != nil {
			t.Errorf("%s : %v", n, err)
		}
	}
	if _, err := parseTimeframeName("M7"); err == nil {
		t.Error("M7 accepté")
	}
	if len(DefaultYAML()) == 0 {
		t.Error("modèle de configuration embarqué vide")
	}
}

// TestSaveDoesNotPersistTheEnvironment : une clé forcée par GW_* garde,
// dans le fichier, la valeur du FICHIER. Sinon `GW_MODE=live gw` lancé
// une fois, puis un réglage anodin enregistré depuis l'écran Paramètres,
// laissaient `broker.mode: live` dans config.yaml pour toujours.
func TestSaveDoesNotPersistTheEnvironment(t *testing.T) {
	p := tempPaths(t)
	if _, err := Load(p); err != nil { // crée le fichier (mode paper)
		t.Fatal(err)
	}
	t.Setenv("GW_MODE", "live")
	t.Setenv("GW_THEME", "light")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Broker.Mode != "live" || cfg.UI.Theme != "light" {
		t.Fatalf("l'environnement doit primer à l'exécution : %s / %s", cfg.Broker.Mode, cfg.UI.Theme)
	}
	cfg.Risk.MaxOpenPositions = 7 // le réglage que l'utilisateur a vraiment changé
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GW_MODE")
	os.Unsetenv("GW_THEME")
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.Broker.Mode != "paper" || back.UI.Theme != "auto" {
		t.Fatalf("valeurs d'environnement écrites dans le fichier : mode %s, thème %s", back.Broker.Mode, back.UI.Theme)
	}
	if back.Risk.MaxOpenPositions != 7 {
		t.Fatalf("le réglage modifié doit être enregistré : %d", back.Risk.MaxOpenPositions)
	}
	if st, err := os.Stat(p.ConfigFile()); err == nil && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config.yaml créé avec des droits trop larges : %v", st.Mode().Perm())
	}
}

// TestNonFiniteNumbersAreRefused : NaN n'est ni « <= 0 » ni « > 100 » ;
// sans contrôle explicite, il traversait la validation et neutralisait
// les garde-fous du risque.
func TestNonFiniteNumbersAreRefused(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cases := map[string]func(*Config){
		"broker.replay_speed":       func(c *Config) { c.Broker.ReplaySpeed = nan },
		"risk.max_position_size":    func(c *Config) { c.Risk.MaxPositionSize = nan },
		"risk.fixed_position_size":  func(c *Config) { c.Risk.FixedPositionSize = inf },
		"risk.max_daily_loss_pct":   func(c *Config) { c.Risk.MaxDailyLossPct = nan },
		"risk.risk_per_trade_pct":   func(c *Config) { c.Risk.RiskPerTradePct = nan },
		"costs.commission_per_unit": func(c *Config) { c.Costs.CommissionPerUnit = inf },
		"backtest.initial_capital":  func(c *Config) { c.Backtest.InitialCapital = nan },
		"backtest.leverage":         func(c *Config) { c.Backtest.Leverage = nan },
		"backtest.account_currency": func(c *Config) { c.Backtest.AccountCurrency = "U$D" },
		"logging.buffer_size":       func(c *Config) { c.Logging.BufferSize = 1 << 40 },
		"ui.refresh_millis":         func(c *Config) { c.UI.RefreshMillis = 1 << 30 },
	}
	for key, mutate := range cases {
		c := Default()
		mutate(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s : valeur non finie ou démesurée acceptée (%v)", key, err)
		}
	}
}

// TestNaNInTheFileIsRepaired : « .nan » dans config.yaml est remis à son
// défaut au chargement, et c'est annoncé.
func TestNaNInTheFileIsRepaired(t *testing.T) {
	p := tempPaths(t)
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile(), []byte("risk:\n  max_position_size: .nan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Risk.MaxPositionSize != Default().Risk.MaxPositionSize {
		t.Fatalf("plafond NaN non réparé : %v", cfg.Risk.MaxPositionSize)
	}
	found := false
	for _, r := range cfg.Repairs {
		found = found || r.Key == "risk.max_position_size"
	}
	if !found {
		t.Fatalf("la réparation doit être annoncée : %+v", cfg.Repairs)
	}
}
