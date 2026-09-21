package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/label"
	"github.com/BLKMLO/Golden-Waterfall/internal/ml/gbdt"
)

// Colibri est le moteur de DÉCISION du projet (le logiciel, lui,
// s'appelle Golden Waterfall — ne jamais confondre les deux).
//
// # Nommage des moteurs
//
// Chaque GÉNÉRATION de moteur porte un nom d'oiseau. Colibri est la
// première ; la suivante, quand elle changera d'approche, portera un autre
// nom d'oiseau plutôt qu'un numéro de plus. Les révisions à l'intérieur
// d'une génération sont numérotées (`colibri_v1_0`, `colibri_v1_1`).
//
// Ce n'est pas de la coquetterie : un nom se retient, se discute et
// s'archive mieux qu'un numéro, et le changement de nom marque clairement
// qu'on ne compare plus des variantes mais deux approches distinctes.
//
// # Principe
//
// Un classifieur binaire apprend P(barrière HAUTE touchée avant la basse)
// sur des bougies étiquetées par triple barrière. Probabilité haute →
// long, probabilité basse → short, entre les deux → abstention. Un seul
// modèle sert donc les deux sens, par symétrie du label.
//
// # Révisions de la génération Colibri
//
// Toute évolution de la DÉFINITION (features, barrières, seuils) crée une
// nouvelle révision, jamais une modification en place — sans quoi un
// modèle archivé ne voudrait plus rien dire.
//
//	colibri_v1_0 : UN modèle PAR actif. Seuils 0,55 / 0,45.
//	colibri_v1_1 : UN modèle MUTUALISÉ sur tous les actifs, avec une
//	               feature catégorielle `symbol`. Seuils 0,60 / 0,40,
//	               plus sélectifs : en v1.0, les probabilités déclenchaient
//	               sur plus de 60 % des bougies, pour un coût de spread
//	               supérieur à l'avantage statistique.
type colibriSpec struct {
	name           string
	version        string
	summary        string
	longThreshold  float64
	shortThreshold float64
	pooled         bool
}

var specV10 = colibriSpec{
	name:           "colibri_v1_0",
	version:        "1.0.0",
	summary:        "Un modèle par actif — seuils 0,55 / 0,45.",
	longThreshold:  0.55,
	shortThreshold: 0.45,
	pooled:         false,
}

var specV11 = colibriSpec{
	name:           "colibri_v1_1",
	version:        "1.1.0",
	summary:        "Modèle unique mutualisé sur tous les actifs (feature `symbol`) — seuils 0,60 / 0,40.",
	longThreshold:  0.60,
	shortThreshold: 0.40,
	pooled:         true,
}

func init() {
	Register(specV10.name, func() Strategy { return newColibri(specV10) })
	Register(specV11.name, func() Strategy { return newColibri(specV11) })
}

// symbolColumnName : nom de la feature catégorielle d'identité de l'actif.
// Constante plutôt que littéral répété : elle doit être IDENTIQUE dans la
// matrice, dans metadata.json et dans la vérification au chargement.
const symbolColumnName = "symbol"

const (
	// minTrainSamples : en deçà, entraîner produit un modèle qui mémorise
	// son bruit. On REFUSE plutôt que de livrer un modèle décoratif.
	minTrainSamples = 500
	// validationFraction : queue du bloc d'entraînement réservée à
	// l'arrêt anticipé. Causale — cette queue reste strictement dans le
	// passé du bloc out-of-sample.
	validationFraction = 0.15
	minValidSamples    = 100
	// liveBufferBars : bougies conservées en live pour recalculer les
	// features causales. Au-dessus de feature.ContextBars avec marge.
	liveBufferBars = 400
)

// prepared : features, ATR et probabilités pré-calculés pour une série.
//
// Le calcul vectorisé sur toute la série est EXACTEMENT équivalent à un
// calcul bougie par bougie — c'est la garantie de stabilité par préfixe
// des features qui l'assure — mais des ordres de grandeur plus rapide.
type prepared struct {
	key    seriesKey
	matrix *feature.Matrix
	atr    []float64
	probs  []float64
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
	spec colibriSpec

	mu        sync.RWMutex
	model     *gbdt.Model
	meta      *modelMeta
	prepCache map[string]*prepared
	reason    string
}

