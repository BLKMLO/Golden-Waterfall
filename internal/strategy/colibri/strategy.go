package colibri

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

const (
	// minTrainSamples : en deçà, entraîner produit un modèle qui mémorise
	// son bruit. On REFUSE plutôt que de livrer un modèle décoratif.
	minTrainSamples = 500
	// validationFraction : queue du bloc d'entraînement réservée à
	// l'arrêt anticipé. Causale — cette queue reste strictement dans le
	// passé du bloc out-of-sample.
	validationFraction = 0.15
	minValidSamples    = 100
)

// prepared : features, ATR, coûts et probabilités pré-calculés pour une
// série.
//
// Le calcul vectorisé sur toute la série est EXACTEMENT équivalent à un
// calcul bougie par bougie — c'est la garantie de stabilité par préfixe
// des features qui l'assure — mais des ordres de grandeur plus rapide.
type prepared struct {
	key    seriesKey
	model  *loaded
	matrix *feature.Matrix
	atr    []float64
	cost   []float64   // coût aller-retour estimé (v1_2), nil sinon
	probs  [][]float64 // une tranche par tête
}

type seriesKey struct {
	symbol string
	length int
	first  int64
	last   int64
}

func keyOf(symbol string, s core.Series) seriesKey {
	k := seriesKey{symbol: symbol, length: len(s)}
	if len(s) > 0 {
		k.first = s[0].Time.UnixNano()
		k.last = s[len(s)-1].Time.UnixNano()
	}
	return k
}

type colibri struct {
	rev revision

	mu sync.RWMutex
	// models : modèle chargé PAR SYMBOLE pour une révision mono-actif, un
	// seul (clé "") pour une révision mutualisée.
	//
	// Une seule instance sert toutes les paires en live. Avec un unique
	// emplacement de modèle, chaque chauffe écrasait la précédente : en
	// colibri_v1_0, la dernière paire chargée imposait SON modèle à toutes
	// les autres — des décisions EURUSD prises par le modèle d'USDJPY, sans
	// rien pour le trahir.
	models    map[string]*loaded
	prepCache map[string]*prepared
	reason    string
}

func newColibri(rev revision) *colibri {
	return &colibri{
		rev:       rev,
		models:    map[string]*loaded{},
		prepCache: map[string]*prepared{},
		reason:    "aucun modèle chargé",
	}
}

// slot : clé de rangement du modèle d'un symbole.
func (c *colibri) slot(symbol string) string {
	if c.rev.pooled {
		return ""
	}
	return symbol
}

func (c *colibri) modelFor(symbol string) *loaded {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.models[c.slot(symbol)]
}

func (c *colibri) Describe() strategy.Description {
	cols := c.rev.columns()
	def := map[string]any{
		"features":         cols,
		"nb_features":      len(cols),
		"barriere_atr":     c.rev.barrierATRMult,
		"horizon_jours":    c.rev.maxHoldDays,
		"chauffe_bougies":  warmupBars,
		"contexte_bougies": contextBars,
		"modele":           "GBDT histogramme (Go pur), perte logistique",
		"mutualise":        c.rev.pooled,
	}
	switch c.rev.target {
	case sidedTarget:
		def["cible"] = "issue nette d'exécution, une tête par sens (long, short)"
		def["decision"] = "espérance nette en R ≥ marge"
		def["marge_min_r"] = c.rev.minEdgeR
		def["purge"] = c.rev.purge
		def["calibrage"] = c.rev.calibrate
		def["poids_unicite"] = c.rev.uniqueness
		def["fenetre_spread"] = spreadWindow
	default:
		def["seuil_long"] = c.rev.longThreshold
		def["seuil_short"] = c.rev.shortThreshold
	}
	return strategy.Description{
		Name:        c.rev.name,
		Version:     c.rev.version,
		Summary:     c.rev.summary,
		Definition:  def,
		ContextBars: contextBars,
		MaxHold:     c.rev.maxHold(),
	}
}

// PoolsSymbols indique au walk-forward s'il doit entraîner un modèle
// unique sur tous les actifs.
func (c *colibri) PoolsSymbols() bool { return c.rev.pooled }

func (c *colibri) Ready() (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.models) == 0 {
		return false, c.reason
	}
	return true, ""
}

func (c *colibri) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = map[string]*loaded{}
	c.prepCache = map[string]*prepared{}
	c.reason = "arrêtée"
	return nil
}

