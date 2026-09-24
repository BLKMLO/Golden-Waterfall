package live

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/storage"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// bufferMargin : bougies conservées AU-DELÀ du contexte que la stratégie
// déclare, pour qu'un trou de flux ou une bougie en retard ne fasse pas
// retomber le tampon sous le minimum à la clôture suivante.
const bufferMargin = 120

// BufferBars : bougies conservées par symbole pour recalculer les features
// causales à chaque clôture — le contexte DÉCLARÉ par la stratégie, plus
// une marge. Le moteur ne suppose rien de la stratégie qu'il fait tourner.
func BufferBars(strat strategy.Strategy) int {
	return strat.Describe().ContextBars + bufferMargin
}

// Engine applique, en temps réel, exactement la même chaîne que le
// backtest.
//
// Il ne trade une paire QUE si les quatre conditions sont réunies :
// broker connecté, kill-switch global actif, paire armée (persistée), et
// aucun ordre en vol sur cette paire. Chacune de ces conditions est
// vérifiable depuis l'interface — un silence s'explique toujours.
type Engine struct {
	gateway  broker.Gateway
	strategy strategy.Strategy
	risk     *risk.Manager
	store    *storage.Store
	bus      *core.Bus
	logger   *slog.Logger
	agg      *Aggregator
	tf       data.Timeframe
	// buffer : taille du tampon par symbole (BufferBars) et maxHold :
	// barrière verticale, tous deux lus UNE fois dans la Description.
	buffer  int
	maxHold time.Duration

	mu       sync.RWMutex
	enabled  bool
	buffers  map[string]core.Series
	inFlight map[string]string   // symbole → identifiant d'ordre en vol
	openLeg  map[string]*openLeg // symbole → entrée ouverte, pour apparier
	lastSig  map[string]core.Signal
	stats    Stats
}

// openLeg : l'entrée d'un aller-retour en cours, telle que le broker l'a
// RAPPORTÉE. Sans compte rendu d'entrée, il n'y a pas d'aller-retour à
// journaliser — et on n'en invente pas.
type openLeg struct {
	side     core.OrderSide
	quantity float64
	price    float64
	time     time.Time
	orderID  string
}

// Stats : compteurs du moteur, affichés par l'interface.
type Stats struct {
	Ticks            int64
	Bars             int64
	Signals          int64
	Orders           int64
	Fills            int64
	RejectedByBroker int64
	// UnprotectedRefused : entrées REFUSÉES par le moteur parce que la
	// passerelle ne sait pas porter les barrières. Compté, jamais tu :
	// une abstention et un refus technique ne veulent pas dire la même
	// chose.
	UnprotectedRefused int64
	LastTick           time.Time
	LastBar            time.Time
}

// NewEngine assemble le moteur. Le câblage réel se fait dans Runtime.
func NewEngine(gw broker.Gateway, strat strategy.Strategy, rm *risk.Manager,
	store *storage.Store, bus *core.Bus, logger *slog.Logger, tf data.Timeframe) *Engine {

	return &Engine{
		gateway:  gw,
		strategy: strat,
		risk:     rm,
		store:    store,
		bus:      bus,
		logger:   logger,
		agg:      NewAggregator(tf),
		tf:       tf,
		buffer:   BufferBars(strat),
		maxHold:  strat.Describe().MaxHold,
		buffers:  map[string]core.Series{},
		inFlight: map[string]string{},
		openLeg:  map[string]*openLeg{},
		lastSig:  map[string]core.Signal{},
	}
}

// SetEnabled actionne le kill-switch GLOBAL.
func (e *Engine) SetEnabled(on bool) {
	e.mu.Lock()
	e.enabled = on
	e.mu.Unlock()
	if on {
		e.logger.Info("kill-switch global ARMÉ : les paires armées peuvent trader")
	} else {
		e.logger.Info("kill-switch global DÉSARMÉ : plus aucune entrée")
	}
}

// Enabled indique l'état du kill-switch global.
func (e *Engine) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.enabled
}

// Stats renvoie une copie des compteurs.
func (e *Engine) Stats() Stats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stats
}

// Seed installe l'historique récent d'un symbole dans le tampon, pour que
// la première bougie live soit décidée avec des indicateurs déjà
// stabilisés — et non après plusieurs jours de chauffe en aveugle.
func (e *Engine) Seed(symbol string, series core.Series) {
	if len(series) > e.buffer {
		series = series.Slice(len(series)-e.buffer, len(series))
	}
	buf := make(core.Series, len(series))
	copy(buf, series)
	e.mu.Lock()
	e.buffers[symbol] = buf
	e.mu.Unlock()
}

