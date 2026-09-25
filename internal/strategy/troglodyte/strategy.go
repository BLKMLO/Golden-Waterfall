// Package troglodyte est la DEUXIÈME génération de moteur de décision de
// Golden Waterfall : un suivi de tendance STRUCTUREL, là où Colibri est un
// classifieur. Rien en dehors de ce paquet ne l'importe, hormis le
// catalogue `internal/strategies`.
//
// # Principe
//
// Le logarithme du prix est décrit par un modèle espace-état à tendance
// locale linéaire (kalman.go). Le filtre de Kalman estime, bougie après
// bougie, la PENTE de cette tendance et son incertitude ; la stratégie
// suit la tendance quand la pente est nettement non nulle, et sort
// quand elle ne l'est plus :
//
//	z_t = β̂_{t|t} / √P_ββ,t|t
//	z_t ≥ +s_in        → entrée longue       stop = close − k × ATR
//	z_t ≤ −s_in        → entrée courte       stop = close + k × ATR
//	|z_t| < s_out      → sortie              (s_out < s_in : hystérésis)
//	sinon              → rien
//
// Pas de limite : un suivi de tendance laisse courir ses gains ; il sort
// sur le retour de la pente vers zéro, sur un signal opposé
// (ExitOnReversal) ou au stop. Il PORTE ses positions pendant le week-end
// (HoldsOverWeekend) : une tendance ne s'arrête pas le vendredi.
//
// Entraîner = estimer les trois variances du modèle par maximum de
// vraisemblance, paire par paire (mle.go). Sans modèle, la stratégie est
// muette, comme Colibri.
//
// # Une fenêtre FIXE, pour que le live décide comme le backtest
//
// La pente d'une tendance presque constante (σ²_ζ petit) a une mémoire
// très longue : filtrée depuis le début de la série, sa valeur à la
// bougie t dépendrait de l'endroit où la série commence. Or le backtest
// part du début de son bloc, et le live d'un tampon glissant. Chaque
// décision refiltre donc les `window` dernières bougies, et seulement
// elles, depuis une initialisation diffuse exacte : la décision est une
// fonction de ces bougies-là, identique partout — et causale par
// construction.
//
// # Révisions
//
// Même règle que Colibri : une définition publiée (fenêtre, seuils, stop)
// ne se modifie jamais ; toute évolution crée une révision.
//
//	troglodyte_v1_0 : fenêtre 500 bougies, s_in 1,5, s_out 0,5, stop
//	                  3 × ATR(14). Ces valeurs sont des CONVENTIONS de
//	                  départ, pas des mesures. Détail : docs/troglodyte.md.
package troglodyte

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// revision : définition FIGÉE d'une révision.
type revision struct {
	name, version, summary string
	// window : bougies refiltrées à chaque décision.
	window int
	// enterZ, exitZ : seuils s_in et s_out sur z.
	enterZ, exitZ float64
	// stopATR, atrPeriod : stop à k × ATR de Wilder.
	stopATR   float64
	atrPeriod int
	// minTrainBars : en deçà, l'estimation des variances n'a pas assez
	// d'innovations pour être autre chose que du bruit. On REFUSE.
	minTrainBars int
}

var revisions = []revision{
	{
		name:    "troglodyte_v1_0",
		version: "1.0",
		summary: "Suivi de tendance structurel : pente d'une tendance locale linéaire " +
			"filtrée par Kalman, variances estimées par maximum de vraisemblance, paire par paire.",
		window:       500,
		enterZ:       1.5,
		exitZ:        0.5,
		stopATR:      3,
		atrPeriod:    14,
		minTrainBars: 1000,
	},
}

func init() {
	for _, r := range revisions {
		strategy.Register(r.name, func() strategy.Strategy { return newTroglodyte(r) })
	}
}

type troglodyte struct {
	rev revision

	mu sync.RWMutex
	// models : un modèle PAR paire — une instance sert toutes les paires
	// en live.
	models map[string]*modelMeta
	reason string
}

func newTroglodyte(rev revision) *troglodyte {
	return &troglodyte{rev: rev, models: map[string]*modelMeta{}, reason: "aucun modèle chargé"}
}

