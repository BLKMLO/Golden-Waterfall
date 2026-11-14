package sterne

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// modelMeta : le modèle TOUT ENTIER tient dans le manifeste
// (`strategy.ModelManifest`) — les fenêtres retenues, et la définition
// sous laquelle elles l'ont été, pour qu'un modèle ne soit jamais appliqué
// par une autre révision, à une autre paire ou à une autre unité de temps.
type modelMeta struct {
	Strategy  string `json:"strategy"`
	Version   string `json:"version"`
	Timeframe string `json:"timeframe"`
	// Symbol : un modèle PAR paire.
	Symbol string `json:"symbol"`
	// Windows : fenêtres retenues par le calibrage. Vide = aucune n'a
	// passé le seuil : le modèle est valide et ne trade pas.
	Windows []string `json:"windows"`
	// Définition figée de la révision, vérifiée au chargement.
	Grid      string  `json:"grid"`
	HoldMin   int     `json:"hold_minutes"`
	ATRWindow int     `json:"atr_window"`
	ATRPeriod int     `json:"atr_period"`
	StopATR   float64 `json:"stop_atr"`
	MinScore  float64 `json:"min_score"`
	MinTrades int     `json:"min_trades"`
	// Ce que le calibrage a essayé et retenu.
	Calibration *calibration `json:"calibration"`
	TrainFrom   time.Time    `json:"train_from"`
	TrainTo     time.Time    `json:"train_to"`
	Seed        int64        `json:"seed"`
	TrainedAt   time.Time    `json:"trained_at"`
}

// gridSignature : la grille des fenêtres, écrite en clair. Deux révisions
// dont une fenêtre diffère d'une minute n'ont pas la même signature.
func (r revision) gridSignature() string {
	parts := make([]string, len(r.windows))
	for i, w := range r.windows {
		parts[i] = fmt.Sprintf("%s@%s %02d:%02d usd%+d", w.name, w.zone, w.hour, w.minute, w.usd)
	}
	return strings.Join(parts, " ; ")
}

func (r revision) newMeta() *modelMeta {
	return &modelMeta{
		Strategy:  r.name,
		Version:   r.version,
		Windows:   []string{},
		Grid:      r.gridSignature(),
		HoldMin:   int(r.hold / time.Minute),
		ATRWindow: r.atrWindow,
		ATRPeriod: r.atrPeriod,
		StopATR:   r.stopATR,
		MinScore:  r.minScore,
		MinTrades: r.minTrades,
	}
}

// save écrit le manifeste. Un seul fichier : rien ne peut rester à moitié
// écrit à côté d'un manifeste valide.
func (m *modelMeta) save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, strategy.ModelManifest), raw, 0o644)
}

// loadModel relit un modèle et vérifie qu'il appartient à CETTE révision,
// à CETTE paire et à CETTE unité de temps.
func (r revision) loadModel(dir, symbol, timeframe string) (*modelMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, strategy.ModelManifest))
	if err != nil {
		return nil, fmt.Errorf("%s manquant dans %s : %w", strategy.ModelManifest, dir, err)
	}
	var m modelMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s illisible dans %s : %w", strategy.ModelManifest, dir, err)
	}
	if m.Strategy != r.name {
		return nil, fmt.Errorf("modèle de la stratégie %q chargé par %q — refusé", m.Strategy, r.name)
	}
	if m.Symbol != symbol {
		return nil, fmt.Errorf("modèle calibré sur %s, demandé pour %s — refusé", m.Symbol, symbol)
	}
	if timeframe != "" && m.Timeframe != timeframe {
		return nil, fmt.Errorf("modèle calibré en %s, appliqué en %s — le réentraîner", m.Timeframe, timeframe)
	}
	want := r.newMeta()
	if m.Grid != want.Grid || m.HoldMin != want.HoldMin || m.ATRWindow != want.ATRWindow ||
		m.ATRPeriod != want.ATRPeriod || m.StopATR != want.StopATR || m.MinScore != want.MinScore ||
		m.MinTrades != want.MinTrades || m.Calibration == nil {
		return nil, fmt.Errorf("%s de %s : définition différente de %s — le réentraîner",
			strategy.ModelManifest, dir, r.name)
	}
	seen := map[string]bool{}
	for _, name := range m.Windows {
		if _, ok := r.windowByName(name); !ok || seen[name] {
			return nil, fmt.Errorf("%s de %s : fenêtre %q inconnue de %s ou répétée — le réentraîner",
				strategy.ModelManifest, dir, name, r.name)
		}
		seen[name] = true
	}
	return &m, nil
}
