package sterne

import (
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// usdSide : sens à prendre sur la PAIRE pour acheter le dollar. +1 si le
// dollar est la devise de base (USDJPY : acheter la paire), −1 s'il est
// la devise de cotation (EURUSD : la vendre), 0 si la paire ne contient
// pas le dollar — Sterne n'a alors rien à dire.
func usdSide(symbol string) int {
	if len(symbol) != 6 {
		return 0
	}
	switch {
	case symbol[:3] == "USD":
		return +1
	case symbol[3:] == "USD":
		return -1
	}
	return 0
}

// setup : ce que la règle voit à la clôture d'une bougie.
type setup struct {
	side  int // +1 achat de la paire, −1 vente, 0 rien
	entry float64
	stop  float64
	risk  float64
}

// opens : la bougie qui commence à `barTime` finit-elle à l'heure d'entrée
// de la fenêtre, sur l'horloge de SA place ?
func (w window) opens(barTime time.Time, bar time.Duration) bool {
	local := barTime.Add(bar).In(w.loc)
	return local.Hour() == w.hour && local.Minute() == w.minute && local.Second() == 0
}

// setupAt est LA règle : OnBar et le calibrage l'appellent tous deux.
// `win` : les atrWindow bougies qui finissent à la bougie de décision ;
// `pair` : sens de la paire pour un achat de dollar (usdSide) ; `active` :
// fenêtres retenues par le modèle.
//
// Deux fenêtres de sens opposés qui s'ouvriraient sur la même bougie ne
// disent rien : abstention.
func (r revision) setupAt(win core.Series, pair int, active []window) setup {
	if len(win) < r.atrWindow || pair == 0 {
		return setup{}
	}
	bar := win[len(win)-1]
	side := 0
	for _, w := range active {
		if !w.opens(bar.Time, r.barDuration()) {
			continue
		}
		s := pair * w.usd
		if side != 0 && s != side {
			return setup{}
		}
		side = s
	}
	if side == 0 {
		return setup{}
	}
	atr := windowATR(win, r.atrPeriod)
	entry := bar.Close()
	risk := r.stopATR * atr
	// Forme positive : un ATR NaN, nul ou infini donne une abstention.
	if !(risk > 0) || math.IsInf(risk, 0) || !(entry > risk) {
		return setup{}
	}
	return setup{side: side, entry: entry, stop: entry - float64(side)*risk, risk: risk}
}

// windowATR : ATR de Wilder de la dernière bougie, calculé sur la fenêtre
// SEULE — même définition que indicator.ATR (TR initial = H − L, lissage
// α = 1/période).
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