func (t *troglodyte) Describe() strategy.Description {
	r := t.rev
	return strategy.Description{
		Name:    r.name,
		Version: r.version,
		Summary: r.summary,
		Definition: map[string]any{
			"modele":           "tendance locale linéaire (niveau + pente), filtre de Kalman, sur ln(close bid)",
			"estimation":       "maximum de vraisemblance diffuse, concentrée en σ²_ε ; grille puis Nelder-Mead",
			"fenetre":          r.window,
			"seuil_entree_z":   r.enterZ,
			"seuil_sortie_z":   r.exitZ,
			"stop_atr":         r.stopATR,
			"periode_atr":      r.atrPeriod,
			"limite":           "aucune",
			"porte_week_end":   true,
			"sortie_si_oppose": true,
			"mutualise":        false,
		},
		ContextBars:      r.window,
		MaxHold:          0,
		HoldsOverWeekend: true,
		ExitOnReversal:   true,
	}
}

func (t *troglodyte) Ready() (bool, string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.models) == 0 {
		return false, t.reason
	}
	return true, ""
}

func (t *troglodyte) Shutdown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.models = map[string]*modelMeta{}
	t.reason = "arrêtée"
	return nil
}

// Warmup charge le modèle de la paire. Il n'y a rien d'autre à préparer :
// chaque décision refiltre sa propre fenêtre.
func (t *troglodyte) Warmup(ctx context.Context, req strategy.WarmupRequest) error {
	if req.ModelDir == "" {
		return nil
	}
	m, err := t.rev.loadModel(req.ModelDir, req.Symbol, string(req.Timeframe))
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		delete(t.models, req.Symbol)
		t.reason = err.Error()
		return err
	}
	t.models[req.Symbol] = m
	t.reason = ""
	return nil
}

func (t *troglodyte) modelFor(symbol string) *modelMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.models[symbol]
}

// OnBar décide à la clôture de la bougie i, sur les `window` bougies qui
// finissent à i.
func (t *troglodyte) OnBar(ctx context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	if i < 0 || i >= len(series) {
		return strategy.NoSignal(symbol), fmt.Errorf("indice de bougie %d hors de la série (%d)", i, len(series))
	}
	m := t.modelFor(symbol)
	if m == nil || i+1 < t.rev.window {
		return strategy.NoSignal(symbol), nil
	}
	window := series[i+1-t.rev.window : i+1]
	y, ok := logCloses(window)
	if !ok {
		// Un prix nul ou absent ne dit rien de la tendance : on s'abstient.
		return strategy.NoSignal(symbol), nil
	}
	z := filterWindow(y, m.params).slopeZ()
	atr := windowATR(window, t.rev.atrPeriod)
	if math.IsNaN(z) || math.IsNaN(atr) || atr <= 0 {
		return strategy.NoSignal(symbol), nil
	}

	bar := series[i]
	sig := core.Signal{
		Strategy: t.rev.name,
		Symbol:   symbol,
		Price:    bar.Close(),
		Time:     bar.Time,
		Action:   core.Hold,
		// Confiance : probabilité a posteriori, SOUS LE MODÈLE gaussien,
		// que la pente soit du côté le plus probable — P(β > 0) = Φ(z).
		Confidence: math.Max(normalCDF(z), normalCDF(-z)),
	}
	offset := t.rev.stopATR * atr
	switch {
	case z >= t.rev.enterZ:
		sig.Action, sig.StopLoss, sig.Confidence = core.EnterLong, bar.Close()-offset, normalCDF(z)
	case z <= -t.rev.enterZ:
		sig.Action, sig.StopLoss, sig.Confidence = core.EnterShort, bar.Close()+offset, normalCDF(-z)
	case math.Abs(z) < t.rev.exitZ:
		sig.Action = core.Exit
	}
	return sig, nil
}