// Buffer renvoie une copie du tampon d'un symbole (affichage du graphique).
func (e *Engine) Buffer(symbol string) core.Series {
	e.mu.RLock()
	defer e.mu.RUnlock()
	src := e.buffers[symbol]
	out := make(core.Series, len(src))
	copy(out, src)
	return out
}

// LastSignal renvoie le dernier signal émis pour un symbole.
func (e *Engine) LastSignal(symbol string) (core.Signal, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.lastSig[symbol]
	return s, ok
}

// HandleTick est branché sur le callback de la gateway.
func (e *Engine) HandleTick(ctx context.Context, tick core.Tick) {
	e.mu.Lock()
	e.stats.Ticks++
	e.stats.LastTick = tick.Time
	e.mu.Unlock()
	e.bus.Publish(core.TopicTick, tick)

	closed, ok := e.agg.Add(tick)
	if !ok {
		return
	}
	e.onBarClosed(ctx, tick.Symbol, closed)
}

// onBarClosed est le cœur de la boucle de décision.
func (e *Engine) onBarClosed(ctx context.Context, symbol string, bar core.Bar) {
	e.mu.Lock()
	buf := append(e.buffers[symbol], bar)
	if len(buf) > e.buffer {
		buf = buf[len(buf)-e.buffer:]
	}
	e.buffers[symbol] = buf
	e.stats.Bars++
	e.stats.LastBar = bar.Time
	enabled := e.enabled
	blocked := e.inFlight[symbol]
	leg := e.openLeg[symbol]
	e.mu.Unlock()

	// Barrière VERTICALE : la position a-t-elle dépassé l'horizon que la
	// stratégie déclare ? La règle est celle de `core`, la même qu'au
	// backtest.
	overdue := leg != nil && core.HoldExpired(bar.Time, e.tf.Duration(), core.HoldDeadline(leg.time, e.maxHold))

	if !enabled {
		// Le kill-switch veut dire « ne touche plus à mon compte » : on ne
		// ferme donc rien de force. Mais une position au-delà de son
		// horizon ne doit pas devenir silencieuse pour autant.
		if overdue {
			e.logger.Warn("position au-delà de son horizon et NON fermée : kill-switch désarmé",
				"symbole", symbol, "entree", leg.time)
		}
		return
	}
	if !e.store.Trading(symbol) {
		if overdue {
			e.logger.Warn("position au-delà de son horizon et NON fermée : paire désarmée",
				"symbole", symbol, "entree", leg.time)
		}
		return
	}
	if !e.gateway.Connected() {
		return
	}
	if blocked != "" {
		// Un ordre non tranché bloque son symbole : sans cette
		// réservation, une stratégie qui réaffirme son biais enverrait un
		// deuxième ordre avant que le premier soit rempli.
		e.logger.Debug("bougie ignorée : ordre en vol", "symbole", symbol, "ordre", blocked)
		return
	}

	var sig core.Signal
	if overdue {
		// Une sortie d'horizon ne demande l'avis de personne : elle ne
		// dépend d'aucun modèle, seulement de la date d'entrée rapportée
		// par le courtier. Elle passe AVANT le test de disponibilité de
		// la stratégie — un modèle absent ou périmé ne doit pas laisser
		// une position courir au-delà de ce qui a été appris d'elle.
		sig = core.Signal{
			Strategy: e.strategy.Describe().Name,
			Symbol:   symbol,
			Action:   core.Exit,
			Time:     bar.Time,
		}
		e.logger.Info("horizon atteint : sortie demandée",
			"symbole", symbol, "entree", leg.time, "horizon", e.maxHold)
	} else {
		if ready, reason := e.strategy.Ready(); !ready {
			e.logger.Debug("bougie ignorée : stratégie non prête", "symbole", symbol, "raison", reason)
			return
		}
		var err error
		sig, err = e.strategy.OnBar(ctx, symbol, buf, len(buf)-1)
		if err != nil {
			e.logger.Error("stratégie en échec sur une bougie", "symbole", symbol, "erreur", err)
			return
		}
	}
	e.mu.Lock()
	e.lastSig[symbol] = sig
	if sig.Action != core.Hold {
		e.stats.Signals++
	}
	e.mu.Unlock()
	if sig.Action == core.Hold {
		return
	}
	e.bus.Publish(core.TopicSignal, sig)

	positions, err := e.gateway.Positions(ctx)
	if err != nil {
		// Sans positions RÉELLES, le risque ne peut pas décider. On
		// s'abstient : mieux vaut une occasion manquée qu'un ordre pris
		// sur une image périmée du compte.
		e.logger.Warn("positions indisponibles : aucune décision prise", "symbole", symbol, "erreur", err)
		return
	}
	var account *core.AccountState
	if acc, err := e.gateway.Account(ctx); err == nil {
		acc.DayStartEquity = e.dayStartEquity(acc.Equity)
		account = &acc
	} else {
		// Conséquence directe du dimensionnement au risque : sans équité,
		// il n'y a pas de budget, donc pas d'entrée du tout — et pas
		// seulement une limite journalière en moins.
		e.logger.Warn("équité indisponible : limite de perte journalière non appliquée, "+
			"et AUCUNE entrée si le dimensionnement au risque est actif",
			"symbole", symbol, "erreur", err)
	}

	dec := e.risk.Evaluate(sig, positions, account)
	if !dec.Accepted() {
		e.logger.Debug("signal rejeté par le risque", "symbole", symbol,
			"action", sig.Action, "motif", dec.Reason)
		return
	}

	// Une entrée protégée par des barrières que la passerelle ne portera
	// pas est une entrée NUE. On refuse plutôt que de l'apprendre sur un
	// relevé de courtier.
	if reason, ok := e.unprotected(*dec.Order); !ok {
		e.mu.Lock()
		e.stats.UnprotectedRefused++
		e.mu.Unlock()
		e.logger.Error("ENTRÉE REFUSÉE par le moteur : "+reason,
			"symbole", symbol, "passerelle", e.gateway.Info().Name,
			"stop", dec.Order.StopLoss, "limite", dec.Order.TakeProfit)
		return
	}

	e.mu.Lock()
	if e.inFlight[symbol] != "" {
		e.mu.Unlock()
		return
	}
	e.inFlight[symbol] = "en attente"
	e.mu.Unlock()

	orderID, err := e.gateway.PlaceOrder(ctx, *dec.Order)
	if err != nil {
		e.mu.Lock()
		delete(e.inFlight, symbol)
		e.mu.Unlock()
		e.logger.Error("ordre refusé à la soumission", "symbole", symbol, "erreur", err)
		return
	}
	e.mu.Lock()
	// Si le compte rendu est arrivé AVANT le retour de PlaceOrder (cas
	// courant d'une passerelle synchrone), la réservation a déjà été
	// levée : ne pas la reposer.
	if _, still := e.inFlight[symbol]; still {
		e.inFlight[symbol] = orderID
	}
	e.stats.Orders++
	e.mu.Unlock()
	e.logger.Info("ordre soumis", "symbole", symbol, "sens", dec.Order.Side,
		"quantite", dec.Order.Quantity, "ordre", orderID,
		"stop", dec.Order.StopLoss, "limite", dec.Order.TakeProfit)
}