func newColibri(spec colibriSpec) *colibri {
	return &colibri{
		spec:      spec,
		prepCache: map[string]*prepared{},
		reason:    "aucun modèle chargé",
	}
}

// modelMeta accompagne model.json : il fige TOUT ce dont l'inférence a
// besoin pour reproduire exactement les conditions de l'entraînement.
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
}

// featureColumns renvoie l'ordre CANONIQUE des colonnes de cette révision.
// Source unique : entraînement, inférence et vérification au chargement
// l'appellent tous.
func (c *colibri) featureColumns() []string {
	cols := append([]string(nil), feature.Columns...)
	if c.spec.pooled {
		cols = append(cols, symbolColumnName)
	}
	return cols
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

func (c *colibri) Describe() Description {
	cols := c.featureColumns()
	return Description{
		Name:    c.spec.name,
		Version: c.spec.version,
		Summary: c.spec.summary,
		Definition: map[string]any{
			"features":         cols,
			"nb_features":      len(cols),
			"barriere_atr":     label.BarrierATRMult,
			"horizon_jours":    label.MaxHoldDays,
			"seuil_long":       c.spec.longThreshold,
			"seuil_short":      c.spec.shortThreshold,
			"chauffe_bougies":  feature.WarmupBars,
			"contexte_bougies": feature.ContextBars,
			"modele":           "GBDT histogramme (Go pur), perte logistique",
			"mutualise":        c.spec.pooled,
		},
	}
}

// PoolsSymbols indique au walk-forward s'il doit entraîner un modèle
// unique sur tous les actifs.
func (c *colibri) PoolsSymbols() bool { return c.spec.pooled }

func (c *colibri) Ready() (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.model == nil {
		return false, c.reason
	}
	return true, ""
}

func (c *colibri) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.model, c.meta = nil, nil
	c.prepCache = map[string]*prepared{}
	c.reason = "arrêtée"
	return nil
}

// Warmup charge le modèle puis pré-calcule features, ATR et probabilités.
//
// Sans modèle, la stratégie reste MUETTE et le dit (Ready). Improviser des
// signaux à partir de rien serait la pire façon de « rester utile ».
func (c *colibri) Warmup(ctx context.Context, req WarmupRequest) error {
	if req.ModelDir != "" {
		if err := c.loadModel(req.ModelDir); err != nil {
			c.mu.Lock()
			c.model, c.meta = nil, nil
			c.reason = err.Error()
			c.mu.Unlock()
			return err
		}
	}
	ready, reason := c.Ready()
	if !ready {
		// Ce n'est pas une erreur : un moteur peut chauffer avant qu'un
		// modèle existe. Il ne décidera simplement rien.
		_ = reason
		return nil
	}
	if len(req.Series) == 0 {
		return nil
	}
	_, err := c.prepare(req.Symbol, req.Series)
	return err
}

func (c *colibri) loadModel(dir string) error {
	modelPath := filepath.Join(dir, "model.json")
	model, err := gbdt.LoadModel(modelPath)
	if err != nil {
		return err
	}
	metaRaw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return fmt.Errorf("metadata.json manquant dans %s : %w", dir, err)
	}
	var meta modelMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return fmt.Errorf("metadata.json illisible dans %s : %w", dir, err)
	}
	if meta.Strategy != c.spec.name {
		return fmt.Errorf("modèle de la stratégie %q chargé par %q — refusé",
			meta.Strategy, c.spec.name)
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
	expected := c.featureColumns()
	if err := sameColumns(model.FeatureNames, expected); err != nil {
		return fmt.Errorf("modèle %s incompatible avec %s : %w — le réentraîner",
			modelPath, c.spec.name, err)
	}
	if len(meta.FeatureColumns) > 0 {
		if err := sameColumns(meta.FeatureColumns, expected); err != nil {
			return fmt.Errorf("metadata.json de %s incohérent : %w — le réentraîner", dir, err)
		}
	}
	c.mu.Lock()
	c.model, c.meta = model, &meta
	c.prepCache = map[string]*prepared{}
	c.reason = ""
	c.mu.Unlock()
	return nil
}

