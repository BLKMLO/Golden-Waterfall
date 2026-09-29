package label

import (
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// bars construit une série horaire à partir de quadruplets OHLC.
func bars(ohlc [][4]float64) core.Series {
	start := time.Date(2022, 1, 3, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, len(ohlc))
	for i, v := range ohlc {
		out[i] = core.Bar{
			Time:     start.Add(time.Duration(i) * time.Hour),
			BidOpen:  v[0],
			BidHigh:  v[1],
			BidLow:   v[2],
			BidClose: v[3],
			Volume:   1,
		}
	}
	return out
}

// flat génère assez de bougies pour que l'ATR de Wilder (14) soit défini,
// avec une amplitude connue.
func flat(n int, price, amplitude float64) [][4]float64 {
	out := make([][4]float64, n)
	for i := range out {
		out[i] = [4]float64{price, price + amplitude, price - amplitude, price}
	}
	return out
}
