// Package training met en œuvre le walk-forward : la SEULE validation
// honnête d'une stratégie de trading.
//
// Principe (fenêtre expansive, ordre temporel strict, JAMAIS de découpe
// aléatoire) : la seconde moitié de l'historique est découpée en N blocs
// de test consécutifs ; le pli i s'entraîne sur TOUT ce qui précède son
// bloc de test, puis est évalué sur ce bloc, out-of-sample.
//
// La métrique qui compte est l'AGRÉGAT out-of-sample (concaténation des
// blocs de test). Les métriques in-sample ne servent qu'à diagnostiquer le
// surapprentissage : l'écart entre les deux EST la mesure de ce dernier.
//
// Le test de chaque pli passe par le MÊME moteur que le backtest manuel :
// Strategy → Signal → risk.Manager → OrderRequest.
package training

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// Request : un walk-forward complet.
type Request struct {
	Strategy  string
	Symbols   []string
	Timeframe data.Timeframe
	Folds     int
	From      time.Time
	To        time.Time
	Seed      int64
	Workers   int
	// TrainFinal : après les plis, entraîner un modèle sur TOUT
	// l'historique disponible. C'est ce modèle-là qui part en live ; les
	// plis, eux, disent s'il faut lui faire confiance.
	TrainFinal bool
	Progress   func(Progress)
}

// Progress : avancement publié vers l'interface.
type Progress struct {
	Phase    string // "chargement", "pli", "final", "terminé"
	Fold     int
	Folds    int
	Ratio    float64
	Message  string
	Finished bool
	Err      error
}

// Fold : résultat d'un pli.
type Fold struct {
	Index      int                   `json:"index"`
	TrainStart time.Time             `json:"train_start"`
	TrainEnd   time.Time             `json:"train_end"`
	TestStart  time.Time             `json:"test_start"`
	TestEnd    time.Time             `json:"test_end"`
	TrainBars  int                   `json:"train_bars"`
	TestBars   int                   `json:"test_bars"`
	ModelDir   string                `json:"model_dir"`
	Report     *strategy.TrainReport `json:"train_report,omitempty"`
	Stats      backtest.Stats        `json:"test_stats"`
	// OOSAUC : AUC recalculée sur le seul bloc de test. ≈ 0,50 = aucun
	// pouvoir prédictif. HasOOSAUC vaut false quand la métrique n'est pas
	// calculable — l'interface affiche « — », jamais un 0,5 décoratif.
	OOSAUC    float64 `json:"oos_auc"`
	HasOOSAUC bool    `json:"has_oos_auc"`
	Err       string  `json:"error,omitempty"`
}

// Result : sortie complète d'un walk-forward.
type Result struct {
	RunID      string                    `json:"run_id"`
	Strategy   string                    `json:"strategy"`
	Symbols    []string                  `json:"symbols"`
	Timeframe  string                    `json:"timeframe"`
	Seed       int64                     `json:"seed"`
	StartedAt  time.Time                 `json:"started_at"`
	Duration   time.Duration             `json:"duration"`
	Folds      []Fold                    `json:"folds"`
	Aggregate  backtest.Stats            `json:"aggregate"`
	PerSymbol  map[string]backtest.Stats `json:"per_symbol"`
	Equity     []backtest.EquityPoint    `json:"equity"`
	FinalDir   string                    `json:"final_model_dir,omitempty"`
	MeanOOSAUC float64                   `json:"mean_oos_auc"`
	HasMeanAUC bool                      `json:"has_mean_oos_auc"`
}

// Runner exécute les walk-forwards.
type Runner struct {
	cfg  config.Config
	risk *risk.Manager
	news backtest.NewsSource
}

// WithNews branche le filtre d'actualités sur les backtests des plis.
func (r *Runner) WithNews(src backtest.NewsSource) *Runner {
	r.news = src
	return r
}