// unprotected : l'ordre peut-il partir tel quel ?
//
// Seules les ENTRÉES sont concernées : une sortie n'a pas de barrières à
// porter, et rien ne doit jamais empêcher de fermer une position.
func (e *Engine) unprotected(order core.OrderRequest) (string, bool) {
	if order.StopLoss == 0 && order.TakeProfit == 0 {
		return "", true
	}
	if e.gateway.Info().SupportsBracket {
		return "", true
	}
	return "la passerelle ne porte pas le stop et la limite chez le courtier ; " +
		"la position partirait sans protection", false
}

// dayStartEquity lit (ou pose) le repère d'équité du jour UTC.
func (e *Engine) dayStartEquity(current float64) float64 {
	today := time.Now().UTC()
	if v, ok, err := e.store.DayStartEquity(today); err == nil && ok {
		return v
	}
	if err := e.store.SetDayStartEquity(today, current); err != nil {
		e.logger.Warn("repère d'équité du jour non persisté", "erreur", err)
	}
	return current
}

// HandleExecution referme la boucle : la gateway rapporte ce que le broker
// a fait, ce qui libère l'ordre en vol et, sur une sortie, inscrit le
// trade au journal.
func (e *Engine) HandleExecution(rep core.ExecutionReport) {
	e.mu.Lock()
	delete(e.inFlight, rep.Symbol) // les trois statuts sont TERMINAUX
	switch rep.Status {
	case core.Filled:
		e.stats.Fills++
	case core.Rejected:
		e.stats.RejectedByBroker++
	}
	e.mu.Unlock()

	e.bus.Publish(core.TopicExecution, rep)

	switch rep.Status {
	case core.Rejected:
		e.logger.Warn("ordre REFUSÉ par le broker", "symbole", rep.Symbol,
			"ordre", rep.OrderID, "motif", rep.Reason)
		return
	case core.Cancelled:
		e.logger.Info("ordre annulé", "symbole", rep.Symbol, "ordre", rep.OrderID)
		return
	}

	e.mu.Lock()
	leg, hasLeg := e.openLeg[rep.Symbol]
	if rep.Closing && !hasLeg {
		// Sortie d'une position dont ce moteur n'a pas vu l'entrée
		// (ouverte avant le démarrage, ou hors du programme). La prendre
		// pour une entrée fabriquerait, à la sortie suivante, un trade
		// entre deux ordres sans rapport.
		e.mu.Unlock()
		e.logger.Warn("sortie d'une position ouverte hors de cette séance : aucun trade journalisé",
			"symbole", rep.Symbol, "ordre", rep.OrderID, "prix", rep.FillPrice, "pnl", rep.PnL)
		return
	}
	// Deux fills du MÊME sens ne forment pas un aller-retour.
	//
	// L'appariement naïf « le deuxième fill ferme le premier » fabriquerait
	// ici un trade qui n'a jamais eu lieu, avec un P&L calculé entre deux
	// entrées — un chiffre qui aurait l'air d'un résultat. Quand cela se
	// produit, notre vue de la position est fausse (compte rendu de sortie
	// perdu, position ouverte hors du programme) : on le DIT, et on repart
	// du fill le plus récent, qui est ce que le broker affirme.
	sameSide := hasLeg && rep.Side == leg.side
	if !hasLeg || sameSide {
		e.openLeg[rep.Symbol] = &openLeg{
			side: rep.Side, quantity: rep.Quantity, price: rep.FillPrice,
			time: rep.Time, orderID: rep.OrderID,
		}
		e.mu.Unlock()
		if sameSide {
			e.logger.Warn("deuxième entrée du même sens sans sortie intermédiaire — "+
				"aucun trade journalisé, la position suivie est remplacée par celle-ci",
				"symbole", rep.Symbol, "sens", rep.Side,
				"ordre_precedent", leg.orderID, "ordre", rep.OrderID)
			return
		}
		e.logger.Info("entrée exécutée", "symbole", rep.Symbol, "sens", rep.Side,
			"prix", rep.FillPrice, "quantite", rep.Quantity)
		return
	}
	delete(e.openLeg, rep.Symbol)
	e.mu.Unlock()

	trade := core.Trade{
		Symbol:     rep.Symbol,
		Strategy:   e.strategy.Describe().Name,
		Side:       leg.side,
		Quantity:   leg.quantity,
		EntryTime:  leg.time,
		EntryPrice: leg.price,
		ExitTime:   rep.Time,
		ExitPrice:  rep.FillPrice,
		PnL:        rep.PnL,
		ExitReason: rep.Reason,
	}
	if !rep.Realized {
		// Le broker n'a pas fourni de P&L : on NE LE CALCULE PAS à sa
		// place (frais, swap, conversion de devise nous échappent). Le
		// trade est journalisé avec un P&L nul et l'interface le signale.
		trade.PnL = 0
		trade.ExitReason = rep.Reason + " (P&L non rapporté par le broker)"
	}
	if _, err := e.store.AppendTrade(trade); err != nil {
		e.logger.Error("trade non journalisé", "symbole", rep.Symbol, "erreur", err)
		return
	}
	e.logger.Info("sortie exécutée", "symbole", rep.Symbol, "prix", rep.FillPrice,
		"pnl", rep.PnL, "motif", rep.Reason)
}

// InFlight renvoie les symboles dont un ordre n'est pas encore tranché.
func (e *Engine) InFlight() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]string, len(e.inFlight))
	for k, v := range e.inFlight {
		out[k] = v
	}
	return out
}

// Describe résume l'état du moteur en une phrase, pour l'interface.
func (e *Engine) Describe() string {
	if !e.gateway.Connected() {
		return "broker déconnecté"
	}
	if !e.Enabled() {
		return "kill-switch global désarmé"
	}
	if ready, reason := e.strategy.Ready(); !ready {
		return fmt.Sprintf("stratégie non prête : %s", reason)
	}
	armed := e.store.ArmedSymbols()
	if len(armed) == 0 {
		return "aucune paire armée"
	}
	if !e.gateway.Info().SupportsBracket {
		// Dit AVANT la première bougie plutôt qu'après une heure de
		// silence inexpliqué.
		return "passerelle sans barrières chez le courtier : aucune entrée ne partira"
	}
	return fmt.Sprintf("%d paire(s) armée(s)", len(armed))
}
