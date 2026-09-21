package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// replayGateway rejoue l'historique local comme s'il s'agissait d'un flux
// temps réel, et exécute les ordres sur un compte SIMULÉ.
//
// Ce n'est pas un courtier : c'est un banc d'essai. Il déclare
// Simulated = true, et l'interface porte un bandeau REJEU en permanence —
// aucun chiffre affiché ici ne doit pouvoir être pris pour un compte réel.
//
// À quoi il sert vraiment : vérifier de bout en bout la chaîne
// flux → agrégation → stratégie → risque → ordre → compte rendu, sans
// dépendre d'un terminal tiers ni risquer un centime.
type replayGateway struct {
	opts Options

	mu        sync.RWMutex
	connected bool
	positions map[string]*simPosition
	cash      float64
	equity    float64
	lastPrice map[string]float64
	// lastTime : horloge du MARCHÉ rejoué, par symbole.
	//
	// Tout ce que cette passerelle rapporte est horodaté avec elle, jamais
	// avec l'heure réelle : un rejeu de 2020 qui daterait ses entrées
	// d'aujourd'hui et ses sorties de 2020 produirait un journal
	// incohérent, et des durées de trade absurdes.
	lastTime map[string]time.Time

	onTick      atomic.Pointer[func(core.Tick)]
	onExecution atomic.Pointer[func(core.ExecutionReport)]

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	orderNo atomic.Int64
}

type simPosition struct {
	side       core.OrderSide
	quantity   float64
	entryPrice float64
	stopLoss   float64
	takeProfit float64
	orderID    string
}

const defaultReplaySpeed = 120.0 // bougies M1 par seconde

func init() {
	Register(Info{
		Name:        "replay",
		Label:       "Rejeu (simulation)",
		Description: "Rejoue l'historique M1 local comme un flux temps réel, sur un compte simulé.",
		Simulated:   true,
		Requirements: "Un historique téléchargé localement. Aucun courtier, aucun terminal, " +
			"aucun argent réel — les chiffres affichés sont ceux d'un compte fictif.",
	}, func(opts Options) (Gateway, error) {
		if opts.HistoryDir == "" {
			return nil, fmt.Errorf("le rejeu a besoin d'un dossier d'historique")
		}
		if opts.InitialCapital <= 0 {
			opts.InitialCapital = 10000
		}
		if opts.Speed <= 0 {
			opts.Speed = defaultReplaySpeed
		}
		if opts.Leverage < 1 {
			opts.Leverage = 1
		}
		return &replayGateway{
			opts:      opts,
			positions: map[string]*simPosition{},
			lastPrice: map[string]float64{},
			lastTime:  map[string]time.Time{},
			cash:      opts.InitialCapital,
			equity:    opts.InitialCapital,
		}, nil
	})
}

func (g *replayGateway) Info() Info {
	for _, i := range List() {
		if i.Name == "replay" {
			return i
		}
	}
	return Info{Name: "replay", Simulated: true}
}

func (g *replayGateway) OnTick(f func(core.Tick))                 { g.onTick.Store(&f) }
func (g *replayGateway) OnExecution(f func(core.ExecutionReport)) { g.onExecution.Store(&f) }

func (g *replayGateway) Connected() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.connected
}

// Connect n'a rien à joindre : le « courtier » est local. On marque quand
// même l'état explicitement, pour que le reste du programme suive le même
// cycle de vie qu'avec un vrai courtier.
func (g *replayGateway) Connect(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.connected = true
	return nil
}

func (g *replayGateway) Disconnect() error {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.connected = false
	g.mu.Unlock()
	g.wg.Wait()
	return nil
}

func (g *replayGateway) Account(ctx context.Context) (core.AccountState, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.connected {
		return core.AccountState{}, fmt.Errorf("passerelle de rejeu non connectée")
	}
	equity := g.cash
	var margin float64
	for sym, p := range g.positions {
		price := g.lastPrice[sym]
		if price > 0 {
			equity += unrealized(p, price)
			margin += p.quantity * p.entryPrice / g.opts.Leverage
		}
	}
	return core.AccountState{Equity: equity, Margin: margin, Currency: "SIM"}, nil
}

func (g *replayGateway) Positions(ctx context.Context) ([]core.Position, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.connected {
		return nil, fmt.Errorf("passerelle de rejeu non connectée")
	}
	out := make([]core.Position, 0, len(g.positions))
	for sym, p := range g.positions {
		qty := p.quantity
		if p.side == core.Sell {
			qty = -qty
		}
		price := g.lastPrice[sym]
		out = append(out, core.Position{
			Symbol:        sym,
			Quantity:      qty,
			AveragePrice:  p.entryPrice,
			UnrealizedPnL: unrealized(p, price),
		})
	}
	return out, nil
}

func unrealized(p *simPosition, price float64) float64 {
	if price <= 0 {
		return 0
	}
	if p.side == core.Buy {
		return (price - p.entryPrice) * p.quantity
	}
	return (p.entryPrice - price) * p.quantity
}

