// Package sterne est la QUATRIÈME génération de moteur de décision de
// Golden Waterfall : la SAISONNALITÉ INTRAJOURNALIÈRE DU DOLLAR autour
// des fixings de change. Rien en dehors de ce paquet ne l'importe, hormis
// le catalogue `internal/strategies`.
//
// # Principe
//
// Le dollar s'apprécie en moyenne dans les heures qui précèdent un fixing
// (Tokyo 9 h 55, BCE 14 h 15 à Francfort, WM/Reuters 16 h à Londres) et
// se déprécie dans celles qui suivent (Krohn, Mueller et Whelan, « Foreign
// Exchange Fixings and Returns around the Clock », Journal of Finance,
// 2024, vol. 79, n° 1, p. 541-578 ; déjà Breedon et Ranaldo, « Intraday
// Patterns in FX Returns and Order Flow », Journal of Money, Credit and
// Banking, 2013). Sterne prend ce mouvement :
//
//	deux heures AVANT un fixing  → achat du dollar
//	deux heures APRÈS un fixing  → vente du dollar
//	  stop   = entrée ∓ 4 ATR(14)   (protection et unité de risque)
//	  sortie = à l'heure, deux heures après l'entrée
//
// Six fenêtres, fixées a priori par la littérature et non choisies sur les
// données. Aucun indicateur : une horloge, et un ATR pour la distance du
// stop.
//
// # Entraîner = retenir ou écarter chaque fenêtre
//
// L'effet n'a pas la même force sur toutes les paires ni à tous les
// fixings, et son ordre de grandeur (un point de base par trade) est
// celui du spread. Entraîner, c'est rejouer chaque fenêtre SEULE sur
// l'historique d'entraînement de la paire, comme le moteur de backtest,
// et ne garder que celles dont le t de Student net du spread atteint 2
// sur 250 trades au moins (calibrate.go). Un modèle sans fenêtre retenue
// est valide : il ne trade pas, et l'entraînement le dit.
//
// # Ce que le moteur ne peut pas faire
//
// Seules les paires qui contiennent le dollar ont un sens ; les autres
// sont refusées à l'entraînement. M15 seulement : la durée d'une bougie
// entre dans la définition des fenêtres. Aucun portage de week-end.
//
// # Une fenêtre FIXE, pour que le live décide comme le backtest
//
// Chaque décision lit l'horodatage de la bougie et les `atrWindow`
// dernières bougies, elles seules : identique en backtest et en live, et
// causale par construction.
//
// # Révisions
//
//	sterne_v1_0 : définition ci-dessus (revision.go). Détail :
//	              docs/sterne.md.
package sterne

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

// loaded : un modèle prêt à décider.
type loaded struct {
	meta    *modelMeta
	pair    int
	windows []window
}

type sterne struct {
	rev revision

	mu sync.RWMutex
	// models : un modèle PAR paire — une instance sert toutes les paires
	// en live.
	models map[string]*loaded
	reason string
}

func newSterne(rev revision) *sterne {
	return &sterne{rev: rev, models: map[string]*loaded{}, reason: "aucun modèle chargé"}
}

func (s *sterne) Describe() strategy.Description {
	r := s.rev
	tfs := make([]string, len(r.timeframes))
	for i, tf := range r.timeframes {
		tfs[i] = string(tf)
	}
	windows := make([]string, len(r.windows))
	for i, w := range r.windows {
		sens := "achat du dollar"
		if w.usd < 0 {
			sens = "vente du dollar"
		}
		windows[i] = fmt.Sprintf("%s : %02d:%02d (%s), %s", w.name, w.hour, w.minute, w.zone, sens)
	}
	return strategy.Description{
		Name:    r.name,
		Version: r.version,
		Summary: r.summary,
		Definition: map[string]any{
			"modele":            "saisonnalité intrajournalière du dollar autour des fixings (Krohn, Mueller et Whelan, Journal of Finance, 2024)",
			"fenetres":          windows,
			"portage":           r.hold.String(),
			"sortie":            "à l'heure (barrière verticale) ; stop de protection",
			"stop":              fmt.Sprintf("%g ATR(%d), ATR calculé sur %d bougies", r.stopATR, r.atrPeriod, r.atrWindow),
			"cible":             "aucune",
			"paires":            "celles qui contiennent le dollar (USD en base ou en cotation)",
			"unites_de_temps":   tfs,
			"porte_week_end":    false,
			"sortie_si_oppose":  false,
			"mutualise":         false,
			"indicateurs":       "ATR (distance du stop) ; aucun autre",
			"entrainement":      fmt.Sprintf("chaque fenêtre est retenue si le t de Student de ses trades d'entraînement (R nets du spread médian) atteint %g sur au moins %d trades", r.minScore, r.minTrades),
			"bougies_minimales": r.minTrainBars,
			"actualites":        "filtre non déclaré : les fenêtres sont mesurées telles quelles",
		},
		ContextBars: r.atrWindow,
		MaxHold:     r.hold,
		Timeframes:  append([]data.Timeframe(nil), r.timeframes...),
	}
}

func (s *sterne) Ready() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.models) == 0 {
		return false, s.reason
	}
	return true, ""
}

func (s *sterne) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = map[string]*loaded{}
	s.reason = "arrêtée"
	return nil
}

