// Package martinet est la TROISIÈME génération de moteur de décision de
// Golden Waterfall : un scalpeur de ZONES DE LIQUIDITÉ, épuré — des
// plus hauts et des plus bas, un ATR, rien d'autre. Rien en dehors de ce
// paquet ne l'importe, hormis le catalogue `internal/strategies`.
//
// # Principe
//
// Au-dessus d'un plus haut de swing intact dorment les stops des vendeurs
// et les ordres d'achat sur cassure ; sous un plus bas, ceux des
// acheteurs. Quand une bougie perce la zone, sert ces ordres, puis CLÔTURE
// de nouveau en deçà, la cassure a échoué : la liquidité a été prise et
// il n'y a plus personne pour pousser. Martinet prend alors le sens
// inverse, pour un aller court :
//
//	haut > zone haute, close < zone, dépassement ≤ 1 ATR  → vente
//	  stop  = haut de la bougie + 0,1 ATR    (au-delà de la mèche)
//	  cible = close − rr × (stop − close)    (rr calibré : 1 ; 1,5 ; 2)
//	symétrique sous une zone basse                         → achat
//
// Filtres, tous de métier et tous des CONVENTIONS : séance 7 h – 20 h UTC
// (début de bougie), risque ≤ 2 ATR, spread ≤ 0,25 R, barrière verticale
// de deux heures, aucun portage de week-end, filtre d'actualités déclaré.
// Une bougie qui balaie des deux côtés ne dit rien.
//
// Unités de temps : M1, M5 et M15 seulement (Description.Timeframes) —
// un « scalp » en H4 n'en est pas un.
//
// # Entraîner = calibrer
//
// Il n'y a rien à estimer : entraîner, c'est choisir sur l'historique
// d'entraînement de la paire la cible rr et la force des pivots parmi six
// réglages, en rejouant chaque balayage comme le moteur de backtest
// (calibrate.go). Sans modèle, la stratégie est muette, comme les autres.
//
// # Une fenêtre FIXE, pour que le live décide comme le backtest
//
// Chaque décision lit les `window` dernières bougies et elles seules, ATR
// compris : identique en backtest (début de bloc) et en live (tampon
// glissant), et causale par construction.
//
// # Révisions
//
//	martinet_v1_0 : définition ci-dessus (revision.go). Détail :
//	                docs/martinet.md.
package martinet

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

type martinet struct {
	rev revision

	mu sync.RWMutex
	// models : un modèle PAR paire — une instance sert toutes les paires
	// en live.
	models map[string]*modelMeta
	reason string
}

func newMartinet(rev revision) *martinet {
	return &martinet{rev: rev, models: map[string]*modelMeta{}, reason: "aucun modèle chargé"}
}

func (m *martinet) Describe() strategy.Description {
	r := m.rev
	tfs := make([]string, len(r.timeframes))
	for i, tf := range r.timeframes {
		tfs[i] = string(tf)
	}
	def := map[string]any{
		"modele":            "balayage d'une zone de liquidité (plus haut / plus bas de swing intact) rejeté à la clôture",
		"zone":              "pivot de force p (calibrée), âgé d'au plus " + strconv.Itoa(r.lookback) + " bougies, jamais dépassé depuis",
		"fenetre":           r.window,
		"periode_atr":       r.atrPeriod,
		"depassement_max":   fmt.Sprintf("%g ATR", r.maxSweepATR),
		"stop":              fmt.Sprintf("extrême de la mèche ± %g ATR", r.stopBufferATR),
		"risque_max":        fmt.Sprintf("%g ATR", r.maxRiskATR),
		"spread_max":        fmt.Sprintf("%g R", r.maxSpreadR),
		"seance_utc":        fmt.Sprintf("%d h – %d h", r.sessionFrom, r.sessionTo),
		"horizon":           r.maxHold.String(),
		"cible":             "rr × R, rr calibré",
		"grille_rr":         r.rrGrid,
		"grille_pivot":      r.pivotGrid,
		"repli":             fmt.Sprintf("rr %g, pivot %d", r.fallbackRR, r.fallbackPivot),
		"unites_de_temps":   tfs,
		"porte_week_end":    false,
		"sortie_si_oppose":  false,
		"mutualise":         false,
		"indicateurs":       "ATR seul",
		"volume":            "non utilisé",
		"entrainement":      "calibrage in-sample de rr et p (t de Student de la moyenne des trades, n ≥ " + strconv.Itoa(r.minCalibTrades) + ")",
		"bougies_minimales": r.minTrainBars,
	}
	if r.usesNews {
		def["actualites"] = "filtre déclaré (appliqué par les moteurs si news.enabled)"
	}
	return strategy.Description{
		Name:        r.name,
		Version:     r.version,
		Summary:     r.summary,
		Definition:  def,
		ContextBars: r.window,
		MaxHold:     r.maxHold,
		UsesNews:    r.usesNews,
		Timeframes:  append([]data.Timeframe(nil), r.timeframes...),
	}
}

func (m *martinet) Ready() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.models) == 0 {
		return false, m.reason
	}
	return true, ""
}