// Warmup charge le modèle du symbole puis pré-calcule features, ATR et
// probabilités.
//
// Sans modèle, la stratégie reste MUETTE et le dit (Ready). Improviser des
// signaux à partir de rien serait la pire façon de « rester utile ».
func (c *colibri) Warmup(ctx context.Context, req strategy.WarmupRequest) error {
	if req.ModelDir != "" {
		model, err := c.cachedOrLoad(req.ModelDir)
		c.mu.Lock()
		if err != nil {
			delete(c.models, c.slot(req.Symbol))
			c.reason = err.Error()
			c.mu.Unlock()
			return err
		}
		c.models[c.slot(req.Symbol)] = model
		c.reason = ""
		c.mu.Unlock()
	}
	if c.modelFor(req.Symbol) == nil || len(req.Series) == 0 {
		// Ce n'est pas une erreur : un moteur peut chauffer avant qu'un
		// modèle existe. Il ne décidera simplement rien.
		return nil
	}
	_, err := c.prepare(req.Symbol, req.Series)
	return err
}

// cachedOrLoad évite de relire trente et une fois le même modèle mutualisé
// quand le live chauffe trente et une paires.
func (c *colibri) cachedOrLoad(dir string) (*loaded, error) {
	c.mu.RLock()
	for _, m := range c.models {
		if m.dir == dir {
			c.mu.RUnlock()
			return m, nil
		}
	}
	c.mu.RUnlock()
	return c.loadModel(dir)
}

// prepare calcule (ou retrouve en cache) features, ATR et probabilités.
func (c *colibri) prepare(symbol string, series core.Series) (*prepared, error) {
	key := keyOf(symbol, series)
	model := c.modelFor(symbol)
	c.mu.RLock()
	cached, ok := c.prepCache[symbol]
	c.mu.RUnlock()
	if ok && cached.key == key && cached.model == model {
		return cached, nil
	}
	if model == nil {
		return nil, fmt.Errorf("aucun modèle chargé pour %s", symbol)
	}

	matrix := c.rev.features.compute(series)
	atr := atrOf(series)
	if c.rev.pooled {
		matrix = matrix.AppendColumn(symbolColumnName, symbolCodeColumn(symbol, model.meta.SymbolCategories, len(series)))
	}
	p := &prepared{key: key, model: model, matrix: matrix, atr: atr}
	if c.rev.target == sidedTarget {
		p.cost = spreadCost(series)
	}
	usable := make([]bool, matrix.Rows)
	for i := range usable {
		usable[i] = c.rowUsable(matrix, i)
	}
	for _, head := range model.heads {
		probs, err := head.PredictBatch(matrix.Data, matrix.Cols)
		if err != nil {
			return nil, err
		}
		// Une ligne incomplète (chauffe) ne doit PAS produire de décision :
		// le GBDT sait traiter les manquants, mais une ligne entièrement
		// manquante ne dit rien du marché — elle renverrait le score de
		// base du modèle, qu'un seuil pourrait franchir par accident.
		for i, ok := range usable {
			if !ok {
				probs[i] = math.NaN()
			}
		}
		p.probs = append(p.probs, probs)
	}
	c.mu.Lock()
	c.prepCache[symbol] = p
	c.mu.Unlock()
	return p, nil
}

