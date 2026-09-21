package live

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
)

// Quote : dernier prix connu d'un symbole, pour l'affichage.
type Quote struct {
	Symbol   string
	Bid      float64
	Ask      float64
	Time     time.Time
	DayOpen  float64
	Change   float64 // variation depuis l'ouverture du jour, en %
	HasQuote bool
}

// SymbolState agrège tout ce que l'interface montre d'une paire.
type SymbolState struct {
	Quote
	Armed      bool
	InFlight   string
	LastSignal core.SignalAction
	Confidence float64
	HasSignal  bool
	// ModelLoaded dit si un modèle est RÉELLEMENT chargé pour cette paire.
	ModelLoaded bool
	// Notice porte l'anomalie éventuelle (modèle absent, historique
	// périmé…). Les deux sont distincts : une paire peut avoir son modèle
	// et un historique local incomplet, ou l'inverse, et confondre les
	// deux donnerait un diagnostic faux.
	Notice string
}

// Snapshot : photo cohérente de l'état live à un instant donné.
//
// L'interface ne lit QUE ça : elle ne garde aucun état de trading de son
// côté, donc elle ne peut pas afficher une réalité divergente.
type Snapshot struct {
	GatewayName  string
	GatewayLabel string
	Simulated    bool
	Connected    bool
	// SupportsBracket : la passerelle porte-t-elle stop et limite chez le
	// courtier ? À false, le moteur refuse les entrées — et l'entête le
	// dit, sans quoi l'absence d'ordre passerait pour de la prudence du
	// modèle.
	SupportsBracket bool
	Mode            string
	Message         string

	StrategyName  string
	StrategyReady bool
	StrategyWhy   string
	KillSwitch    bool
	EngineStatus  string

	Timeframe  data.Timeframe
	Symbols    []SymbolState
	Positions  []core.Position
	Account    core.AccountState
	HasAccount bool
	AccountErr string
	Stats      Stats
}

// Runtime assemble la passerelle, la stratégie et le moteur, et les fait
// vivre ensemble. C'est le SEUL endroit où ces trois objets se
// rencontrent en mode live.
type Runtime struct {
	cfg    config.Config
	bus    *core.Bus
	logger *slog.Logger
	store  *storage.Store
	risk   *risk.Manager

	mu       sync.RWMutex
	gateway  broker.Gateway
	engine   *Engine
	strategy strategy.Strategy
	symbols  []string
	quotes   map[string]*Quote
	modelOK  map[string]bool
	notices  map[string]string
	message  string
	cancel   context.CancelFunc

	// cache des données de compte, rafraîchi par une boucle dédiée : la
	// TUI redessine plusieurs fois par seconde et ne doit pas interroger
	// le broker à chaque image.
	accMu      sync.RWMutex
	account    core.AccountState
	hasAccount bool
	accountErr string
	positions  []core.Position
}

// NewRuntime construit le runtime (rien n'est connecté avant Connect).
func NewRuntime(cfg config.Config, bus *core.Bus, logger *slog.Logger,
	store *storage.Store, rm *risk.Manager) *Runtime {
	return &Runtime{
		cfg: cfg, bus: bus, logger: logger, store: store, risk: rm,
		quotes:  map[string]*Quote{},
		modelOK: map[string]bool{},
		notices: map[string]string{},
		message: "non connecté",
	}
}

