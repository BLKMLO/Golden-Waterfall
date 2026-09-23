package colibri

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/label"
	"github.com/BLKMLO/Golden-Waterfall/internal/ml/gbdt"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// target : la cible d'une série, tête par tête.
type target struct {
	value [][]float64 // [tête][bougie] : 1, 0 ou NaN
	exit  [][]int     // [tête][bougie] : bougie de fin du trade (v1_2), nil sinon
	gross [][]float64 // [tête][bougie] : issue brute en R (v1_2), nil sinon
	// costsModelled : la série avait un côté ask, le coût est dans la cible.
	costsModelled bool
}

func (t target) defined(i int) bool {
	for _, v := range t.value {
		if math.IsNaN(v[i]) {
			return false
		}
	}
	return true
}

// targetOf calcule la cible de la révision sur une série.
//
// `barDuration` ne sert qu'à la cible alignée sur l'exécution (v1_2) : la
// barrière verticale s'y juge sur la cadence du flux, comme au moteur.
func (c *colibri) targetOf(series core.Series, barDuration time.Duration) target {
	atr := atrOf(series)
	if c.rev.target == symmetricTarget {
		lab := label.TripleBarrier(series, atr, c.rev.barrierATRMult, c.rev.maxHold())
		return target{value: [][]float64{lab.Value}}
	}
	ends := label.ExecutionWindow(series, barDuration, c.rev.maxHold())
	cost := spreadCost(series)
	_, costsModelled := series.MedianSpread()
	lab := label.Sided(series, atr, c.rev.barrierATRMult, ends, cost)
	return target{
		value:         [][]float64{lab.Long.Value, lab.Short.Value},
		exit:          [][]int{lab.Long.Exit, lab.Short.Exit},
		gross:         [][]float64{lab.Long.GrossR, lab.Short.GrossR},
		costsModelled: costsModelled,
	}
}

// symbolSet : jeu étiqueté d'UN symbole, déjà filtré.
type symbolSet struct {
	symbol string
	rows   []float64 // matrice à plat, cols colonnes
	cols   int
	n      int
	bar    []int       // indice de bougie de chaque ligne
	labels [][]float64 // [tête][ligne]
	exit   [][]int     // [tête][ligne] (v1_2)
	gross  [][]float64 // [tête][ligne] (v1_2)
	weight [][]float64 // [tête][ligne] (v1_2, si pondération)
	costs  bool
}

// buildSet calcule features + cible d'un symbole et ne garde que les
// lignes EXPLOITABLES.
//
// Anti-fuite, en deux temps :
//   - les features sont causales (aucune ne regarde après t) ;
//   - le label n'est défini que pour les bougies à fenêtre avant COMPLÈTE.
//
// Le filtre « ligne de features complète ET label défini » retire donc à
// la fois la chauffe (au début) et la queue non étiquetable (à la fin).
// C'est un filtrage EXPLICITE : rien n'est retiré en silence.
func (c *colibri) buildSet(symbol string, series core.Series, categories []string, barDuration time.Duration) symbolSet {
	m := c.rev.features.compute(series)
	tg := c.targetOf(series, barDuration)
	if c.rev.pooled {
		m = m.AppendColumn(symbolColumnName, symbolCodeColumn(symbol, categories, m.Rows))
	}
	heads := len(tg.value)
	set := symbolSet{symbol: symbol, cols: m.Cols, costs: tg.costsModelled}
	set.rows = make([]float64, 0, m.Rows*m.Cols/2)
	set.labels = make([][]float64, heads)
	for h := range set.labels {
		set.labels[h] = make([]float64, 0, m.Rows/2)
	}
	var uniq [][]float64
	if tg.exit != nil {
		set.exit = make([][]int, heads)
		set.gross = make([][]float64, heads)
		if c.rev.uniqueness {
			set.weight = make([][]float64, heads)
			uniq = make([][]float64, heads)
			for h := range uniq {
				// Unicité calculée sur TOUTES les bougies étiquetées de la
				// série, avant filtrage : le chevauchement est une propriété
				// du marché, pas de ce qu'on garde.
				uniq[h] = label.AverageUniqueness(tg.exit[h])
			}
		}
	}
	for i := 0; i < m.Rows; i++ {
		if !tg.defined(i) || !c.rowUsable(m, i) {
			continue
		}
		set.rows = append(set.rows, m.Row(i)...)
		set.bar = append(set.bar, i)
		for h := 0; h < heads; h++ {
			set.labels[h] = append(set.labels[h], tg.value[h][i])
			if tg.exit != nil {
				set.exit[h] = append(set.exit[h], tg.exit[h][i])
				set.gross[h] = append(set.gross[h], tg.gross[h][i])
			}
			if uniq != nil {
				set.weight[h] = append(set.weight[h], uniq[h][i])
			}
		}
		set.n++
	}
	return set
}

