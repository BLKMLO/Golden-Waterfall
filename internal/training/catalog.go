package training

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RunSummary : ce que l'interface affiche pour un entraînement archivé.
// Lu depuis run.json — jamais reconstitué de mémoire.
type RunSummary struct {
	RunID      string    `json:"run_id"`
	Strategy   string    `json:"strategy"`
	Symbols    []string  `json:"symbols"`
	Timeframe  string    `json:"timeframe"`
	StartedAt  time.Time `json:"started_at"`
	Trades     int       `json:"trades"`
	WinRate    float64   `json:"win_rate"`
	NetPnL     float64   `json:"net_pnl"`
	ReturnPct  float64   `json:"return_pct"`
	MeanOOSAUC float64   `json:"mean_oos_auc"`
	HasAUC     bool      `json:"has_mean_oos_auc"`
	FinalDir   string    `json:"final_model_dir"`
	Dir        string    `json:"-"`
}

// ListRuns inventorie les entraînements archivés, du plus récent au plus
// ancien. Un dossier sans run.json exploitable est IGNORÉ (run interrompu)
// plutôt que présenté avec des chiffres inventés.
func ListRuns(modelsDir string) ([]RunSummary, error) {
	strategies, err := os.ReadDir(modelsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RunSummary
	for _, s := range strategies {
		if !s.IsDir() {
			continue
		}
		runs, err := os.ReadDir(filepath.Join(modelsDir, s.Name()))
		if err != nil {
			continue
		}
		for _, run := range runs {
			if !run.IsDir() {
				continue
			}
			dir := filepath.Join(modelsDir, s.Name(), run.Name())
			summary, err := readRun(dir)
			if err != nil {
				continue
			}
			summary.Dir = dir
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func readRun(dir string) (RunSummary, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return RunSummary{}, err
	}
	var full Result
	if err := json.Unmarshal(raw, &full); err != nil {
		return RunSummary{}, err
	}
	return RunSummary{
		RunID:      full.RunID,
		Strategy:   full.Strategy,
		Symbols:    full.Symbols,
		Timeframe:  full.Timeframe,
		StartedAt:  full.StartedAt,
		Trades:     full.Aggregate.Trades,
		WinRate:    full.Aggregate.WinRate,
		NetPnL:     full.Aggregate.NetPnL,
		ReturnPct:  full.Aggregate.ReturnPct,
		MeanOOSAUC: full.MeanOOSAUC,
		HasAUC:     full.HasMeanAUC,
		FinalDir:   full.FinalDir,
	}, nil
}

// LoadRun relit le détail complet d'un entraînement archivé.
func LoadRun(dir string) (*Result, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return nil, err
	}
	var full Result
	if err := json.Unmarshal(raw, &full); err != nil {
		return nil, fmt.Errorf("run.json illisible (%s) : %w", dir, err)
	}
	return &full, nil
}

// DeleteRun supprime un entraînement archivé (modèles compris).
// Refuse tout chemin qui ne serait pas SOUS le dossier des modèles : une
// faute de frappe ne doit pas pouvoir effacer autre chose.
func DeleteRun(modelsDir, dir string) error {
	absRoot, err := filepath.Abs(modelsDir)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		len(rel) >= 2 && rel[:2] == ".." {
		return fmt.Errorf("refus de supprimer %s : hors du dossier des modèles", dir)
	}
	return os.RemoveAll(absDir)
}

// SelectModel choisit le modèle à utiliser en LIVE pour un symbole.
//
// Règle : le modèle de PRODUCTION (`final/`) de l'entraînement le plus
// récent de cette stratégie qui couvre le symbole. Pour une stratégie
// mono-actif, le sous-dossier du symbole.
//
// Renvoie une chaîne vide et une explication quand aucun modèle ne
// convient : la stratégie reste alors muette et l'interface dit pourquoi,
// au lieu de laisser croire à un marché sans opportunité.
func SelectModel(modelsDir, strategyName, symbol string) (string, string) {
	runs, err := ListRuns(modelsDir)
	if err != nil {
		return "", fmt.Sprintf("dossier des modèles illisible : %v", err)
	}
	found := false
	for _, run := range runs {
		if run.Strategy != strategyName || run.FinalDir == "" {
			continue
		}
		found = true
		if !containsString(run.Symbols, symbol) {
			continue
		}
		// Mutualisé : le dossier final porte directement le modèle.
		if hasModel(run.FinalDir) {
			return run.FinalDir, ""
		}
		// Mono-actif : un sous-dossier par symbole.
		perSymbol := filepath.Join(run.FinalDir, symbol)
		if hasModel(perSymbol) {
			return perSymbol, ""
		}
	}
	if !found {
		return "", fmt.Sprintf("aucun entraînement archivé pour la stratégie %q", strategyName)
	}
	return "", fmt.Sprintf("aucun modèle %q ne couvre %s", strategyName, symbol)
}

func hasModel(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "model.json")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "metadata.json"))
	return err == nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