// NewRunner construit l'exécuteur.
func NewRunner(cfg config.Config, rm *risk.Manager) *Runner {
	return &Runner{cfg: cfg, risk: rm}
}

// Run exécute le walk-forward de bout en bout.
func (r *Runner) Run(ctx context.Context, req Request) (*Result, error) {
	started := time.Now()
	if len(req.Symbols) == 0 {
		return nil, fmt.Errorf("aucun symbole demandé")
	}
	if req.Folds < 2 {
		return nil, fmt.Errorf("un walk-forward demande au moins 2 plis (%d demandés)", req.Folds)
	}
	report := func(p Progress) {
		if req.Progress != nil {
			req.Progress(p)
		}
	}
	// La stratégie est instanciée AVANT de lire quoi que ce soit : un nom
	// inconnu doit échouer tout de suite, et le contexte qu'elle exige
	// décide de ce qui est « assez d'historique ». Le walk-forward ne
	// suppose rien d'elle.
	probe, err := strategy.New(req.Strategy)
	if err != nil {
		return nil, err
	}
	contextBars := probe.Describe().ContextBars
	probe.Shutdown()

	// --- 1. Chargement et ré-échantillonnage -------------------------------
	report(Progress{Phase: "chargement", Message: "lecture de l'historique"})
	series := make(map[string]core.Series, len(req.Symbols))
	for i, sym := range req.Symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := data.Load(r.cfg.Paths.HistoryDir(), sym, req.From, req.To)
		if err != nil {
			return nil, err
		}
		s := data.Resample(raw, req.Timeframe)
		if len(s) < contextBars*4 {
			return nil, fmt.Errorf("%s : %d bougies en %s, trop peu pour un walk-forward "+
				"(descendre d'unité de temps ou élargir la période)", sym, len(s), req.Timeframe)
		}
		series[sym] = s
		report(Progress{
			Phase:   "chargement",
			Ratio:   float64(i+1) / float64(len(req.Symbols)) * 0.1,
			Message: fmt.Sprintf("%s : %d bougies %s", sym, len(s), req.Timeframe),
		})
	}

	symbols := append([]string(nil), req.Symbols...)
	sort.Strings(symbols)

	// --- 2. Découpe temporelle COMMUNE -------------------------------------
	// Pour un modèle mutualisé, tous les actifs doivent être tranchés aux
	// MÊMES dates : sinon un actif entraînerait sur une période que le
	// bloc de test d'un autre actif recouvre — une fuite inter-actifs.
	commonStart, commonEnd := commonRange(series)
	if !commonEnd.After(commonStart) {
		return nil, fmt.Errorf("les actifs demandés n'ont aucune période commune")
	}
	bounds := splitFolds(commonStart, commonEnd, req.Folds)

	runID := started.UTC().Format("20060102-150405")
	runRoot := filepath.Join(r.cfg.Paths.ModelsDir(), req.Strategy, runID)
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return nil, err
	}

	workers := req.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > req.Folds {
		workers = req.Folds
	}
	if workers < 1 {
		workers = 1
	}
	// Les plis tournent en parallèle ; on répartit les cœurs entre eux
	// pour éviter que l'apprentissage de chaque pli n'en réclame la
	// totalité (sur-souscription = plus lent qu'en séquentiel).
	threadsPerFold := runtime.NumCPU() / workers
	if threadsPerFold < 1 {
		threadsPerFold = 1
	}

	// --- 3. Plis -----------------------------------------------------------
	folds := make([]Fold, len(bounds))
	perFoldResults := make([][]*backtest.Result, len(bounds))
	var mu sync.Mutex
	doneFolds := 0

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for k := range bounds {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			fold, results, err := r.runFold(ctx, req, k, bounds[k], commonStart, series,
				symbols, filepath.Join(runRoot, fmt.Sprintf("fold-%d", k+1)), threadsPerFold, contextBars)
			if err != nil {
				fold.Err = err.Error()
			}
			mu.Lock()
			folds[k] = fold
			perFoldResults[k] = results
			doneFolds++
			report(Progress{
				Phase: "pli", Fold: doneFolds, Folds: len(bounds),
				Ratio:   0.1 + 0.8*float64(doneFolds)/float64(len(bounds)),
				Message: foldMessage(fold),
			})
			mu.Unlock()
		}(k)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// --- 4. Agrégats OUT-OF-SAMPLE -----------------------------------------
	var all []*backtest.Result
	perSymbol := map[string][]*backtest.Result{}
	for _, results := range perFoldResults {
		for i, res := range results {
			if res == nil {
				continue
			}
			all = append(all, res)
			perSymbol[symbols[i]] = append(perSymbol[symbols[i]], res)
		}
	}
	result := &Result{
		RunID:     runID,
		Strategy:  req.Strategy,
		Symbols:   symbols,
		Timeframe: string(req.Timeframe),
		Seed:      req.Seed,
		StartedAt: started.UTC(),
		Folds:     folds,
		PerSymbol: map[string]backtest.Stats{},
		Aggregate: backtest.AggregateStats(all, r.cfg.Backtest.InitialCapital),
		Equity:    backtest.MergeEquity(all, r.cfg.Backtest.InitialCapital, backtest.MaxEquityPoints),
	}
	for sym, res := range perSymbol {
		st := backtest.AggregateStats(res, r.cfg.Backtest.InitialCapital)
		st.Symbol = sym
		result.PerSymbol[sym] = st
	}
	var aucSum float64
	var aucCount int
	for _, f := range folds {
		if f.HasOOSAUC {
			aucSum += f.OOSAUC
			aucCount++
		}
	}
	if aucCount > 0 {
		result.MeanOOSAUC = aucSum / float64(aucCount)
		result.HasMeanAUC = true
	}

	// --- 5. Modèle de production -------------------------------------------
	// Entraîné sur TOUT l'historique disponible : c'est celui qui part en
	// live. Les plis ci-dessus disent s'il mérite qu'on l'y envoie.
	if req.TrainFinal {
		report(Progress{Phase: "final", Ratio: 0.92, Message: "modèle de production sur tout l'historique"})
		finalDir := filepath.Join(runRoot, "final")
		if err := r.trainFinal(ctx, req, series, symbols, finalDir); err != nil {
			report(Progress{Phase: "final", Ratio: 0.95,
				Message: fmt.Sprintf("modèle de production non produit : %v", err)})
		} else {
			result.FinalDir = finalDir
		}
	}

	result.Duration = time.Since(started)
	if err := writeRunSummary(runRoot, result); err != nil {
		return result, err
	}
	report(Progress{Phase: "terminé", Ratio: 1, Finished: true,
		Message: fmt.Sprintf("%d trades out-of-sample, %s", result.Aggregate.Trades, result.Duration.Round(time.Second))})
	return result, nil
}