// Connect instancie la passerelle et la stratégie, charge les modèles,
// amorce les tampons, puis ouvre le flux.
//
// L'ordre compte : on chauffe AVANT de connecter le flux, pour qu'aucune
// bougie ne soit décidée par une stratégie encore aveugle.
func (r *Runtime) Connect(ctx context.Context) error {
	r.Disconnect()

	tf, err := data.ParseTimeframe(r.cfg.Broker.Timeframe)
	if err != nil {
		return err
	}
	gw, err := broker.New(r.cfg.Broker.Name, broker.Options{
		Host:           r.cfg.Broker.Host,
		Port:           r.cfg.Broker.Port,
		Mode:           r.cfg.Broker.Mode,
		HistoryDir:     r.cfg.Paths.HistoryDir(),
		InitialCapital: r.cfg.Backtest.InitialCapital,
		Leverage:       r.cfg.Backtest.Leverage,
		Speed:          r.cfg.Broker.ReplaySpeed,
		Logger:         slogAdapter{r.logger},
	})
	if err != nil {
		return err
	}
	strat, err := strategy.New(r.cfg.Strategy.Name)
	if err != nil {
		return err
	}

	symbols := r.cfg.LiveSymbols()
	engine := NewEngine(gw, strat, r.risk, r.store, r.bus, r.logger, tf)
	engine.SetEnabled(r.cfg.Strategy.Enabled)

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	gw.OnTick(func(t core.Tick) {
		r.recordQuote(t)
		engine.HandleTick(runCtx, t)
	})
	gw.OnExecution(engine.HandleExecution)

	// --- Chauffe : modèle + historique récent, symbole par symbole ------
	//
	// Les deux diagnostics sont tenus SÉPARÉMENT : « pas de modèle » et
	// « pas d'historique » n'ont ni la même cause ni le même remède, et
	// les confondre conduit à chercher le problème au mauvais endroit.
	modelOK := map[string]bool{}
	notices := map[string]string{}
	addNotice := func(sym, msg string) {
		if notices[sym] == "" {
			notices[sym] = msg
			return
		}
		notices[sym] += " · " + msg
	}

	for _, sym := range symbols {
		series, histErr := r.warmupSeries(sym, tf)
		switch {
		case histErr != nil:
			// Pas d'historique local : la paire reste SUIVIE (les prix
			// arrivent), mais la stratégie n'aura pas de contexte avant
			// d'avoir accumulé assez de bougies live. C'est dit.
			addNotice(sym, fmt.Sprintf("historique local absent (%v)", histErr))
		default:
			engine.Seed(sym, series)
			if len(series) > 0 {
				if age := time.Since(series[len(series)-1].Time); age > 7*24*time.Hour {
					addNotice(sym, fmt.Sprintf("historique périmé de %d jours — le retélécharger",
						int(age.Hours()/24)))
				}
			}
		}

		dir, why := training.SelectModel(r.cfg.Paths.ModelsDir(), r.cfg.Strategy.Name, sym)
		if dir == "" {
			addNotice(sym, why)
			continue
		}
		if err := strat.Warmup(runCtx, strategy.WarmupRequest{
			Symbol: sym, Series: series, Timeframe: tf, ModelDir: dir,
		}); err != nil {
			addNotice(sym, "modèle non chargé : "+err.Error())
			continue
		}
		modelOK[sym] = true
	}

	if err := gw.Connect(runCtx); err != nil {
		cancel()
		r.setMessage(err.Error())
		return err
	}
	if err := gw.Subscribe(runCtx, symbols); err != nil {
		gw.Disconnect()
		cancel()
		r.setMessage(err.Error())
		return err
	}

	r.mu.Lock()
	r.gateway, r.engine, r.strategy = gw, engine, strat
	r.symbols = symbols
	r.modelOK, r.notices = modelOK, notices
	r.cancel = cancel
	r.message = "connecté"
	r.mu.Unlock()

	go r.pollAccount(runCtx)
	r.bus.Publish(core.TopicBroker, broker.Status{
		Gateway: gw.Info().Name, Connected: true,
		Simulated: gw.Info().Simulated, Mode: r.cfg.Broker.Mode, Message: "connecté",
	})
	r.logger.Info("passerelle connectée", "passerelle", gw.Info().Name,
		"mode", r.cfg.Broker.Mode, "simule", gw.Info().Simulated, "paires", len(symbols))
	return nil
}

