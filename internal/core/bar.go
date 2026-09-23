// Package core rassemble les types du domaine partagés par tous les
// modules (données de marché, signaux, ordres, comptes rendus) ainsi que
// les briques transverses sans dépendance métier : bus d'événements,
// journalisation.
//
// Règle : les modules ne s'échangent jamais de `map[string]any` anonymes,
// uniquement les types définis ici. C'est le langage commun du système.
package core

import (
	"math"
	"time"
)

// Bar est une bougie OHLCV à deux côtés (bid/ask), unité fondamentale de
// toutes les données historiques de Golden Waterfall.
//
// Le côté BID est le côté de RÉFÉRENCE : indicateurs, features, labels et
// barrières sont calculés dessus. Le côté ASK ne sert qu'à MESURER le
// spread réel (voir Series.MedianSpread) ; il vaut zéro quand la source
// n'a pas fourni les deux côtés, et dans ce cas aucun coût n'est modélisé
// — plutôt qu'un zéro trompeur.
type Bar struct {
	Time     time.Time
	BidOpen  float64
	BidHigh  float64
	BidLow   float64
	BidClose float64
	AskOpen  float64
	AskHigh  float64
	AskLow   float64
	AskClose float64
	Volume   float64
}

// HasAsk indique si la bougie porte un côté ask exploitable.
func (b Bar) HasAsk() bool { return b.AskClose > 0 }

// Open, High, Low, Close exposent le côté de référence (bid).
func (b Bar) Open() float64  { return b.BidOpen }
func (b Bar) High() float64  { return b.BidHigh }
func (b Bar) Low() float64   { return b.BidLow }
func (b Bar) Close() float64 { return b.BidClose }

// Spread du moment (ask - bid au close). NaN si le côté ask est absent.
func (b Bar) Spread() float64 {
	if !b.HasAsk() {
		return math.NaN()
	}
	return b.AskClose - b.BidClose
}

// Series est une suite de bougies TRIÉE par temps croissant, sans doublon.
// Toutes les fonctions du projet supposent cet invariant ; data.Sanitize le
// rétablit sur une série d'origine douteuse.
type Series []Bar

// Closes, Highs, Lows, Opens, Volumes extraient une colonne. Les
// indicateurs travaillent sur ces vecteurs plats plutôt que sur la Series,
// ce qui évite un accès indirect par bougie dans les boucles chaudes.
func (s Series) Closes() []float64  { return s.column(func(b Bar) float64 { return b.BidClose }) }
func (s Series) Highs() []float64   { return s.column(func(b Bar) float64 { return b.BidHigh }) }
func (s Series) Lows() []float64    { return s.column(func(b Bar) float64 { return b.BidLow }) }
func (s Series) Opens() []float64   { return s.column(func(b Bar) float64 { return b.BidOpen }) }
func (s Series) Volumes() []float64 { return s.column(func(b Bar) float64 { return b.Volume }) }

func (s Series) column(pick func(Bar) float64) []float64 {
	out := make([]float64, len(s))
	for i, b := range s {
		out[i] = pick(b)
	}
	return out
}

// Times extrait l'index temporel.
func (s Series) Times() []time.Time {
	out := make([]time.Time, len(s))
	for i, b := range s {
		out[i] = b.Time
	}
	return out
}

// Slice renvoie une sous-série [from, to) en partageant le tableau
// sous-jacent (aucune copie : les séries M1 pèsent plusieurs centaines de
// Mo, on ne les duplique pas pour découper un pli de walk-forward).
func (s Series) Slice(from, to int) Series {
	if from < 0 {
		from = 0
	}
	if to > len(s) {
		to = len(s)
	}
	if from >= to {
		return Series{}
	}
	return s[from:to]
}

// IndexAtOrAfter renvoie l'indice de la première bougie dont le temps est
// >= t (len(s) si aucune). Recherche dichotomique : l'invariant de tri le
// permet.
func (s Series) IndexAtOrAfter(t time.Time) int {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi) / 2
		if s[mid].Time.Before(t) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// HasAskSide indique si la série porte un côté ask exploitable, c'est-à-dire
// si au moins une bougie l'a. Une série sans ask ne permet AUCUNE mesure de
// spread : le backtest le signale au lieu de facturer zéro.
func (s Series) HasAskSide() bool {
	for _, b := range s {
		if b.HasAsk() {
			return true
		}
	}
	return false
}

// Span renvoie la première et la dernière date de la série.
func (s Series) Span() (start, end time.Time) {
	if len(s) == 0 {
		return time.Time{}, time.Time{}
	}
	return s[0].Time, s[len(s)-1].Time
}
