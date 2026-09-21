package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/label"
	"github.com/BLKMLO/Golden-Waterfall/internal/ml/gbdt"
)

// symbolSet : jeu étiqueté d'UN symbole, déjà filtré.
type symbolSet struct {
	symbol string
	rows   []float64 // matrice à plat, cols colonnes
	labels []float64
	cols   int
	n      int
}

// buildSet calcule features + labels d'un symbole et ne garde que les
// lignes EXPLOITABLES.
//
// Anti-fuite, en deux temps :
//   - les features sont causales (aucune ne regarde après t) ;
//   - le label n'est défini que pour les bougies à fenêtre avant COMPLÈTE.
//
// Le filtre « ligne de features complète ET label défini » retire donc à
// la fois la chauffe (au début) et la queue non étiquetable (à la fin).
// C'est un filtrage EXPLICITE : rien n'est retiré en silence.
func buildSet(symbol string, series core.Series, pooled bool, categories []string) symbolSet {
	m := feature.Compute(series)
	lab := label.Default(series)
	if pooled {
		m = m.AppendColumn(symbolColumnName, symbolCodeColumn(symbol, categories, m.Rows))
	}
	set := symbolSet{symbol: symbol, cols: m.Cols}
	set.rows = make([]float64, 0, m.Rows*m.Cols/2)
	set.labels = make([]float64, 0, m.Rows/2)
	for i := 0; i < m.Rows; i++ {
		if !lab.Defined(i) || !rowUsable(m, i, pooled) {
			continue
		}
		set.rows = append(set.rows, m.Row(i)...)
		set.labels = append(set.labels, lab.Value[i])
		set.n++
	}
	return set
}

func symbolCodeColumn(symbol string, categories []string, rows int) []float64 {
	code := math.NaN()
	for i, s := range categories {
		if s == symbol {
			code = float64(i)
			break
		}
	}
	out := make([]float64, rows)
	for i := range out {
		out[i] = code
	}
	return out
}

