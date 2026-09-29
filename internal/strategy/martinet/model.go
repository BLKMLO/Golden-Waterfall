package martinet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// modelMeta : le modèle TOUT ENTIER tient dans le manifeste
// (`strategy.ModelManifest`) — la cible et la force de pivot retenues, et
// la définition sous laquelle elles l'ont été, pour qu'un modèle ne soit
// jamais appliqué par une autre révision, à une autre paire ou à une
// autre unité de temps.
type modelMeta struct {
	Strategy  string `json:"strategy"`
	Version   string `json:"version"`
	Timeframe string `json:"timeframe"`
	// Symbol : un modèle PAR paire.
	Symbol string `json:"symbol"`
	// Réglage retenu par le calibrage (ou de repli).
	RR    float64 `json:"rr"`
	Pivot int     `json:"pivot"`
	// Définition figée de la révision, vérifiée au chargement.
	Window        int     `json:"window"`
	Lookback      int     `json:"lookback"`
	ATRPeriod     int     `json:"atr_period"`
	MaxSweepATR   float64 `json:"max_sweep_atr"`
	StopBufferATR float64 `json:"stop_buffer_atr"`
	MaxRiskATR    float64 `json:"max_risk_atr"`
	MaxSpreadR    float64 `json:"max_spread_r"`
	SessionFrom   int     `json:"session_from_utc"`
	SessionTo     int     `json:"session_to_utc"`
	MaxHoldMin    int     `json:"max_hold_minutes"`
	// Ce que le calibrage a essayé et retenu.
	Calibration *calibration `json:"calibration"`
	TrainFrom   time.Time    `json:"train_from"`
	TrainTo     time.Time    `json:"train_to"`
	Seed        int64        `json:"seed"`
	TrainedAt   time.Time    `json:"trained_at"`
}

func (r revision) newMeta() *modelMeta {
	return &modelMeta{
		Strategy:      r.name,
		Version:       r.version,
		Window:        r.window,
		Lookback:      r.lookback,
		ATRPeriod:     r.atrPeriod,
		MaxSweepATR:   r.maxSweepATR,
		StopBufferATR: r.stopBufferATR,
		MaxRiskATR:    r.maxRiskATR,
		MaxSpreadR:    r.maxSpreadR,
		SessionFrom:   r.sessionFrom,
		SessionTo:     r.sessionTo,
		MaxHoldMin:    int(r.maxHold / time.Minute),
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
		// Une zone de 120 bougies M1 n'est pas une zone de 120 bougies
		// M15 : le réglage calibré n'y voudrait plus rien dire.
		return nil, fmt.Errorf("modèle calibré en %s, appliqué en %s — le réentraîner", m.Timeframe, timeframe)
	}
	if !r.sameDefinition(&m) {
		return nil, fmt.Errorf("%s de %s : définition différente de %s — le réentraîner",
			strategy.ModelManifest, dir, r.name)
	}
	return &m, nil
}

// sameDefinition : le modèle a-t-il été produit sous CETTE définition, et
// son réglage est-il un point de SA grille (ou son repli) ?
func (r revision) sameDefinition(m *modelMeta) bool {
	want := r.newMeta()
	if m.Window != want.Window || m.Lookback != want.Lookback || m.ATRPeriod != want.ATRPeriod ||
		m.MaxSweepATR != want.MaxSweepATR || m.StopBufferATR != want.StopBufferATR ||
		m.MaxRiskATR != want.MaxRiskATR || m.MaxSpreadR != want.MaxSpreadR ||
		m.SessionFrom != want.SessionFrom || m.SessionTo != want.SessionTo ||
		m.MaxHoldMin != want.MaxHoldMin || m.Calibration == nil {
		return false
	}
	if m.RR == r.fallbackRR && m.Pivot == r.fallbackPivot {
		return true
	}
	return contains(r.rrGrid, m.RR) && contains(r.pivotGrid, float64(m.Pivot))
}

func contains(list []float64, v float64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