func foldMessage(f Fold) string {
	if f.Err != "" {
		return fmt.Sprintf("pli %d en échec : %s", f.Index, f.Err)
	}
	auc := "—"
	if f.HasOOSAUC {
		auc = fmt.Sprintf("%.3f", f.OOSAUC)
	}
	return fmt.Sprintf("pli %d : %d trades, AUC OOS %s", f.Index, f.Stats.Trades, auc)
}

// foldBounds : bornes d'un bloc de test.
type foldBounds struct {
	testStart time.Time
	testEnd   time.Time
}

// splitFolds découpe la SECONDE MOITIÉ de [start, end] en n blocs de test
// consécutifs. La première moitié reste le socle d'entraînement minimal du
// premier pli : entraîner sur trois bougies pour tester sur mille ne
// mesurerait rien.
func splitFolds(start, end time.Time, n int) []foldBounds {
	mid := start.Add(end.Sub(start) / 2)
	span := end.Sub(mid) / time.Duration(n)
	out := make([]foldBounds, 0, n)
	for k := 0; k < n; k++ {
		ts := mid.Add(time.Duration(k) * span)
		te := ts.Add(span)
		if k == n-1 {
			te = end
		}
		out = append(out, foldBounds{testStart: ts, testEnd: te})
	}
	return out
}

