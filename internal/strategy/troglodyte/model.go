package troglodyte

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// modelMeta : le modèle TOUT ENTIER tient dans le manifeste
// (`strategy.ModelManifest`) — trois variances, et la définition sous
// laquelle elles ont été estimées, pour qu'un modèle ne soit jamais
// appliqué par une autre.
type modelMeta struct {
	Strategy  string `json:"strategy"`
	Version   string `json:"version"`
	Timeframe string `json:"timeframe"`
	// Symbol : un modèle PAR paire. Chargé pour une autre, il est
	// refusé : les variances d'EURUSD ne décrivent pas USDJPY.
	Symbol string `json:"symbol"`
	params
	// Mesures de l'estimation.
	LogLikelihood float64 `json:"log_likelihood"`
	Observations  int     `json:"observations"`
	Iterations    int     `json:"iterations"`
	// Variances négligeables (solutions au bord, légitimes) et borne
	// haute touchée (modèle inadapté à la série) : voir mle.go.
	NegligibleEps  bool `json:"negligible_eps"`
	NegligibleZeta bool `json:"negligible_zeta"`
	AtUpperBound   bool `json:"at_upper_bound"`
	// Définition figée de la révision, vérifiée au chargement.
	Window    int     `json:"window"`
	EnterZ    float64 `json:"enter_z"`
	ExitZ     float64 `json:"exit_z"`
	StopATR   float64 `json:"stop_atr"`
	ATRPeriod int     `json:"atr_period"`
	// Période d'entraînement et graine (archivée : l'estimation est
	// déterministe et ne tire rien, mais le catalogue la lit partout).
	TrainFrom time.Time `json:"train_from"`
	TrainTo   time.Time `json:"train_to"`
	Seed      int64     `json:"seed"`
	TrainedAt time.Time `json:"trained_at"`
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
		return nil, fmt.Errorf("modèle estimé sur %s, demandé pour %s — refusé", m.Symbol, symbol)
	}
	if timeframe != "" && m.Timeframe != timeframe {
		// Les variances dépendent de la durée d'une bougie : celles du H4
		// appliquées au M15 donneraient des z sans rapport avec la réalité.
		return nil, fmt.Errorf("modèle estimé en %s, appliqué en %s — le réentraîner", m.Timeframe, timeframe)
	}
	if m.Window != r.window || m.EnterZ != r.enterZ || m.ExitZ != r.exitZ ||
		m.StopATR != r.stopATR || m.ATRPeriod != r.atrPeriod {
		return nil, fmt.Errorf("%s de %s : définition différente de %s — le réentraîner",
			strategy.ModelManifest, dir, r.name)
	}
	for name, v := range map[string]float64{"sigma2_eps": m.Eps, "sigma2_eta": m.Eta, "sigma2_zeta": m.Zeta} {
		if !(v > 0) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s de %s : %s = %v, une variance doit être finie et positive",
				strategy.ModelManifest, dir, name, v)
		}
	}
	return &m, nil
}