// split : lignes gardées pour l'entraînement et pour la validation d'un
// symbole.
//
// La validation est la QUEUE (15 %) du symbole — causale. Avec la purge,
// une ligne d'entraînement dont le trade se termine APRÈS la première
// bougie de validation est retirée : son label a vu des prix que la
// validation juge ensuite, et l'arrêt anticipé s'arrêterait sur une perte
// flattée par ce recouvrement.
func (c *colibri) split(set symbolSet) (train, valid []int, purged int) {
	cut := set.n
	if set.n >= minValidSamples*2 {
		v := int(float64(set.n) * validationFraction)
		if v >= minValidSamples {
			cut = set.n - v
		}
	}
	for r := 0; r < cut; r++ {
		if c.rev.purge && cut < set.n && set.exit != nil {
			first := set.bar[cut]
			overlap := false
			for h := range set.exit {
				if set.exit[h][r] >= first {
					overlap = true
				}
			}
			if overlap {
				purged++
				continue
			}
		}
		train = append(train, r)
	}
	for r := cut; r < set.n; r++ {
		valid = append(valid, r)
	}
	return train, valid, purged
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
func (c *colibri) Train(ctx context.Context, req strategy.TrainRequest) (*strategy.TrainReport, error) {
	if len(req.Datasets) == 0 {
		return nil, fmt.Errorf("aucune donnée à entraîner")
	}
	symbols := make([]string, 0, len(req.Datasets))
	for s := range req.Datasets {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	if !c.rev.pooled && len(symbols) > 1 {
		return nil, fmt.Errorf("%s entraîne UN modèle par actif : %d symboles fournis",
			c.rev.name, len(symbols))
	}

	// Liste ORDONNÉE et figée des catégories : elle est persistée et
	// rechargée avec le modèle. Sans elle, les codes changeraient d'un
	// entraînement à l'autre.
	categories := append([]string(nil), symbols...)
	heads := c.rev.heads()
	barDuration := data.Timeframe(req.Timeframe).Duration()

	var trainRows, validRows []float64
	trainLabels := make([][]float64, len(heads))
	validLabels := make([][]float64, len(heads))
	trainWeights := make([][]float64, len(heads))
	validWeights := make([][]float64, len(heads))
	trainGross := make([][]float64, len(heads))
	cols, total, purged := 0, 0, 0
	costs := false
	for idx, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Progress != nil {
			req.Progress(float64(idx)/float64(len(symbols))*0.6, fmt.Sprintf("features %s", sym))
		}
		set := c.buildSet(sym, req.Datasets[sym], categories, barDuration)
		if set.n == 0 {
			continue
		}
		cols = set.cols
		total += set.n
		costs = costs || set.costs
		tr, va, p := c.split(set)
		purged += p
		for _, r := range tr {
			trainRows = append(trainRows, set.rows[r*set.cols:(r+1)*set.cols]...)
		}
		for _, r := range va {
			validRows = append(validRows, set.rows[r*set.cols:(r+1)*set.cols]...)
		}
		for h := range heads {
			for _, r := range tr {
				trainLabels[h] = append(trainLabels[h], set.labels[h][r])
				if set.gross != nil {
					trainGross[h] = append(trainGross[h], set.gross[h][r])
				}
				if set.weight != nil {
					trainWeights[h] = append(trainWeights[h], set.weight[h][r])
				}
			}
			for _, r := range va {
				validLabels[h] = append(validLabels[h], set.labels[h][r])
				if set.weight != nil {
					validWeights[h] = append(validWeights[h], set.weight[h][r])
				}
			}
		}
	}
	if total < minTrainSamples {
		return nil, fmt.Errorf("jeu trop petit : %d exemples étiquetés (%d minimum) — "+
			"élargir la période ou descendre d'unité de temps", total, minTrainSamples)
	}
	names := c.rev.columns()
	if cols != len(names) {
		return nil, fmt.Errorf("incohérence interne : %d colonnes calculées, %d nommées", cols, len(names))
	}

	meta := &modelMeta{
		Strategy:       c.rev.name,
		Version:        c.rev.version,
		Timeframe:      string(req.Timeframe),
		Symbols:        symbols,
		FeatureColumns: names,
		LongThreshold:  c.rev.longThreshold,
		ShortThreshold: c.rev.shortThreshold,
		BarrierATRMult: c.rev.barrierATRMult,
		MaxHoldDays:    c.rev.maxHoldDays,
		Seed:           req.Seed,
		TrainedAt:      time.Now().UTC(),
		Samples:        total,
	}
	if c.rev.pooled {
		meta.SymbolCategories = categories
	}
	if c.rev.target == sidedTarget {
		meta.Heads = heads
		meta.HeadStats = map[string]headStats{}
		meta.MinEdgeR = c.rev.minEdgeR
		meta.CostsModelled = costs
		meta.Purged = purged
	}

	model := &loaded{dir: req.OutputDir, meta: meta}
	report := &strategy.TrainReport{
		ModelDir: req.OutputDir,
		Samples:  total,
		Features: len(names),
		Metrics:  map[string]float64{},
		Symbols:  symbols,
		Seed:     req.Seed,
	}
	for h, head := range heads {
		trainDS, err := gbdt.NewDataset(trainRows, trainLabels[h], names)
		if err != nil {
			return nil, err
		}
		if c.rev.uniqueness {
			if err := trainDS.SetWeights(trainWeights[h]); err != nil {
				return nil, err
			}
		}
		var validDS *gbdt.Dataset
		if len(validLabels[h]) >= minValidSamples {
			validDS, err = gbdt.NewDataset(validRows, validLabels[h], names)
			if err != nil {
				return nil, err
			}
			if c.rev.uniqueness {
				if err := validDS.SetWeights(validWeights[h]); err != nil {
					return nil, err
				}
			}
		}

		params := gbdt.DefaultParams()
		params.Seed = req.Seed
		params.Threads = req.Threads
		if c.rev.pooled {
			params.CategoricalFeatures = []int{len(names) - 1}
		}
		base := 0.6 + 0.35*float64(h)/float64(len(heads))
		span := 0.35 / float64(len(heads))
		gm, err := gbdt.Train(ctx, trainDS, validDS, params, func(round int, tl, vl float64) {
			if req.Progress != nil && round%10 == 0 {
				step := fmt.Sprintf("arbre %d/%d", round, params.NumRounds)
				if head != "" {
					step = fmt.Sprintf("tête %s, arbre %d/%d", head, round, params.NumRounds)
				}
				req.Progress(base+span*float64(round)/float64(params.NumRounds), step)
			}
		})
		if err != nil {
			return nil, err
		}
		model.heads = append(model.heads, gm)

		// Le rapport garde les clés historiques pour la tête unique ; en
		// deux têtes, chaque métrique est préfixée et la moyenne des deux
		// garde le nom historique, pour que les écrans et `gw runs`
		// continuent de lire quelque chose de comparable.
		for k, v := range gm.Metrics {
			if head == "" {
				report.Metrics[k] = v
				continue
			}
			report.Metrics[head+"_"+k] = v
			report.Metrics[k] += v / float64(len(heads))
		}
		report.Rounds += gm.BestIteration
		if head == "" {
			meta.PositiveRate = trainDS.PositiveRate()
			report.Importance = gm.FeatureImportance()
			continue
		}
		st := measureHead(trainLabels[h], trainGross[h])
		st.PositiveRate = trainDS.PositiveRate()
		st.Calibration = gbdt.NoShrinkage(gm.BaseScore)
		if validDS != nil && c.rev.calibrate {
			scores := make([]float64, validDS.Rows)
			for i := range scores {
				scores[i] = gm.RawScore(validDS.Row(i))
			}
			if cal, ok := gbdt.FitShrinkage(scores, validDS.Y, validDS.W, gm.BaseScore); ok {
				st.Calibration, st.Calibrated = cal, true
				report.Metrics[head+"_calibration_a"] = cal.A
			}
		}
		meta.HeadStats[head] = st
		meta.PositiveRate += st.PositiveRate / float64(len(heads))
		if report.Importance == nil {
			report.Importance = map[string]int{}
		}
		for k, v := range gm.FeatureImportance() {
			report.Importance[k] += v
		}
	}
	report.PositiveRate = meta.PositiveRate

	if err := model.save(c.rev); err != nil {
		return nil, err
	}

	// Le modèle fraîchement entraîné devient le modèle actif : le pli
	// suivant du walk-forward doit être évalué avec LUI, pas avec celui
	// du pli précédent.
	c.mu.Lock()
	for _, sym := range symbols {
		c.models[c.slot(sym)] = model
	}
	c.prepCache = map[string]*prepared{}
	c.reason = ""
	c.mu.Unlock()

	if req.Progress != nil {
		req.Progress(1, "modèle écrit")
	}
	return report, nil
}

// measureHead : issues brutes moyennes, en R, des trades gagnants et
// perdants du jeu d'entraînement. MESURÉES — c'est ce qui évite de
// supposer +1 R / −1 R.
func measureHead(labels, gross []float64) headStats {
	var win, loss float64
	var nWin, nLoss int
	for i, y := range labels {
		if y == 1 {
			win += gross[i]
			nWin++
		} else {
			loss -= gross[i]
			nLoss++
		}
	}
	st := headStats{Samples: len(labels)}
	if nWin > 0 {
		st.WinR = win / float64(nWin)
	}
	if nLoss > 0 {
		st.LossR = loss / float64(nLoss)
	}
	return st
}

// ScoreOOS mesure l'AUC du modèle sur un bloc OUT-OF-SAMPLE.
//
// C'est la métrique la plus honnête du projet : contrairement à l'AUC
// d'entraînement (qui mesure la mémorisation), elle est calculée sur des
// données jamais vues au fit. L'écart entre les deux EST le
// surapprentissage, mesuré directement. En deux têtes, c'est la moyenne
// des AUC des deux têtes, chacune contre sa propre cible.
func (c *colibri) ScoreOOS(symbol string, series core.Series, from int) (float64, bool) {
	if c.modelFor(symbol) == nil {
		return 0, false
	}
	p, err := c.prepare(symbol, series)
	if err != nil {
		return 0, false
	}
	barDuration := data.Timeframe(p.model.meta.Timeframe).Duration()
	tg := c.targetOf(series, barDuration)
	if from < 0 {
		from = 0
	}
	var sum float64
	for h := range tg.value {
		scores := make([]float64, 0, len(series)-from)
		truth := make([]float64, 0, len(series)-from)
		for i := from; i < len(series); i++ {
			if math.IsNaN(p.probs[h][i]) || math.IsNaN(tg.value[h][i]) {
				continue
			}
			scores = append(scores, p.probs[h][i])
			truth = append(truth, tg.value[h][i])
		}
		if len(scores) < 30 {
			return 0, false
		}
		auc := gbdt.AUC(scores, truth)
		if math.IsNaN(auc) {
			return 0, false
		}
		sum += auc
	}
	return sum / float64(len(tg.value)), true
}