// rowUsable : toutes les features CAUSALES obligatoires doivent être
// présentes. La colonne `symbol` et les colonnes déclarées optionnelles
// peuvent manquer — c'est une information honnêtement inconnue, pas un
// trou de calcul.
func (c *colibri) rowUsable(m *feature.Matrix, row int) bool {
	r := m.Row(row)
	for i, name := range m.Names {
		if name == symbolColumnName || c.rev.features.optional[name] {
			continue
		}
		if math.IsNaN(r[i]) {
			return false
		}
	}
	return true
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

// OnBar décide à la clôture de la bougie i.
func (c *colibri) OnBar(ctx context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	if i < 0 || i >= len(series) {
		return strategy.NoSignal(symbol), fmt.Errorf("indice de bougie %d hors de la série (%d)", i, len(series))
	}
	if c.modelFor(symbol) == nil {
		return strategy.NoSignal(symbol), nil
	}
	p, err := c.prepare(symbol, series)
	if err != nil {
		return strategy.NoSignal(symbol), err
	}
	atr := p.atr[i]
	if math.IsNaN(atr) || atr <= 0 {
		return strategy.NoSignal(symbol), nil
	}
	bar := series[i]
	sig := core.Signal{
		Strategy: c.rev.name,
		Symbol:   symbol,
		Price:    bar.Close(),
		Time:     bar.Time,
		Action:   core.Hold,
	}
	var action core.SignalAction
	switch c.rev.target {
	case sidedTarget:
		action, sig.Confidence = c.decideExpectedValue(p, i)
	default:
		action, sig.Confidence = c.decideThreshold(p, i)
	}
	if math.IsNaN(sig.Confidence) {
		return strategy.NoSignal(symbol), nil
	}
	// Les barrières sont dimensionnées au MÊME multiple d'ATR que
	// l'étiquetage : le modèle a appris exactement la cible que
	// l'exécution va chercher à atteindre.
	offset := c.rev.barrierATRMult * atr
	switch action {
	case core.EnterLong:
		sig.Action = core.EnterLong
		sig.TakeProfit = bar.Close() + offset
		sig.StopLoss = bar.Close() - offset
	case core.EnterShort:
		sig.Action = core.EnterShort
		sig.TakeProfit = bar.Close() - offset
		sig.StopLoss = bar.Close() + offset
	}
	return sig, nil
}

// decideThreshold : règle historique (v1_0, v1_1). Une probabilité, deux
// seuils.
func (c *colibri) decideThreshold(p *prepared, i int) (core.SignalAction, float64) {
	prob := p.probs[0][i]
	switch {
	case math.IsNaN(prob):
		return core.Hold, math.NaN()
	case prob >= c.rev.longThreshold:
		return core.EnterLong, prob
	case prob <= c.rev.shortThreshold:
		return core.EnterShort, prob
	}
	return core.Hold, prob
}

// decideExpectedValue : règle de colibri_v1_2.
//
// Pour chaque sens s, avec p_s la probabilité CALIBRÉE (rétrécie vers le
// taux de base, sur la validation) d'un trade gagnant NET, W_s
// et L_s les issues brutes moyennes mesurées à l'entraînement quand il
// gagne et quand il perd (en unités de barrière R = k × ATR), et c le coût
// d'un aller-retour rapporté à la barrière :
//
//	E_s = p_s · W_s − (1 − p_s) · L_s − c / (k · ATR_t)
//
// On entre dans le sens de plus grande espérance, si elle atteint la marge
// minEdgeR. Le coût varie à chaque bougie : un seuil de probabilité fixe
// ignorait qu'un spread d'un dixième de barrière exige une probabilité
// plus haute qu'un spread négligeable. La confiance publiée est la
// probabilité du sens retenu (ou du meilleur sens, sans entrée).
func (c *colibri) decideExpectedValue(p *prepared, i int) (core.SignalAction, float64) {
	stats := p.model.meta.HeadStats
	pl := calibrated(p.probs[0][i], stats[headLong])
	ps := calibrated(p.probs[1][i], stats[headShort])
	if math.IsNaN(pl) || math.IsNaN(ps) {
		return core.Hold, math.NaN()
	}
	costR := 0.0
	if cost := p.cost[i]; !math.IsNaN(cost) && cost > 0 {
		costR = cost / (c.rev.barrierATRMult * p.atr[i])
	}
	long := expectedR(pl, stats[headLong]) - costR
	short := expectedR(ps, stats[headShort]) - costR
	minEdge := p.model.meta.MinEdgeR
	switch {
	case long >= short && long >= minEdge:
		return core.EnterLong, pl
	case short > long && short >= minEdge:
		return core.EnterShort, ps
	case long >= short:
		return core.Hold, pl
	}
	return core.Hold, ps
}

// calibrated applique le rétrécissement de la tête à une probabilité brute
// du modèle (repassée en log-odds, l'échelle des arbres).
func calibrated(p float64, s headStats) float64 {
	if math.IsNaN(p) || !s.Calibrated {
		return p
	}
	p = math.Min(math.Max(p, 1e-15), 1-1e-15)
	return s.Calibration.Apply(math.Log(p / (1 - p)))
}

// expectedR : espérance BRUTE d'un trade, en R, pour une probabilité de
// gain p et les issues moyennes mesurées de la tête.
func expectedR(p float64, s headStats) float64 {
	return p*s.WinR - (1-p)*s.LossR
}
