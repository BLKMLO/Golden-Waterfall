package colibri

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/ml/gbdt"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// modelMeta accompagne les fichiers de modèle : il fige TOUT ce dont
// l'inférence a besoin pour reproduire exactement les conditions de
// l'entraînement. C'est le manifeste (`strategy.ModelManifest`) par lequel
// le catalogue reconnaît un dossier de modèle.
type modelMeta struct {
	Strategy  string   `json:"strategy"`
	Version   string   `json:"version"`
	Timeframe string   `json:"timeframe"`
	Symbols   []string `json:"symbols"`
	// SymbolCategories : liste ORDONNÉE des symboles vus à
	// l'entraînement. L'encodage de la feature catégorielle `symbol` en
	// dépend : sans cette liste figée, un modèle rechargé associerait le
	// code 3 à une autre paire qu'au fit — des probabilités crédibles et
	// fausses.
	SymbolCategories []string  `json:"symbol_categories,omitempty"`
	FeatureColumns   []string  `json:"feature_columns"`
	LongThreshold    float64   `json:"long_threshold"`
	ShortThreshold   float64   `json:"short_threshold"`
	BarrierATRMult   float64   `json:"barrier_atr_mult"`
	MaxHoldDays      int       `json:"max_hold_days"`
	Seed             int64     `json:"seed"`
	TrainedAt        time.Time `json:"trained_at"`
	Samples          int       `json:"samples"`
	PositiveRate     float64   `json:"positive_rate"`

	// --- À partir de colibri_v1_2 ------------------------------------------

	// Heads : têtes du modèle, un fichier model_<tête>.json chacune.
	Heads []string `json:"heads,omitempty"`
	// HeadStats : ce que chaque tête a MESURÉ sur son jeu d'entraînement,
	// dont la règle de décision a besoin.
	HeadStats map[string]headStats `json:"head_stats,omitempty"`
	// MinEdgeR : marge d'espérance nette exigée pour entrer.
	MinEdgeR float64 `json:"min_edge_r,omitempty"`
	// CostsModelled : false quand aucune série d'entraînement n'avait de
	// côté ask — la cible est alors BRUTE, et c'est écrit ici.
	CostsModelled bool `json:"costs_modelled,omitempty"`
	// Purged : lignes retirées de l'entraînement parce que leur label
	// débordait sur la validation de l'arrêt anticipé.
	Purged int `json:"purged,omitempty"`
}

// headStats : issue MOYENNE d'un trade, en unités de barrière (R), BRUTE
// de coûts, selon que le label a été gagnant ou perdant. L'espérance
// d'une entrée s'en déduit sans supposer que chaque gain vaut +1 R et
// chaque perte −1 R — les sorties au temps et les stops en gap le
// démentent.
type headStats struct {
	WinR         float64 `json:"win_r"`
	LossR        float64 `json:"loss_r"`
	PositiveRate float64 `json:"positive_rate"`
	Samples      int     `json:"samples"`
	// Calibration : rétrécissement vers le taux de base d'entraînement,
	// ajusté sur la validation de l'arrêt anticipé (gbdt.FitShrinkage).
	// Calibrated = false quand il n'y avait pas de validation : la
	// probabilité est alors utilisée brute, et c'est dit.
	Calibration gbdt.Shrinkage `json:"calibration"`
	Calibrated  bool           `json:"calibrated"`
}

// loaded : un modèle chargé (une ou deux têtes) et son manifeste.
type loaded struct {
	dir   string
	heads []*gbdt.Model // dans l'ordre de revision.heads()
	meta  *modelMeta
}

// sameColumns compare deux listes de colonnes et nomme la PREMIÈRE
// divergence. Un message qui dit seulement « incompatible » oblige à
// fouiller ; celui-ci pointe la colonne fautive.
func sameColumns(got, want []string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%d colonnes, %d attendues", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("colonne %d : %q au lieu de %q", i, got[i], want[i])
		}
	}
	return nil
}

// loadModel relit un dossier de modèle et vérifie qu'il appartient bien à
// CETTE révision, colonne par colonne.
func (c *colibri) loadModel(dir string) (*loaded, error) {
	metaRaw, err := os.ReadFile(filepath.Join(dir, strategy.ModelManifest))
	if err != nil {
		return nil, fmt.Errorf("%s manquant dans %s : %w", strategy.ModelManifest, dir, err)
	}
	var meta modelMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, fmt.Errorf("%s illisible dans %s : %w", strategy.ModelManifest, dir, err)
	}
	if meta.Strategy != c.rev.name {
		return nil, fmt.Errorf("modèle de la stratégie %q chargé par %q — refusé",
			meta.Strategy, c.rev.name)
	}
	// Vérification des colonnes par leur NOM et leur ORDRE, pas seulement
	// par leur nombre.
	//
	// Un simple compte laisserait passer un modèle dont les colonnes ont
	// été réordonnées ou renommées entre deux versions du code : la
	// prédiction lirait alors le RSI là où le modèle attend l'ATR, et
	// produirait des probabilités parfaitement crédibles et parfaitement
	// fausses. C'est le pire mode de défaillance possible ici, parce que
	// rien à l'écran ne le trahirait.
	expected := c.rev.columns()
	out := &loaded{dir: dir, meta: &meta}
	for _, head := range c.rev.heads() {
		path := filepath.Join(dir, headFile(head))
		model, err := gbdt.LoadModel(path)
		if err != nil {
			return nil, err
		}
		if err := sameColumns(model.FeatureNames, expected); err != nil {
			return nil, fmt.Errorf("modèle %s incompatible avec %s : %w — le réentraîner",
				path, c.rev.name, err)
		}
		out.heads = append(out.heads, model)
	}
	if len(meta.FeatureColumns) > 0 {
		if err := sameColumns(meta.FeatureColumns, expected); err != nil {
			return nil, fmt.Errorf("%s de %s incohérent : %w — le réentraîner",
				strategy.ModelManifest, dir, err)
		}
	}
	if c.rev.target == sidedTarget {
		for _, head := range c.rev.heads() {
			if _, ok := meta.HeadStats[head]; !ok {
				return nil, fmt.Errorf("%s de %s sans statistiques pour la tête %q — le réentraîner",
					strategy.ModelManifest, dir, head)
			}
		}
	}
	return out, nil
}

// save écrit les têtes puis le manifeste — le manifeste EN DERNIER : un
// dossier interrompu en cours d'écriture n'est pas reconnu comme un modèle.
func (l *loaded) save(rev revision) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	for i, head := range rev.heads() {
		if err := l.heads[i].Save(filepath.Join(l.dir, headFile(head))); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(l.meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.dir, strategy.ModelManifest), raw, 0o644)
}