// Subscribe démarre le rejeu des symboles demandés.
func (g *replayGateway) Subscribe(ctx context.Context, symbols []string) error {
	if len(symbols) == 0 {
		return fmt.Errorf("aucun symbole à rejouer")
	}
	g.mu.Lock()
	if !g.connected {
		g.mu.Unlock()
		return fmt.Errorf("passerelle de rejeu non connectée")
	}
	if g.cancel != nil {
		g.cancel()
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	g.cancel = cancel
	g.mu.Unlock()

	for _, sym := range symbols {
		series, err := data.Load(g.opts.HistoryDir, sym, time.Time{}, time.Time{})
		if err != nil {
			// Un symbole sans historique ne doit pas faire tomber les
			// autres : on le DIT et on continue.
			if g.opts.Logger != nil {
				g.opts.Logger.Warn("rejeu impossible, historique absent", "symbole", sym, "erreur", err)
			}
			continue
		}
		g.wg.Add(1)
		go g.replaySymbol(runCtx, sym, series)
	}
	return nil
}

func (g *replayGateway) replaySymbol(ctx context.Context, symbol string, series core.Series) {
	defer g.wg.Done()
	interval := time.Duration(float64(time.Second) / g.opts.Speed)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for _, bar := range series {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ask := bar.AskClose
		if ask <= 0 {
			ask = bar.BidClose
		}
		g.mu.Lock()
		g.lastPrice[symbol] = bar.BidClose
		g.lastTime[symbol] = bar.Time
		g.mu.Unlock()

		g.checkBarriers(symbol, bar)

		if fn := g.onTick.Load(); fn != nil {
			(*fn)(core.Tick{
				Symbol: symbol,
				Bid:    bar.BidClose,
				Ask:    ask,
				Volume: bar.Volume,
				Time:   bar.Time,
			})
		}
	}
}

// checkBarriers ferme une position dont le stop ou la limite est franchi
// par la bougie, et RAPPORTE l'exécution — comme le ferait un courtier
// dont les ordres attachés se déclenchent.
func (g *replayGateway) checkBarriers(symbol string, bar core.Bar) {
	g.mu.Lock()
	p, ok := g.positions[symbol]
	if !ok {
		g.mu.Unlock()
		return
	}
	price, reason := 0.0, ""
	if p.side == core.Buy {
		switch {
		case p.stopLoss > 0 && bar.Low() <= p.stopLoss:
			price, reason = p.stopLoss, "stop"
		case p.takeProfit > 0 && bar.High() >= p.takeProfit:
			price, reason = p.takeProfit, "limite"
		}
	} else {
		switch {
		case p.stopLoss > 0 && bar.High() >= p.stopLoss:
			price, reason = p.stopLoss, "stop"
		case p.takeProfit > 0 && bar.Low() <= p.takeProfit:
			price, reason = p.takeProfit, "limite"
		}
	}
	if reason == "" {
		g.mu.Unlock()
		return
	}
	pnl := unrealized(p, price)
	g.cash += pnl
	delete(g.positions, symbol)
	side := p.side.Opposite()
	qty := p.quantity
	g.mu.Unlock()

	g.report(core.ExecutionReport{
		OrderID:   g.nextOrderID(),
		Symbol:    symbol,
		Side:      side,
		Quantity:  qty,
		Status:    core.Filled,
		FillPrice: price,
		PnL:       pnl,
		Realized:  true,
		Reason:    reason,
		Time:      bar.Time,
	})
}

// PlaceOrder exécute au dernier prix connu et rapporte immédiatement.
func (g *replayGateway) PlaceOrder(ctx context.Context, req core.OrderRequest) (string, error) {
	id := g.nextOrderID()
	g.mu.Lock()
	if !g.connected {
		g.mu.Unlock()
		return "", fmt.Errorf("passerelle de rejeu non connectée")
	}
	price := g.lastPrice[req.Symbol]
	now := g.lastTime[req.Symbol]
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if price <= 0 {
		g.mu.Unlock()
		g.report(core.ExecutionReport{
			OrderID: id, Symbol: req.Symbol, Side: req.Side, Quantity: req.Quantity,
			Status: core.Rejected, Reason: "aucun prix connu pour ce symbole", Time: now,
		})
		return id, fmt.Errorf("aucun prix connu pour %s", req.Symbol)
	}

	existing, hasPosition := g.positions[req.Symbol]
	if hasPosition && existing.side != req.Side {
		// Ordre de sens opposé = fermeture (le moteur ne fait jamais de
		// retournement direct : il ferme, puis décide à nouveau).
		pnl := unrealized(existing, price)
		g.cash += pnl
		qty := existing.quantity
		delete(g.positions, req.Symbol)
		g.mu.Unlock()
		g.report(core.ExecutionReport{
			OrderID: id, Symbol: req.Symbol, Side: req.Side, Quantity: qty,
			Status: core.Filled, FillPrice: price, PnL: pnl, Realized: true,
			Reason: "sortie", Time: now,
		})
		return id, nil
	}
	if hasPosition {
		g.mu.Unlock()
		g.report(core.ExecutionReport{
			OrderID: id, Symbol: req.Symbol, Side: req.Side, Quantity: req.Quantity,
			Status: core.Rejected, Reason: "position déjà ouverte sur ce symbole", Time: now,
		})
		return id, nil
	}

	margin := req.Quantity * price / g.opts.Leverage
	if margin > g.cash {
		g.mu.Unlock()
		g.report(core.ExecutionReport{
			OrderID: id, Symbol: req.Symbol, Side: req.Side, Quantity: req.Quantity,
			Status: core.Rejected, Reason: "marge insuffisante", Time: now,
		})
		return id, nil
	}
	g.positions[req.Symbol] = &simPosition{
		side: req.Side, quantity: req.Quantity, entryPrice: price,
		stopLoss: req.StopLoss, takeProfit: req.TakeProfit, orderID: id,
	}
	g.mu.Unlock()

	g.report(core.ExecutionReport{
		OrderID: id, Symbol: req.Symbol, Side: req.Side, Quantity: req.Quantity,
		Status: core.Filled, FillPrice: price, Reason: "entrée", Time: now,
	})
	return id, nil
}

func (g *replayGateway) report(rep core.ExecutionReport) {
	if fn := g.onExecution.Load(); fn != nil {
		(*fn)(rep)
	}
}

func (g *replayGateway) nextOrderID() string {
	return fmt.Sprintf("REJEU-%06d", g.orderNo.Add(1))
}
