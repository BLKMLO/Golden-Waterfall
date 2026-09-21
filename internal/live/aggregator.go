// Package live tient le trading en temps réel : agrégation des ticks en
// bougies, moteur de décision, et assemblage runtime.
//
// Le flux est identique au backtest, et c'est la garantie principale du
// projet : ticks → bougie CLOSE → Strategy → Signal → risk.Manager →
// OrderRequest → BrokerGateway, puis retour par ExecutionReport → journal.
package live

import (
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// Aggregator transforme un flux de ticks en bougies closes, alignées sur
// l'unité de temps demandée.
//
// Il vit ICI, une seule fois, et non dans chaque stratégie : le contrat de
// stratégie ne parle que de bougies, donc backtest et live entrent par la
// même porte.
//
// Une bougie n'est émise que lorsqu'un tick d'un bucket SUIVANT arrive.
// Conséquence assumée : sur un marché silencieux, la dernière bougie reste
// ouverte tant qu'aucun prix n'arrive. C'est volontaire — clôturer sur une
// minuterie inventerait une bougie que le marché n'a pas produite.
type Aggregator struct {
	tf data.Timeframe

	mu      sync.Mutex
	current map[string]*core.Bar
}

// NewAggregator crée un agrégateur pour une unité de temps.
func NewAggregator(tf data.Timeframe) *Aggregator {
	return &Aggregator{tf: tf, current: map[string]*core.Bar{}}
}

// Timeframe renvoie l'unité de temps agrégée.
func (a *Aggregator) Timeframe() data.Timeframe { return a.tf }

// Add intègre un tick. Renvoie la bougie qui vient d'être CLOSE, s'il y en
// a une.
func (a *Aggregator) Add(tick core.Tick) (core.Bar, bool) {
	bucket := a.tf.Floor(tick.Time)
	a.mu.Lock()
	defer a.mu.Unlock()

	cur, ok := a.current[tick.Symbol]
	if !ok || !cur.Time.Equal(bucket) {
		var closed core.Bar
		hasClosed := false
		if ok {
			closed, hasClosed = *cur, true
		}
		a.current[tick.Symbol] = newBar(bucket, tick)
		return closed, hasClosed
	}

	price := tick.Price()
	if price > cur.BidHigh {
		cur.BidHigh = price
	}
	if price < cur.BidLow {
		cur.BidLow = price
	}
	cur.BidClose = price
	if tick.Ask > 0 {
		if cur.AskClose <= 0 {
			cur.AskOpen, cur.AskHigh, cur.AskLow = tick.Ask, tick.Ask, tick.Ask
		} else {
			if tick.Ask > cur.AskHigh {
				cur.AskHigh = tick.Ask
			}
			if tick.Ask < cur.AskLow {
				cur.AskLow = tick.Ask
			}
		}
		cur.AskClose = tick.Ask
	}
	cur.Volume += tick.Volume
	return core.Bar{}, false
}

func newBar(bucket time.Time, tick core.Tick) *core.Bar {
	price := tick.Price()
	bar := &core.Bar{
		Time:     bucket,
		BidOpen:  price,
		BidHigh:  price,
		BidLow:   price,
		BidClose: price,
		Volume:   tick.Volume,
	}
	if tick.Ask > 0 {
		bar.AskOpen, bar.AskHigh, bar.AskLow, bar.AskClose = tick.Ask, tick.Ask, tick.Ask, tick.Ask
	}
	return bar
}

// Current renvoie la bougie EN COURS d'un symbole (non close). Utile à
// l'affichage, jamais à la décision : décider sur une bougie ouverte,
// c'est décider sur un prix qui peut encore bouger.
func (a *Aggregator) Current(symbol string) (core.Bar, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.current[symbol]
	if !ok {
		return core.Bar{}, false
	}
	return *cur, true
}

// Reset oublie toutes les bougies en cours (reconnexion).
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = map[string]*core.Bar{}
}