// prepare calcule (ou retrouve en cache) features, ATR et probabilités.
func (c *colibri) prepare(symbol string, series core.Series) (*prepared, error) {
	key := keyOf(symbol, series)
	c.mu.RLock()
	cached, ok := c.prepCache[symbol]
	model, meta := c.model, c.meta
	c.mu.RUnlock()
	if ok && cached.key == key {
		return cached, nil
	}
	if model == nil {
		return nil, fmt.Errorf("aucun modèle chargé pour %s", symbol)
	}

	matrix := feature.Compute(series)
	atr := feature.ATR(series)
	if c.spec.pooled {
		matrix = matrix.AppendColumn(symbolColumnName, symbolColumn(symbol, meta, len(series)))
	}
	probs, err := model.PredictBatch(matrix.Data, matrix.Cols)
	if err != nil {
		return nil, err
	}
	// Une ligne incomplète (chauffe) ne doit PAS produire de décision :
	// le GBDT sait traiter les manquants, mais une ligne entièrement
	// manquante ne dit rien du marché — elle renverrait le score de base
	// du modèle, qu'un seuil pourrait franchir par accident.
	for i := 0; i < matrix.Rows; i++ {
		if !rowUsable(matrix, i, c.spec.pooled) {
			probs[i] = math.NaN()
		}
	}
	p := &prepared{key: key, matrix: matrix, atr: atr, probs: probs}
	c.mu.Lock()
	c.prepCache[symbol] = p
	c.mu.Unlock()
	return p, nil
}

// rowUsable : toutes les features CAUSALES doivent être présentes. La
// colonne `symbol` peut être manquante (symbole inconnu du modèle) — c'est
// une information honnête, pas un trou de calcul.
func rowUsable(m *feature.Matrix, row int, pooled bool) bool {
	cols := m.Cols
	if pooled {
		cols--
	}
	r := m.Row(row)
	for i := 0; i < cols; i++ {
		if math.IsNaN(r[i]) {
			return false
		}
	}
	return true
}

func symbolColumn(symbol string, meta *modelMeta, rows int) []float64 {
	code := math.NaN()
	if meta != nil {
		for i, s := range meta.SymbolCategories {
			if s == symbol {
				code = float64(i)
				break
			}
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
		return NoSignal(symbol), fmt.Errorf("indice de bougie %d hors de la série (%d)", i, len(series))
	}
	if ready, _ := c.Ready(); !ready {
		return NoSignal(symbol), nil
	}
	p, err := c.prepare(symbol, series)
	if err != nil {
		return NoSignal(symbol), err
	}
	prob := p.probs[i]
	atr := p.atr[i]
	if math.IsNaN(prob) || math.IsNaN(atr) || atr <= 0 {
		return NoSignal(symbol), nil
	}

	bar := series[i]
	sig := core.Signal{
		Strategy:   c.spec.name,
		Symbol:     symbol,
		Confidence: prob,
		Price:      bar.Close(),
		Time:       bar.Time,
		Action:     core.Hold,
	}
	// Les barrières sont dimensionnées au MÊME multiple d'ATR que le
	// labeling : le modèle a appris exactement la cible que l'exécution
	// va chercher à atteindre.
	offset := label.BarrierATRMult * atr
	switch {
	case prob >= c.spec.longThreshold:
		sig.Action = core.EnterLong
		sig.TakeProfit = bar.Close() + offset
		sig.StopLoss = bar.Close() - offset
	case prob <= c.spec.shortThreshold:
		sig.Action = core.EnterShort
		sig.TakeProfit = bar.Close() - offset
		sig.StopLoss = bar.Close() + offset
	}
	return sig, nil
}