func (m *martinet) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = map[string]*modelMeta{}
	m.reason = "arrêtée"
	return nil
}

// Warmup charge le modèle de la paire. Il n'y a rien d'autre à préparer :
// chaque décision relit sa propre fenêtre.
func (m *martinet) Warmup(_ context.Context, req strategy.WarmupRequest) error {
	if req.ModelDir == "" {
		return nil
	}
	model, err := m.rev.loadModel(req.ModelDir, req.Symbol, string(req.Timeframe))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		delete(m.models, req.Symbol)
		m.reason = err.Error()
		return err
	}
	m.models[req.Symbol] = model
	m.reason = ""
	return nil
}

func (m *martinet) modelFor(symbol string) *modelMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.models[symbol]
}

// OnBar décide à la clôture de la bougie i, sur les `window` bougies qui
// finissent à i.
func (m *martinet) OnBar(_ context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	if i < 0 || i >= len(series) {
		return strategy.NoSignal(symbol), fmt.Errorf("indice de bougie %d hors de la série (%d)", i, len(series))
	}
	model := m.modelFor(symbol)
	if model == nil || i+1 < m.rev.window {
		return strategy.NoSignal(symbol), nil
	}
	s := m.rev.setupAt(series[i+1-m.rev.window:i+1], model.Pivot)
	if s.side == 0 {
		return strategy.NoSignal(symbol), nil
	}
	bar := series[i]
	sig := core.Signal{
		Strategy:   m.rev.name,
		Symbol:     symbol,
		Action:     core.EnterLong,
		Price:      bar.Close(),
		StopLoss:   s.stop,
		TakeProfit: s.target(model.RR),
		Time:       bar.Time,
		// Confidence : 0 — une règle n'a pas de probabilité à publier.
	}
	if s.side < 0 {
		sig.Action = core.EnterShort
	}
	return sig, nil
}

// Train calibre la cible et la force des pivots sur UNE paire.
func (m *martinet) Train(ctx context.Context, req strategy.TrainRequest) (*strategy.TrainReport, error) {
	r := m.rev
	if len(req.Datasets) != 1 {
		return nil, fmt.Errorf("%s se calibre paire par paire : %d jeux reçus, 1 attendu", r.name, len(req.Datasets))
	}
	if err := strategy.CheckTimeframe(m.Describe(), req.Timeframe); err != nil {
		return nil, err
	}
	var symbol string
	var series core.Series
	for s, d := range req.Datasets {
		symbol, series = s, d
	}
	if len(series) < r.minTrainBars {
		return nil, fmt.Errorf("%s : %d bougies, %s en exige au moins %d pour calibrer",
			symbol, len(series), r.name, r.minTrainBars)
	}
	for _, b := range series {
		if !(b.Close() > 0) || !(b.High() >= b.Low()) {
			return nil, fmt.Errorf("%s : bougie invalide le %s (prix nul, absent ou haut < bas)",
				symbol, b.Time.Format("2006-01-02 15:04"))
		}
	}
	if req.Progress != nil {
		req.Progress(0.05, "calibrage de la cible et des pivots sur l'entraînement")
	}
	rr, pivot, cal, err := r.calibrate(ctx, series, req.Timeframe.Duration())
	if err != nil {
		return nil, err
	}
	first, last := series.Span()
	meta := r.newMeta()
	meta.Timeframe = string(req.Timeframe)
	meta.Symbol = symbol
	meta.RR, meta.Pivot = rr, pivot
	meta.Calibration = cal
	meta.TrainFrom, meta.TrainTo = first, last
	meta.Seed = req.Seed
	meta.TrainedAt = time.Now().UTC()
	if err := meta.save(req.OutputDir); err != nil {
		return nil, err
	}
	if req.Progress != nil {
		req.Progress(1, "calibrage terminé")
	}

	flag := 0.0
	if cal.Fallback {
		flag = 1
	}
	metrics := map[string]float64{
		"calibrage_rr":     rr,
		"calibrage_pivot":  float64(pivot),
		"calibrage_repli":  flag,
		"points_de_grille": float64(len(cal.Grid)),
	}
	for _, gp := range cal.Grid {
		if gp.RR == rr && gp.Pivot == pivot {
			metrics["trades_calibrage"] = float64(gp.Trades)
			if gp.Score != nil {
				metrics["score_calibrage"] = *gp.Score
				metrics["moyenne_r_calibrage"] = gp.MeanR
			}
		}
	}
	return &strategy.TrainReport{
		ModelDir: req.OutputDir,
		Samples:  len(series) - (r.window - 1),
		// Entrées lues : haut, bas, clôture (et le spread, en filtre).
		Features: 3,
		Metrics:  metrics,
		Symbols:  []string{symbol},
		Seed:     req.Seed,
	}, nil
}

// ScoreOOS : l'AUC mesure un classifieur ; Martinet n'en est pas un.
// « Non calculable » plutôt qu'une métrique inventée.
func (m *martinet) ScoreOOS(string, core.Series, int) (float64, bool) { return 0, false }