// warmupSeries relit l'historique récent d'un symbole à l'unité de temps
// du live.
//
// Deux passes volontaires : on tente d'abord la fenêtre récente (lire
// quinze ans de M1 pour 400 bougies H4 serait du gaspillage pur), puis, si
// elle est vide, on relit la QUEUE de ce qui existe. Le second cas arrive
// dès que l'historique n'a pas été rafraîchi depuis un moment ; mieux vaut
// chauffer sur des bougies anciennes — en le signalant — que démarrer
// complètement aveugle.
func (r *Runtime) warmupSeries(symbol string, tf data.Timeframe) (core.Series, error) {
	span := time.Duration(bufferBars*3) * tf.Duration()
	if span < 30*24*time.Hour {
		span = 30 * 24 * time.Hour
	}
	from := time.Now().UTC().Add(-span)
	if raw, err := data.Load(r.cfg.Paths.HistoryDir(), symbol, from, time.Time{}); err == nil {
		if series := data.Resample(raw, tf); len(series) > 0 {
			return series, nil
		}
	}
	raw, err := data.Load(r.cfg.Paths.HistoryDir(), symbol, time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	series := data.Resample(raw, tf)
	if len(series) > bufferBars {
		series = series.Slice(len(series)-bufferBars, len(series))
	}
	return series, nil
}

// Disconnect arrête tout proprement. Appelable plusieurs fois.
func (r *Runtime) Disconnect() {
	r.mu.Lock()
	gw, cancel := r.gateway, r.cancel
	r.gateway, r.engine, r.cancel = nil, nil, nil
	r.message = "non connecté"
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if gw != nil {
		if err := gw.Disconnect(); err != nil {
			r.logger.Warn("déconnexion imparfaite", "erreur", err)
		}
		r.bus.Publish(core.TopicBroker, broker.Status{
			Gateway: gw.Info().Name, Connected: false, Mode: r.cfg.Broker.Mode,
			Message: "déconnecté",
		})
		r.logger.Info("passerelle déconnectée", "passerelle", gw.Info().Name)
	}
	r.accMu.Lock()
	r.hasAccount, r.positions, r.accountErr = false, nil, ""
	r.accMu.Unlock()
}

// Connected indique l'état réel de la passerelle.
func (r *Runtime) Connected() bool {
	r.mu.RLock()
	gw := r.gateway
	r.mu.RUnlock()
	return gw != nil && gw.Connected()
}

// ToggleKillSwitch bascule le kill-switch global.
func (r *Runtime) ToggleKillSwitch() bool {
	r.mu.RLock()
	engine := r.engine
	r.mu.RUnlock()
	if engine == nil {
		return false
	}
	on := !engine.Enabled()
	engine.SetEnabled(on)
	return on
}

// ToggleSymbol arme ou désarme une paire (persisté).
//
// Refuse d'armer si le broker n'est pas connecté : armer une paire sans
// courtier donnerait l'illusion d'un système prêt à trader.
func (r *Runtime) ToggleSymbol(symbol string) (bool, error) {
	if !r.Connected() && !r.store.Trading(symbol) {
		return false, fmt.Errorf("broker déconnecté : impossible d'armer %s", symbol)
	}
	on := !r.store.Trading(symbol)
	if err := r.store.SetTrading(symbol, on); err != nil {
		return false, err
	}
	r.logger.Info("paire basculée", "symbole", symbol, "armee", on)
	return on, nil
}

func (r *Runtime) setMessage(msg string) {
	r.mu.Lock()
	r.message = msg
	r.mu.Unlock()
}

func (r *Runtime) recordQuote(t core.Tick) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.quotes[t.Symbol]
	if !ok {
		q = &Quote{Symbol: t.Symbol}
		r.quotes[t.Symbol] = q
	}
	day := t.Time.UTC().Truncate(24 * time.Hour)
	if q.DayOpen == 0 || q.Time.UTC().Truncate(24*time.Hour).Before(day) {
		q.DayOpen = t.Bid
	}
	q.Bid, q.Ask, q.Time, q.HasQuote = t.Bid, t.Ask, t.Time, true
	if q.DayOpen > 0 {
		q.Change = (t.Bid/q.DayOpen - 1) * 100
	}
}

