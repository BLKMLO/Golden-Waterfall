package martinet

import (
	"math"
	"sort"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// setup : ce que propose la bougie de décision — un balayage de zone de
// liquidité rejeté à la clôture, ou rien.
type setup struct {
	// side : +1 achat (balayage SOUS un plus bas), −1 vente (balayage
	// AU-DESSUS d'un plus haut), 0 rien.
	side int
	// level : niveau de la zone balayée.
	level float64
	// entry : close de la bougie ; stop : au-delà de la mèche ; risk :
	// |entry − stop|, l'unité R de la cible.
	entry, stop, risk float64
}

// setupAt lit la bougie `cur` d'une fenêtre (la dernière) et dit si elle
// balaie une zone de liquidité puis la rejette.
//
// Zone de liquidité : un plus haut (plus bas) de swing de force p — une
// bougie dont le haut dépasse strictement celui des p bougies qui la
// précèdent et égale au moins celui des p qui la suivent — CONFIRMÉ avant
// la bougie de décision, âgé d'au plus `lookback` bougies, et INTACT :
// aucune bougie ne l'a dépassé depuis. C'est là que dorment les stops des
// vendeurs (des acheteurs) et les ordres d'entrée sur cassure.
//
// Balayage rejeté, côté vente : le haut de la bougie passe au-dessus de
// la zone (les stops sont servis, la liquidité est prise), le close
// revient SOUS la zone (la cassure a échoué), et le dépassement reste
// inférieur à maxSweepATR × ATR (au-delà, c'est un marché qui part, pas
// une chasse aux stops). Plusieurs zones balayées d'un coup : la plus
// haute compte. Une bougie qui balaie des deux côtés ne dit rien : on
// s'abstient.
//
// Tout est lu dans la fenêtre seule : la décision ne dépend d'aucune
// bougie antérieure à la fenêtre, ni d'aucune bougie future.
//
// Filtre de volume (volMin > 0) : le volume de la bougie de décision doit
// atteindre volMin fois la MÉDIANE des volWindow bougies qui la précèdent.
// Un volume inconnu (NaN : source ou passerelle qui n'en publie pas) ou
// une référence nulle ne permettent pas de trancher : on s'abstient,
// jamais on ne suppose.
func (r revision) setupAt(w core.Series, pivot int, volMin float64) setup {
	cur := len(w) - 1
	if cur < 1 || pivot < 1 {
		return setup{}
	}
	bar := w[cur]
	h := bar.Time.UTC().Hour()
	if h < r.sessionFrom || h >= r.sessionTo {
		return setup{}
	}
	atr := windowATR(w, r.atrPeriod)
	if !(atr > 0) {
		return setup{}
	}
	high, low, close := bar.High(), bar.Low(), bar.Close()

	// Zones intactes, de la plus récente à la plus ancienne. runHigh et
	// runLow : extrêmes des bougies STRICTEMENT entre la zone candidate et
	// la bougie de décision.
	sell, buy := math.NaN(), math.NaN()
	runHigh, runLow := math.Inf(-1), math.Inf(1)
	oldest := cur - r.lookback
	if oldest < pivot {
		oldest = pivot
	}
	for j := cur - 1; j >= oldest; j-- {
		if j+pivot <= cur-1 {
			if lv := w[j].High(); lv >= runHigh && swingHigh(w, j, pivot) &&
				high > lv && close < lv && high-lv <= r.maxSweepATR*atr && !(lv <= sell) {
				sell = lv
			}
			if lv := w[j].Low(); lv <= runLow && swingLow(w, j, pivot) &&
				low < lv && close > lv && lv-low <= r.maxSweepATR*atr && !(lv >= buy) {
				buy = lv
			}
		}
		runHigh = math.Max(runHigh, w[j].High())
		runLow = math.Min(runLow, w[j].Low())
	}

	var s setup
	switch {
	case !math.IsNaN(sell) && !math.IsNaN(buy):
		return setup{}
	case !math.IsNaN(sell):
		s = setup{side: -1, level: sell, entry: close, stop: high + r.stopBufferATR*atr}
	case !math.IsNaN(buy):
		s = setup{side: +1, level: buy, entry: close, stop: low - r.stopBufferATR*atr}
	default:
		return setup{}
	}
	s.risk = math.Abs(s.entry - s.stop)
	if !(s.risk > 0) || s.risk > r.maxRiskATR*atr {
		return setup{}
	}
	if bar.HasAsk() {
		if spread := bar.AskClose - bar.BidClose; spread > r.maxSpreadR*s.risk {
			return setup{}
		}
	}
	if volMin > 0 && !r.volumeSpike(w, volMin) {
		return setup{}
	}
	return s
}

// volumeSpike : la dernière bougie de w a-t-elle un volume ≥ volMin ×
// médiane des volWindow précédentes ? Faux si un volume manque.
func (r revision) volumeSpike(w core.Series, volMin float64) bool {
	cur := len(w) - 1
	if cur < r.volWindow {
		return false
	}
	v := w[cur].Volume
	if math.IsNaN(v) {
		return false
	}
	ref := make([]float64, 0, r.volWindow)
	for _, b := range w[cur-r.volWindow : cur] {
		if math.IsNaN(b.Volume) {
			return false
		}
		ref = append(ref, b.Volume)
	}
	sort.Float64s(ref)
	n := len(ref)
	med := ref[n/2]
	if n%2 == 0 {
		med = (ref[n/2-1] + ref[n/2]) / 2
	}
	return med > 0 && v >= volMin*med
}

// target : niveau de la cible à rr × R.
func (s setup) target(rr float64) float64 {
	return s.entry + float64(s.side)*rr*s.risk
}

// swingHigh : w[j] est-il un plus haut de swing de force p ? Strict à
// gauche, large à droite : de deux sommets égaux consécutifs, seul le
// premier compte — deux fois le même niveau n'est qu'une zone.
func swingHigh(w core.Series, j, p int) bool {
	h := w[j].High()
	for k := 1; k <= p; k++ {
		if !(h > w[j-k].High()) || !(h >= w[j+k].High()) {
			return false
		}
	}
	return true
}

func swingLow(w core.Series, j, p int) bool {
	l := w[j].Low()
	for k := 1; k <= p; k++ {
		if !(l < w[j-k].Low()) || !(l <= w[j+k].Low()) {
			return false
		}
	}
	return true
}

// windowATR : ATR de Wilder de la dernière bougie, calculé sur la
// fenêtre SEULE — même définition que indicator.ATR (TR initial = H − L,
// lissage α = 1/période).
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