// Train estime les variances du modèle sur UNE paire.
func (t *troglodyte) Train(ctx context.Context, req strategy.TrainRequest) (*strategy.TrainReport, error) {
	if len(req.Datasets) != 1 {
		return nil, fmt.Errorf("%s s'entraîne paire par paire : %d jeux reçus, 1 attendu",
			t.rev.name, len(req.Datasets))
	}
	var symbol string
	var series core.Series
	for s, d := range req.Datasets {
		symbol, series = s, d
	}
	if len(series) < t.rev.minTrainBars {
		return nil, fmt.Errorf("%s : %d bougies, %s en exige au moins %d pour estimer ses variances",
			symbol, len(series), t.rev.name, t.rev.minTrainBars)
	}
	y, ok := logCloses(series)
	if !ok {
		return nil, fmt.Errorf("%s : prix nul, négatif ou absent dans l'historique — logarithme impossible", symbol)
	}
	progress := func(r float64, step string) {
		if req.Progress != nil {
			req.Progress(r, step)
		}
	}
	progress(0.05, "estimation des variances par maximum de vraisemblance")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := estimate(y)
	if err != nil {
		return nil, fmt.Errorf("%s : %w", symbol, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	first, last := series.Span()
	meta := &modelMeta{
		Strategy:       t.rev.name,
		Version:        t.rev.version,
		Timeframe:      string(req.Timeframe),
		Symbol:         symbol,
		params:         f.Params,
		LogLikelihood:  f.LogL,
		Observations:   f.Obs,
		Iterations:     f.Iterations,
		NegligibleEps:  f.NegligibleEps,
		NegligibleZeta: f.NegligibleZeta,
		AtUpperBound:   f.AtUpperBound,
		Window:         t.rev.window,
		EnterZ:         t.rev.enterZ,
		ExitZ:          t.rev.exitZ,
		StopATR:        t.rev.stopATR,
		ATRPeriod:      t.rev.atrPeriod,
		TrainFrom:      first,
		TrainTo:        last,
		Seed:           req.Seed,
		TrainedAt:      time.Now().UTC(),
	}
	if err := meta.save(req.OutputDir); err != nil {
		return nil, err
	}
	progress(1, "variances estimées")

	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	return &strategy.TrainReport{
		ModelDir: req.OutputDir,
		Samples:  f.Obs,
		// Une seule entrée : le logarithme du close.
		Features: 1,
		Metrics: map[string]float64{
			"log_vraisemblance_par_obs": f.LogL / float64(f.Obs),
			"sigma2_eps":                f.Params.Eps,
			"sigma2_eta":                f.Params.Eta,
			"sigma2_zeta":               f.Params.Zeta,
			"iterations":                float64(f.Iterations),
			"sigma2_eps_negligeable":    flag(f.NegligibleEps),
			"sigma2_zeta_negligeable":   flag(f.NegligibleZeta),
			"borne_haute_atteinte":      flag(f.AtUpperBound),
		},
		Symbols: []string{symbol},
		Seed:    req.Seed,
	}, nil
}

// ScoreOOS : l'AUC mesure un classifieur ; Troglodyte n'en est pas un.
// « Non calculable » plutôt qu'une métrique inventée — l'interface
// affiche « — ».
func (t *troglodyte) ScoreOOS(string, core.Series, int) (float64, bool) { return 0, false }

// logCloses : ln(close bid). false si un prix n'est pas strictement
// positif et fini.
func logCloses(s core.Series) ([]float64, bool) {
	out := make([]float64, len(s))
	for i, b := range s {
		c := b.Close()
		if !(c > 0) || math.IsInf(c, 0) {
			return nil, false
		}
		out[i] = math.Log(c)
	}
	return out, true
}

// windowATR : ATR de Wilder de la dernière bougie, calculé sur la fenêtre
// SEULE — même définition que indicator.ATR (TR initial = H − L, lissage
// α = 1/période), sans allouer cinq tranches à chaque bougie.
func windowATR(s core.Series, period int) float64 {
	if len(s) < period {
		return math.NaN()
	}
	alpha := 1 / float64(period)
	atr := s[0].High() - s[0].Low()
	for i := 1; i < len(s); i++ {
		prev := s[i-1].Close()
		tr := s[i].High() - s[i].Low()
		if v := math.Abs(s[i].High() - prev); v > tr {
			tr = v
		}
		if v := math.Abs(s[i].Low() - prev); v > tr {
			tr = v
		}
		atr = (1-alpha)*atr + alpha*tr
	}
	return atr
}

// normalCDF : fonction de répartition de la loi normale centrée réduite.
func normalCDF(z float64) float64 { return 0.5 * math.Erfc(-z/math.Sqrt2) }
