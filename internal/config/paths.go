// Package config est la SOURCE UNIQUE de configuration du programme.
//
// Aucun autre paquet ne lit l'environnement ni le fichier YAML : tout passe
// par Load() puis par la structure Config. Cette règle est ce qui permet de
// savoir, en un seul endroit, d'où vient chaque réglage.
//
// Deux sources, dans cet ordre de priorité croissante :
//  1. les valeurs par défaut du code (Default()) ;
//  2. le fichier config.yaml ;
//  3. les variables d'environnement GW_* (utile en CI/conteneur).
//
// L'environnement a le dernier mot : c'est la convention attendue partout
// ailleurs, et une variable qu'on prend la peine d'exporter doit agir. Un
// fichier qui primerait sur elle la rendrait inopérante en silence.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths décrit où vivent le fichier de configuration et les données.
//
// Le binaire est UNIQUE et déplaçable : il n'écrit jamais à côté de
// lui-même. Tout le reste (historique, modèles, base, journaux, cache) va
// dans un dossier utilisateur dédié, conforme aux conventions de l'OS.
type Paths struct {
	// ConfigDir contient config.yaml.
	ConfigDir string
	// DataDir contient history/, models/, exports/, gw.db, logs/.
	DataDir string
}

const appDirUnix = "golden-waterfall"
const appDirOther = "GoldenWaterfall"

// DefaultPaths calcule les emplacements standard, ou ceux imposés par les
// variables d'environnement GW_CONFIG_DIR / GW_DATA_DIR (précieux pour les
// tests, les installations portables « clé USB » et les conteneurs).
//
// Emplacements par défaut :
//
//	Linux/BSD : ~/.config/golden-waterfall     ~/.local/share/golden-waterfall
//	macOS     : ~/Library/Application Support/GoldenWaterfall (les deux)
//	Windows   : %AppData%\GoldenWaterfall      %LocalAppData%\GoldenWaterfall
func DefaultPaths() Paths {
	p := Paths{}
	if v := os.Getenv("GW_CONFIG_DIR"); v != "" {
		p.ConfigDir = v
	}
	if v := os.Getenv("GW_DATA_DIR"); v != "" {
		p.DataDir = v
	}
	if p.ConfigDir != "" && p.DataDir != "" {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	var cfg, data string
	switch runtime.GOOS {
	case "windows":
		appData := envOr("AppData", filepath.Join(home, "AppData", "Roaming"))
		localAppData := envOr("LocalAppData", filepath.Join(home, "AppData", "Local"))
		cfg = filepath.Join(appData, appDirOther)
		data = filepath.Join(localAppData, appDirOther)
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support", appDirOther)
		cfg, data = base, base
	default:
		cfg = filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")), appDirUnix)
		data = filepath.Join(envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), appDirUnix)
	}
	if p.ConfigDir == "" {
		p.ConfigDir = cfg
	}
	if p.DataDir == "" {
		p.DataDir = data
	}
	return p
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ConfigFile est le chemin du fichier config.yaml.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.yaml") }

// HistoryDir contient l'historique M1 : history/<SYMBOLE>/<SYMBOLE>_m1_<année>.gwb
func (p Paths) HistoryDir() string { return filepath.Join(p.DataDir, "history") }

// ModelsDir contient un sous-dossier par entraînement réussi.
func (p Paths) ModelsDir() string { return filepath.Join(p.DataDir, "models") }

// DatabaseFile est la base embarquée (journal des trades, états, runs).
func (p Paths) DatabaseFile() string { return filepath.Join(p.DataDir, "gw.db") }

// ExportsDir contient les CSV produits par l'interface et par la ligne de
// commande. Un dossier à part : ce sont les seuls fichiers que
// l'utilisateur est censé ouvrir avec un autre outil.
func (p Paths) ExportsDir() string { return filepath.Join(p.DataDir, "exports") }

// LogFile est le journal applicatif.
func (p Paths) LogFile() string { return filepath.Join(p.DataDir, "logs", "gw.log") }

// EnsureDirs crée l'arborescence manquante. Appelée une fois au démarrage :
// aucun autre paquet n'a le droit de créer des dossiers à la volée.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{
		p.ConfigDir, p.DataDir, p.HistoryDir(), p.ModelsDir(), p.ExportsDir(),
		filepath.Dir(p.LogFile()),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