func commonRange(series map[string]core.Series) (start, end time.Time) {
	for _, s := range series {
		if len(s) == 0 {
			continue
		}
		first, last := s.Span()
		if start.IsZero() || first.After(start) {
			start = first
		}
		if end.IsZero() || last.Before(end) {
			end = last
		}
	}
	return start, end
}

// runFold entraîne puis évalue UN pli.
func (r *Runner) runFold(ctx context.Context, req Request, k int, b foldBounds,
	globalStart time.Time, series map[string]core.Series, symbols []string,
	modelDir string, threads, contextBars int) (Fold, []*backtest.Result, error) {

	fold := Fold{
		Index:      k + 1,
		TrainStart: globalStart,
		TrainEnd:   b.testStart,
		TestStart:  b.testStart,
		TestEnd:    b.testEnd,
		ModelDir:   modelDir,
	}

	// Jeux d'entraînement : tout ce qui précède STRICTEMENT le bloc de test.
	trainSets := make(map[string]core.Series, len(symbols))
	for _, sym := range symbols {
		s := series[sym]
		end := s.IndexAtOrAfter(b.testStart)
		if end < contextBars {
			continue
		}
		trainSets[sym] = s.Slice(0, end)
		fold.TrainBars += end
	}
	if len(trainSets) == 0 {
		return fold, nil, fmt.Errorf("aucune donnée d'entraînement avant %s", b.testStart.Format("2006-01-02"))
	}

	strat, err := strategy.New(req.Strategy)
	if err != nil {
		return fold, nil, err
	}
	defer strat.Shutdown()

	pooled := false
	if p, ok := strat.(strategy.Pooled); ok {
		pooled = p.PoolsSymbols()
	}
	trainable, isTrainable := strat.(strategy.Trainable)

	if isTrainable {
		if pooled {
			rep, err := trainable.Train(ctx, strategy.TrainRequest{
				Datasets: trainSets, Timeframe: req.Timeframe,
				OutputDir: modelDir, Seed: req.Seed, Threads: threads,
			})
			if err != nil {
				return fold, nil, err
			}
			fold.Report = rep
		} else {
			// Mono-actif : un modèle par symbole, dans un sous-dossier.
			//
			// Le rapport du pli agrège les symboles : n'en garder qu'un
			// (le dernier entraîné) ferait croire que le pli s'est
			// entraîné sur trois fois moins d'exemples qu'en réalité.
			agg := &strategy.TrainReport{
				ModelDir: modelDir,
				Metrics:  map[string]float64{},
				Seed:     req.Seed,
			}
			for _, sym := range symbols {
				set, ok := trainSets[sym]
				if !ok {
					continue
				}
				rep, err := trainable.Train(ctx, strategy.TrainRequest{
					Datasets:  map[string]core.Series{sym: set},
					Timeframe: req.Timeframe,
					OutputDir: filepath.Join(modelDir, sym),
					Seed:      req.Seed, Threads: threads,
				})
				if err != nil {
					return fold, nil, err
				}
				agg.Samples += rep.Samples
				agg.Features = rep.Features
				agg.Rounds += rep.Rounds
				agg.Symbols = append(agg.Symbols, rep.Symbols...)
				for name, v := range rep.Metrics {
					agg.Metrics[name] += v / float64(len(trainSets))
				}
			}
			fold.Report = agg
		}
	}

	// Évaluation OUT-OF-SAMPLE, actif par actif.
	engine := backtest.NewEngine(r.cfg, r.risk).WithNews(r.news)
	results := make([]*backtest.Result, len(symbols))
	var aucSum float64
	var aucCount int
	for i, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return fold, results, err
		}
		s := series[sym]
		testFrom := s.IndexAtOrAfter(b.testStart)
		testTo := s.IndexAtOrAfter(b.testEnd)
		if testTo <= testFrom {
			continue
		}
		// Contexte de chauffe pris AVANT le bloc de test : il stabilise
		// les indicateurs récursifs sans jamais être évalué ni tradé.
		ctxFrom := testFrom - contextBars
		if ctxFrom < 0 {
			ctxFrom = 0
		}
		block := s.Slice(ctxFrom, testTo)
		offset := testFrom - ctxFrom

		// Chaque actif repart d'une stratégie propre pour un modèle
		// mono-actif ; en mutualisé, la même instance sert tout le monde.
		evalStrat := strat
		modelPath := modelDir
		if isTrainable && !pooled {
			evalStrat, err = strategy.New(req.Strategy)
			if err != nil {
				return fold, results, err
			}
			modelPath = filepath.Join(modelDir, sym)
		}
		if err := evalStrat.Warmup(ctx, strategy.WarmupRequest{
			Symbol: sym, Series: block, Timeframe: req.Timeframe, ModelDir: modelPath,
		}); err != nil {
			return fold, results, err
		}

		res, err := engine.Run(ctx, backtest.Request{
			Symbol: sym, Series: block, From: offset,
			Strategy: evalStrat, Timeframe: req.Timeframe,
		})
		if err != nil {
			return fold, results, err
		}
		results[i] = res
		fold.TestBars += res.Stats.Bars

		if t, ok := evalStrat.(strategy.Trainable); ok {
			if auc, ok := t.ScoreOOS(sym, block, offset); ok {
				// Somme et compteur, puis division à la fin : chaque actif
				// pèse EXACTEMENT autant que les autres.
				//
				// Une moyenne « au fil de l'eau » (moy = (moy+auc)/2) ne
				// donne pas ça du tout : sur trois actifs elle pondère
				// 1/4, 1/4, 1/2, et le dernier actif évalué décide donc de
				// la moitié du chiffre affiché.
				aucSum += auc
				aucCount++
			}
		}
		if evalStrat != strat {
			evalStrat.Shutdown()
		}
	}

	if aucCount > 0 {
		fold.OOSAUC, fold.HasOOSAUC = aucSum/float64(aucCount), true
	}
	fold.Stats = backtest.AggregateStats(results, r.cfg.Backtest.InitialCapital)
	fold.Stats.Symbol = fmt.Sprintf("pli %d", k+1)
	return fold, results, nil
}

// trainFinal entraîne le modèle de production sur tout l'historique.
func (r *Runner) trainFinal(ctx context.Context, req Request, series map[string]core.Series,
	symbols []string, dir string) error {

	strat, err := strategy.New(req.Strategy)
	if err != nil {
		return err
	}
	defer strat.Shutdown()
	trainable, ok := strat.(strategy.Trainable)
	if !ok {
		return fmt.Errorf("la stratégie %s n'est pas entraînable", req.Strategy)
	}
	pooled := false
	if p, ok := strat.(strategy.Pooled); ok {
		pooled = p.PoolsSymbols()
	}
	if pooled {
		_, err := trainable.Train(ctx, strategy.TrainRequest{
			Datasets: series, Timeframe: req.Timeframe, OutputDir: dir, Seed: req.Seed,
		})
		return err
	}
	for _, sym := range symbols {
		if _, err := trainable.Train(ctx, strategy.TrainRequest{
			Datasets:  map[string]core.Series{sym: series[sym]},
			Timeframe: req.Timeframe,
			OutputDir: filepath.Join(dir, sym),
			Seed:      req.Seed,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeRunSummary(dir string, result *Result) error {
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "run.json"), raw, 0o644)
}
