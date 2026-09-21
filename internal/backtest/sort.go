package backtest

import (
	"sort"
	"time"
)

func sortTimes(t []time.Time) {
	sort.Slice(t, func(i, j int) bool { return t[i].Before(t[j]) })
}
