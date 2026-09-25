package news

import (
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

type span struct{ from, to time.Time }

// Calendar : instantané IMMUABLE de l'archive — partageable sans verrou
// entre les plis parallèles du walk-forward et le moteur live.
type Calendar struct {
	weeks     []span  // triées
	events    []Event // triées par date
	lastFetch time.Time
}

// Covers : l'instant t tombe-t-il dans une semaine couverte ?
func (c *Calendar) Covers(t time.Time) bool {
	if c == nil {
		return false
	}
	i := sort.Search(len(c.weeks), func(i int) bool { return c.weeks[i].to.After(t) })
	return i < len(c.weeks) && !t.Before(c.weeks[i].from)
}

// Weeks : nombre de semaines couvertes.
func (c *Calendar) Weeks() int {
	if c == nil {
		return 0
	}
	return len(c.weeks)
}

// Span : première et dernière semaine couvertes (zéro si aucune).
func (c *Calendar) Span() (from, to time.Time) {
	if c == nil || len(c.weeks) == 0 {
		return time.Time{}, time.Time{}
	}
	return c.weeks[0].from, c.weeks[len(c.weeks)-1].to
}

// LastFetch : date de la récupération la plus récente.
func (c *Calendar) LastFetch() time.Time {
	if c == nil {
		return time.Time{}
	}
	return c.lastFetch
}

// Events : nombre d'annonces archivées.
func (c *Calendar) Events() int {
	if c == nil {
		return 0
	}
	return len(c.events)
}

// Verdict du filtre pour une entrée.
type Verdict int

const (
	// Clear : période couverte, aucune annonce dans la fenêtre.
	Clear Verdict = iota
	// Blocked : une annonce assez importante tombe dans la fenêtre.
	Blocked
	// Uncovered : période NON couverte par le calendrier. L'entrée n'est
	// pas filtrée, et c'est compté à part.
	Uncovered
)

// Gate : la règle de filtrage, identique en backtest et en live.
type Gate struct {
	Calendar *Calendar
	// Before / After : l'entrée est refusée si une annonce tombe dans les
	// Before qui SUIVENT la décision, ou est survenue dans les After qui
	// la PRÉCÈDENT.
	Before, After time.Duration
	// MinImpact : impact minimal d'une annonce filtrante.
	MinImpact Impact
}

// Check décide pour une entrée sur `symbol` décidée à l'instant `at`.
// L'annonce renvoyée est la première qui bloque (zéro sinon).
func (g *Gate) Check(symbol string, at time.Time) (Verdict, Event) {
	if g == nil || !g.Calendar.Covers(at) {
		return Uncovered, Event{}
	}
	inst, err := data.LookupInstrument(symbol)
	if err != nil {
		// Symbole inconnu : on ne sait pas quelles devises regarder.
		return Uncovered, Event{}
	}
	from, to := at.Add(-g.After), at.Add(g.Before)
	events := g.Calendar.events
	i := sort.Search(len(events), func(i int) bool { return !events[i].Time.Before(from) })
	for ; i < len(events) && !events[i].Time.After(to); i++ {
		e := events[i]
		if e.Impact >= g.MinImpact && e.Impact != ImpactNone && (e.Currency == inst.Base || e.Currency == inst.Quote) {
			return Blocked, e
		}
	}
	return Clear, Event{}
}