// Warmup charge le modèle de la paire. Il n'y a rien d'autre à préparer :
// chaque décision relit sa propre fenêtre.
func (s *sterne) Warmup(_ context.Context, req strategy.WarmupRequest) error {
	if req.ModelDir == "" {
		return nil
	}
	meta, err := s.rev.loadModel(req.ModelDir, req.Symbol, string(req.Timeframe))
	var model *loaded
	if err == nil {
		model = &loaded{meta: meta, pair: usdSide(req.Symbol)}
		for _, name := range meta.Windows {
			w, _ := s.rev.windowByName(name) // vérifié par loadModel
			model.windows = append(model.windows, w)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		delete(s.models, req.Symbol)
		s.reason = err.Error()
		return err
	}
	s.models[req.Symbol] = model
	s.reason = ""
	return nil
}

func (s *sterne) modelFor(symbol string) *loaded {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.models[symbol]
}

// OnBar décide à la clôture de la bougie i : une fenêtre retenue
// s'ouvre-t-elle à cette heure-là ?
func (s *sterne) OnBar(_ context.Context, symbol string, series core.Series, i int) (core.Signal, error) {
	if i < 0 || i >= len(series) {
		return strategy.NoSignal(symbol), fmt.Errorf("indice de bougie %d hors de la série (%d)", i, len(series))
	}
	model := s.modelFor(symbol)
	if model == nil || len(model.windows) == 0 || i+1 < s.rev.atrWindow {
		return strategy.NoSignal(symbol), nil
	}
	st := s.rev.setupAt(series[i+1-s.rev.atrWindow:i+1], model.pair, model.windows)
	if st.side == 0 {
		return strategy.NoSignal(symbol), nil
	}
	bar := series[i]
	sig := core.Signal{
		Strategy: s.rev.name,
		Symbol:   symbol,
		Action:   core.EnterLong,
		Price:    bar.Close(),
		StopLoss: st.stop,
		// TakeProfit : 0 — aucune cible, la sortie est l'heure.
		Time: bar.Time,
		// Confidence : 0 — une règle n'a pas de probabilité à publier.
	}
	if st.side < 0 {
		sig.Action = core.EnterShort
	}
	return sig, nil
}

// Train retient ou écarte chaque fenêtre sur UNE paire.
func (s *sterne) Train(ctx context.Context, req strategy.TrainRequest) (*strategy.TrainReport, error) {
	r := s.rev
	if len(req.Datasets) != 1 {
		return nil, fmt.Errorf("%s se calibre paire par paire : %d jeux reçus, 1 attendu", r.name, len(req.Datasets))
	}
	if err := strategy.CheckTimeframe(s.Describe(), req.Timeframe); err != nil {
		return nil, err
	}
	var symbol string
	var series core.Series
	for sym, d := range req.Datasets {
		symbol, series = sym, d
	}
	if err := s.AcceptsSymbol(symbol); err != nil {
		return nil, err
	}
	pair := usdSide(symbol)
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
		req.Progress(0.05, "calibrage des fenêtres de fixing sur l'entraînement")
	}
	selected, cal, err := r.calibrate(ctx, series, pair)
	if err != nil {
		return nil, err
	}
	first, last := series.Span()
	meta := r.newMeta()
	meta.Timeframe = string(req.Timeframe)
	meta.Symbol = symbol
	meta.Windows = append(meta.Windows, selected...)
	meta.Calibration = cal
	meta.TrainFrom, meta.TrainTo = first, last
	meta.Seed = req.Seed
	meta.TrainedAt = time.Now().UTC()
	if err := meta.save(req.OutputDir); err != nil {
		return nil, err
	}
	if req.Progress != nil {
		req.Progress(1, "calibrage terminé : "+strconv.Itoa(len(selected))+" fenêtre(s) retenue(s)")
	}

	metrics := map[string]float64{
		"fenetres_retenues": float64(len(selected)),
		"fenetres_essayees": float64(len(cal.Windows)),
	}
	for _, wr := range cal.Windows {
		metrics["trades_"+wr.Window] = float64(wr.Trades)
		if wr.Score != nil {
			metrics["score_"+wr.Window] = *wr.Score
		}
	}
	return &strategy.TrainReport{
		ModelDir: req.OutputDir,
		Samples:  len(series) - (r.atrWindow - 1),
		// Entrées lues : l'heure, et haut, bas, clôture pour l'ATR.
		Features: 4,
		Metrics:  metrics,
		Symbols:  []string{symbol},
		Seed:     req.Seed,
	}, nil
}

// AcceptsSymbol : Sterne ne lit que le dollar (strategy.SymbolFilter).
func (s *sterne) AcceptsSymbol(symbol string) error {
	if usdSide(symbol) == 0 {
		return fmt.Errorf("%s : la paire ne contient pas le dollar — %s ne lit que la saisonnalité du dollar "+
			"(paires en USD : EURUSD, USDJPY…)", symbol, s.rev.name)
	}
	return nil
}

// ScoreOOS : l'AUC mesure un classifieur ; Sterne n'en est pas un.
// « Non calculable » plutôt qu'une métrique inventée.
func (s *sterne) ScoreOOS(string, core.Series, int) (float64, bool) { return 0, false }
