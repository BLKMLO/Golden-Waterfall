package config

import (
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

func TestUnknownKeyIsRejected(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o755)
	// « max_position » au singulier : une faute de frappe qui, si elle
	// passait, laisserait une limite de risque jamais appliquée.
	os.WriteFile(p.ConfigFile(), []byte("risk:\n  max_position: 3\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("une clé inconnue doit faire échouer le chargement")
	} else if !strings.Contains(err.Error(), "max_position") {
		t.Fatalf("le message doit nommer la clé fautive : %v", err)
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

func TestInvalidEnvValueIsAnError(t *testing.T) {
	p := tempPaths(t)
	t.Setenv("GW_BROKER_PORT", "pas-un-nombre")
	if _, err := Load(p); err == nil {
		t.Fatal("une valeur d'environnement invalide doit être refusée")
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