// pollAccount rafraîchit compte et positions à cadence fixe.
func (r *Runtime) pollAccount(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.RLock()
		gw := r.gateway
		r.mu.RUnlock()
		if gw == nil || !gw.Connected() {
			continue
		}
		acc, accErr := gw.Account(ctx)
		pos, posErr := gw.Positions(ctx)
		r.accMu.Lock()
		if accErr != nil {
			// On garde la DERNIÈRE valeur connue mais on signale
			// l'erreur : l'interface affichera l'anomalie, pas un chiffre
			// périmé présenté comme frais.
			r.accountErr = accErr.Error()
		} else {
			r.account, r.hasAccount, r.accountErr = acc, true, ""
			r.account.DayStartEquity = r.dayStart(acc.Equity)
		}
		if posErr == nil {
			r.positions = pos
		}
		r.accMu.Unlock()
	}
}

func (r *Runtime) dayStart(current float64) float64 {
	today := time.Now().UTC()
	if v, ok, err := r.store.DayStartEquity(today); err == nil && ok {
		return v
	}
	_ = r.store.SetDayStartEquity(today, current)
	return current
}

// Snapshot produit la photo lue par l'interface.
func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	gw, engine, strat := r.gateway, r.engine, r.strategy
	symbols := append([]string(nil), r.symbols...)
	message := r.message
	quotes := make(map[string]Quote, len(r.quotes))
	for k, v := range r.quotes {
		quotes[k] = *v
	}
	modelOK := make(map[string]bool, len(r.modelOK))
	for k, v := range r.modelOK {
		modelOK[k] = v
	}
	notices := make(map[string]string, len(r.notices))
	for k, v := range r.notices {
		notices[k] = v
	}
	r.mu.RUnlock()

	snap := Snapshot{
		Mode:         r.cfg.Broker.Mode,
		Message:      message,
		StrategyName: r.cfg.Strategy.Name,
		Timeframe:    data.Timeframe(r.cfg.Broker.Timeframe),
	}
	if len(symbols) == 0 {
		symbols = r.cfg.LiveSymbols()
	}
	if gw != nil {
		info := gw.Info()
		snap.GatewayName, snap.GatewayLabel = info.Name, info.Label
		snap.Simulated = info.Simulated
		snap.SupportsBracket = info.SupportsBracket
		snap.Connected = gw.Connected()
	} else {
		snap.GatewayName = r.cfg.Broker.Name
		for _, i := range broker.List() {
			if i.Name == snap.GatewayName {
				snap.GatewayLabel, snap.Simulated = i.Label, i.Simulated
				snap.SupportsBracket = i.SupportsBracket
			}
		}
	}
	if strat != nil {
		d := strat.Describe()
		snap.StrategyName = d.Name
		snap.StrategyReady, snap.StrategyWhy = strat.Ready()
	}
	var inFlight map[string]string
	if engine != nil {
		snap.KillSwitch = engine.Enabled()
		snap.EngineStatus = engine.Describe()
		snap.Stats = engine.Stats()
		inFlight = engine.InFlight()
	} else {
		snap.EngineStatus = "moteur arrêté"
	}

	sort.Strings(symbols)
	for _, sym := range symbols {
		st := SymbolState{
			Quote:       quotes[sym],
			Armed:       r.store.Trading(sym),
			InFlight:    inFlight[sym],
			Notice:      notices[sym],
			ModelLoaded: modelOK[sym],
		}
		st.Symbol = sym
		if engine != nil {
			if sig, ok := engine.LastSignal(sym); ok {
				st.LastSignal, st.Confidence, st.HasSignal = sig.Action, sig.Confidence, true
			}
		}
		snap.Symbols = append(snap.Symbols, st)
	}

	r.accMu.RLock()
	snap.Account, snap.HasAccount, snap.AccountErr = r.account, r.hasAccount, r.accountErr
	snap.Positions = append([]core.Position(nil), r.positions...)
	r.accMu.RUnlock()
	return snap
}

// Buffer expose le tampon de bougies d'un symbole (graphique).
func (r *Runtime) Buffer(symbol string) core.Series {
	r.mu.RLock()
	engine := r.engine
	r.mu.RUnlock()
	if engine == nil {
		return nil
	}
	return engine.Buffer(symbol)
}

// slogAdapter branche slog sur l'interface Logger minimale des gateways.
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }
func (a slogAdapter) Debug(msg string, args ...any) { a.l.Debug(msg, args...) }