// Train entraîne un modèle sur un ou plusieurs symboles.
//
// Règles de construction du jeu (anti-fuite ENTRE ACTIFS) :
//   - features et labels sont calculés SYMBOLE PAR SYMBOLE — jamais sur
//     une concaténation des OHLC bruts, sinon les fenêtres glissantes
//     déborderaient d'un actif sur l'autre ;
//   - la colonne `symbol` est ajoutée APRÈS ;
//   - l'arrêt anticipé reste causal : la validation est la QUEUE (15 %) de
//     chaque symbole, et les parties entraînement/validation sont
//     concaténées séparément.
func (c *colibri) Train(ctx context.Context, req TrainRequest) (*TrainReport, error) {
	if len(req.Datasets) == 0 {
		return nil, fmt.Errorf("aucune donnée à entraîner")
	}
	symbols := make([]string, 0, len(req.Datasets))
	for s := range req.Datasets {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	if !c.spec.pooled && len(symbols) > 1 {
		return nil, fmt.Errorf("%s entraîne UN modèle par actif : %d symboles fournis",
			c.spec.name, len(symbols))
	}

	// Liste ORDONNÉE et figée des catégories : elle est persistée et
	// rechargée avec le modèle. Sans elle, les codes changeraient d'un
	// entraînement à l'autre.
	categories := append([]string(nil), symbols...)

	var trainRows, trainLabels []float64
	var validRows, validLabels []float64
	cols := 0
	total := 0
	for idx, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Progress != nil {
			req.Progress(float64(idx)/float64(len(symbols))*0.7,
				fmt.Sprintf("features %s", sym))
		}
		set := buildSet(sym, req.Datasets[sym], c.spec.pooled, categories)
		if set.n == 0 {
			continue
		}
		cols = set.cols
		total += set.n

		cut := set.n
		if set.n >= minValidSamples*2 {
			v := int(float64(set.n) * validationFraction)
			if v >= minValidSamples {
				cut = set.n - v
			}
		}
		trainRows = append(trainRows, set.rows[:cut*set.cols]...)
		trainLabels = append(trainLabels, set.labels[:cut]...)
		if cut < set.n {
			validRows = append(validRows, set.rows[cut*set.cols:]...)
			validLabels = append(validLabels, set.labels[cut:]...)
		}
	}
	if total < minTrainSamples {
		return nil, fmt.Errorf("jeu trop petit : %d exemples étiquetés (%d minimum) — "+
			"élargir la période ou descendre d'unité de temps", total, minTrainSamples)
	}

	names := c.featureColumns()
	if cols != len(names) {
		return nil, fmt.Errorf("incohérence interne : %d colonnes calculées, %d nommées", cols, len(names))
	}

	trainDS, err := gbdt.NewDataset(trainRows, trainLabels, names)
	if err != nil {
		return nil, err
	}
	var validDS *gbdt.Dataset
	if len(validLabels) >= minValidSamples {
		validDS, err = gbdt.NewDataset(validRows, validLabels, names)
		if err != nil {
			return nil, err
		}
	}

	params := gbdt.DefaultParams()
	params.Seed = req.Seed
	params.Threads = req.Threads
	if c.spec.pooled {
		params.CategoricalFeatures = []int{len(names) - 1}
	}

	model, err := gbdt.Train(ctx, trainDS, validDS, params, func(round int, tl, vl float64) {
		if req.Progress != nil && round%10 == 0 {
			ratio := 0.7 + 0.25*float64(round)/float64(params.NumRounds)
			req.Progress(ratio, fmt.Sprintf("arbre %d/%d", round, params.NumRounds))
		}
	})
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, err
	}
	if err := model.Save(filepath.Join(req.OutputDir, "model.json")); err != nil {
		return nil, err
	}
	meta := modelMeta{
		Strategy:       c.spec.name,
		Version:        c.spec.version,
		Timeframe:      string(req.Timeframe),
		Symbols:        symbols,
		FeatureColumns: names,
		LongThreshold:  c.spec.longThreshold,
		ShortThreshold: c.spec.shortThreshold,
		BarrierATRMult: label.BarrierATRMult,
		MaxHoldDays:    label.MaxHoldDays,
		Seed:           req.Seed,
		TrainedAt:      time.Now().UTC(),
		Samples:        total,
		PositiveRate:   trainDS.PositiveRate(),
	}
	if c.spec.pooled {
		meta.SymbolCategories = categories
	}
	metaRaw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(req.OutputDir, "metadata.json"), metaRaw, 0o644); err != nil {
		return nil, err
	}

	// Le modèle fraîchement entraîné devient le modèle actif : le pli
	// suivant du walk-forward doit être évalué avec LUI, pas avec celui
	// du pli précédent.
	c.mu.Lock()
	c.model, c.meta = model, &meta
	c.prepCache = map[string]*prepared{}
	c.reason = ""
	c.mu.Unlock()

	if req.Progress != nil {
		req.Progress(1, "modèle écrit")
	}
	return &TrainReport{
		ModelDir:     req.OutputDir,
		Samples:      total,
		Features:     len(names),
		PositiveRate: trainDS.PositiveRate(),
		Rounds:       model.BestIteration,
		Metrics:      model.Metrics,
		Importance:   model.FeatureImportance(),
		Symbols:      symbols,
		Seed:         req.Seed,
	}, nil
}

// ScoreOOS mesure l'AUC du modèle sur un bloc OUT-OF-SAMPLE.
//
// C'est la métrique la plus honnête du projet : contrairement à l'AUC
// d'entraînement (qui mesure la mémorisation), elle est calculée sur des
// données jamais vues au fit. L'écart entre les deux EST le
// surapprentissage, mesuré directement.
func (c *colibri) ScoreOOS(symbol string, series core.Series, from int) (float64, bool) {
	if ready, _ := c.Ready(); !ready {
		return 0, false
	}
	p, err := c.prepare(symbol, series)
	if err != nil {
		return 0, false
	}
	lab := label.Default(series)
	if from < 0 {
		from = 0
	}
	scores := make([]float64, 0, len(series)-from)
	truth := make([]float64, 0, len(series)-from)
	for i := from; i < len(series); i++ {
		if math.IsNaN(p.probs[i]) || !lab.Defined(i) {
			continue
		}
		scores = append(scores, p.probs[i])
		truth = append(truth, lab.Value[i])
	}
	if len(scores) < 30 {
		return 0, false
	}
	auc := gbdt.AUC(scores, truth)
	if math.IsNaN(auc) {
		return 0, false
	}
	return auc, true
}
