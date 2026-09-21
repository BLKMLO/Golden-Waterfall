package feature

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// synthetic fabrique une série H1 plausible (marche aléatoire avec
// volatilité) pour éprouver les calculs. Aucune donnée inventée ne sort
// d'ici : elle ne sert qu'aux tests.
func synthetic(n int, seed int64) core.Series {
	rng := rand.New(rand.NewSource(seed))
	price := 1.10
	start := time.Date(2020, 1, 6, 0, 0, 0, 0, time.UTC) // un lundi
	out := make(core.Series, 0, n)
	for i := 0; i < n; i++ {
		drift := rng.NormFloat64() * 0.0008
		open := price
		price = math.Max(price+drift, 0.5)
		high := math.Max(open, price) + math.Abs(rng.NormFloat64())*0.0003
		low := math.Min(open, price) - math.Abs(rng.NormFloat64())*0.0003
		spread := 0.00012
		out = append(out, core.Bar{
			Time:     start.Add(time.Duration(i) * time.Hour),
			BidOpen:  open,
			BidHigh:  high,
			BidLow:   low,
			BidClose: price,
			AskOpen:  open + spread,
			AskHigh:  high + spread,
			AskLow:   low + spread,
			AskClose: price + spread,
			Volume:   50 + rng.Float64()*100,
		})
	}
	return out
}

// TestPrefixStability est LE garde-fou anti-fuite du projet.
//
// Si une feature regardait vers l'avant, sa valeur à l'indice k changerait
// selon qu'on lui donne la suite de la série ou non. En exigeant
// Compute(série)[:k] == Compute(série[:k]), on rend ce type de fuite
// impossible à introduire sans casser le test.
func TestPrefixStability(t *testing.T) {
	series := synthetic(600, 11)
	full := Compute(series)

	for _, k := range []int{120, 300, 455, 599} {
		partial := Compute(series.Slice(0, k))
		if partial.Rows != k {
			t.Fatalf("préfixe %d : %d lignes calculées", k, partial.Rows)
		}
		for row := 0; row < k; row++ {
			for col := 0; col < full.Cols; col++ {
				a, b := full.At(row, col), partial.At(row, col)
				if math.IsNaN(a) && math.IsNaN(b) {
					continue
				}
				// Tolérance strictement numérique : les calculs sont
				// identiques, seules les sommes glissantes peuvent
				// différer du dernier bit.
				if math.Abs(a-b) > 1e-12*math.Max(1, math.Abs(a)) {
					t.Fatalf("FUITE TEMPORELLE possible — colonne %q, ligne %d du préfixe %d : "+
						"%.15g avec la suite, %.15g sans", full.Names[col], row, k, a, b)
				}
			}
		}
	}
}

func TestColumnsAreStable(t *testing.T) {
	if len(Columns) != 34 {
		t.Fatalf("Colibri définit 34 features causales, %d déclarées", len(Columns))
	}
	seen := map[string]bool{}
	for _, c := range Columns {
		if seen[c] {
			t.Fatalf("colonne dupliquée : %q", c)
		}
		seen[c] = true
	}
	m := Compute(synthetic(200, 3))
	if m.Cols != len(Columns) {
		t.Fatalf("matrice à %d colonnes pour %d noms", m.Cols, len(Columns))
	}
	for i, name := range m.Names {
		if name != Columns[i] {
			t.Fatalf("ordre des colonnes modifié à l'indice %d : %q au lieu de %q", i, name, Columns[i])
		}
	}
}

func TestWarmupRowsAreNaNThenComplete(t *testing.T) {
	m := Compute(synthetic(400, 7))
	if m.RowComplete(0) {
		t.Fatal("la première ligne ne peut pas être complète : toutes les fenêtres sont vides")
	}
	if !m.RowComplete(m.Rows - 1) {
		t.Fatal("après 400 bougies, la dernière ligne doit être entièrement calculée")
	}
	firstComplete := -1
	for i := 0; i < m.Rows; i++ {
		if m.RowComplete(i) {
			firstComplete = i
			break
		}
	}
	if firstComplete < 0 {
		t.Fatal("aucune ligne complète : les features ne se stabilisent jamais")
	}
	if firstComplete > ContextBars {
		t.Fatalf("première ligne complète à l'indice %d, au-delà du contexte annoncé (%d)",
			firstComplete, ContextBars)
	}
}

func TestNoInfiniteValues(t *testing.T) {
	// Série volontairement dégénérée : prix constant, volume nul. C'est
	// le cas qui produisait des ±Inf (divisions par un écart nul).
	n := 200
	series := make(core.Series, n)
	start := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := range series {
		series[i] = core.Bar{
			Time:    start.Add(time.Duration(i) * time.Hour),
			BidOpen: 1.2, BidHigh: 1.2, BidLow: 1.2, BidClose: 1.2,
		}
	}
	m := Compute(series)
	for i, v := range m.Data {
		if math.IsInf(v, 0) {
			t.Fatalf("valeur infinie en colonne %q (ligne %d)", m.Names[i%m.Cols], i/m.Cols)
		}
	}
}

func TestCalendarFeaturesFollowISOConvention(t *testing.T) {
	// Lundi 6 janvier 2020, 14 h UTC → dow 0, days_to_friday 4,
	// session 2 (US).
	series := core.Series{{
		Time:    time.Date(2020, 1, 6, 14, 0, 0, 0, time.UTC),
		BidOpen: 1, BidHigh: 1, BidLow: 1, BidClose: 1,
	}}
	m := Compute(series)
	get := func(name string) float64 {
		idx, err := m.ColumnIndex(name)
		if err != nil {
			t.Fatal(err)
		}
		return m.At(0, idx)
	}
	if got := get("dow"); got != 0 {
		t.Fatalf("lundi doit valoir 0 (convention ISO), reçu %v", got)
	}
	if got := get("days_to_friday"); got != 4 {
		t.Fatalf("days_to_friday lundi = 4, reçu %v", got)
	}
	if got := get("hour_utc"); got != 14 {
		t.Fatalf("hour_utc = 14, reçu %v", got)
	}
	if got := get("session"); got != 2 {
		t.Fatalf("14 h UTC = session US (2), reçu %v", got)
	}
}

func TestATRIsPositiveAndCausal(t *testing.T) {
	series := synthetic(300, 5)
	atr := ATR(series)
	if len(atr) != len(series) {
		t.Fatalf("ATR de longueur %d pour %d bougies", len(atr), len(series))
	}
	if !math.IsNaN(atr[0]) {
		t.Fatal("l'ATR ne peut pas être défini sur la première bougie")
	}
	last := atr[len(atr)-1]
	if math.IsNaN(last) || last <= 0 {
		t.Fatalf("ATR final invalide : %v", last)
	}
	partial := ATR(series.Slice(0, 150))
	for i := 0; i < 150; i++ {
		if math.IsNaN(atr[i]) && math.IsNaN(partial[i]) {
			continue
		}
		if math.Abs(atr[i]-partial[i]) > 1e-12 {
			t.Fatalf("ATR non causal à l'indice %d : %v contre %v", i, atr[i], partial[i])
		}
	}
}
