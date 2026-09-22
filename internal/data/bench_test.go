package data

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// benchM1 fabrique une année de bougies M1 : c'est la taille réelle du
// problème (une paire forex, une année, ≈ 372 000 bougies), et c'est ce
// volume que le walk-forward ré-échantillonne pour chaque paire et chaque
// année demandée.
func benchM1(n int) core.Series {
	rng := rand.New(rand.NewSource(7))
	start := time.Date(2022, 1, 3, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, n)
	// Marche aléatoire sur l'ENTIER, pas sur le flottant : Dukascopy
	// publie des prix entiers mis à l'échelle, et un banc d'essai de
	// stockage nourri de doubles à quinze décimales mesurerait la
	// compression d'un bruit qui n'existe sur aucun marché.
	const pip = 100000.0
	k := int64(110000)
	for i := range out {
		k += int64(math.Round(rng.NormFloat64() * 20))
		if k < 50000 {
			k = 50000
		}
		price := func(ticks int64) float64 { return float64(k+ticks) / pip }
		out[i] = core.Bar{
			Time:    start.Add(time.Duration(i) * time.Minute),
			BidOpen: price(0), BidHigh: price(20), BidLow: price(-20), BidClose: price(0),
			AskOpen: price(10), AskHigh: price(30), AskLow: price(-10), AskClose: price(10),
			Volume: 3,
		}
	}
	return out
}

const benchBars = 372_000

func BenchmarkResampleH4(b *testing.B) {
	s := benchM1(benchBars)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Resample(s, H4)
	}
}

func BenchmarkResampleM5(b *testing.B) {
	s := benchM1(benchBars)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Resample(s, M5)
	}
}

func BenchmarkResampleD1(b *testing.B) {
	s := benchM1(benchBars)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Resample(s, D1)
	}
}

// resampleNaive est l'implémentation de RÉFÉRENCE : un plancher recalculé
// pour chaque bougie, comparé au plancher courant. Elle est lente et
// évidemment correcte — c'est tout ce qu'on lui demande.
//
// Elle existe pour que l'optimisation de Resample (plancher recalculé
// seulement au changement de bucket) reste vérifiable : sans ce témoin,
// « plus rapide » et « toujours juste » ne sont plus liés que par la
// confiance.
func resampleNaive(series core.Series, tf Timeframe) core.Series {
	if tf == M1 || len(series) == 0 {
		return series
	}
	out := core.Series{}
	var cur core.Bar
	var bucket time.Time
	open := false
	for _, bar := range series {
		b := tf.Floor(bar.Time)
		if !open || !b.Equal(bucket) {
			if open {
				out = append(out, cur)
			}
			bucket, open = b, true
			cur = bar
			cur.Time = b
			continue
		}
		cur.BidHigh = math.Max(cur.BidHigh, bar.BidHigh)
		cur.BidLow = math.Min(cur.BidLow, bar.BidLow)
		cur.BidClose = bar.BidClose
		if bar.HasAsk() {
			if !cur.HasAsk() {
				cur.AskOpen, cur.AskHigh, cur.AskLow = bar.AskOpen, bar.AskHigh, bar.AskLow
			} else {
				cur.AskHigh = math.Max(cur.AskHigh, bar.AskHigh)
				cur.AskLow = math.Min(cur.AskLow, bar.AskLow)
			}
			cur.AskClose = bar.AskClose
		}
		cur.Volume += bar.Volume
	}
	if open {
		out = append(out, cur)
	}
	return out
}

// TestResampleMatchesNaive : le raccourci du plancher ne change RIEN au
// résultat, sur toutes les unités livrées et sur une série trouée.
func TestResampleMatchesNaive(t *testing.T) {
	full := benchM1(20_000)

	// Série trouée : un saut de plusieurs buckets, exactement ce que
	// produit une fermeture de week-end.
	holed := append(core.Series{}, full[:5_000]...)
	for i := 12_000; i < 20_000; i++ {
		holed = append(holed, full[i])
	}

	for _, tf := range Timeframes {
		for name, s := range map[string]core.Series{"continue": full, "trouée": holed} {
			want := resampleNaive(s, tf)
			got := Resample(s, tf)
			if len(got) != len(want) {
				t.Fatalf("%s %s : %d bougies, attendu %d", tf, name, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s %s bougie %d :\n got %+v\nwant %+v", tf, name, i, got[i], want[i])
				}
			}
		}
	}
}

// TestResampleDoesNotOverAllocate : la capacité réservée suit l'unité
// demandée. C'est le défaut corrigé — 93 000 bougies réservées pour en
// produire 1 560 — et rien dans le résultat ne le trahirait.
func TestResampleDoesNotOverAllocate(t *testing.T) {
	s := benchM1(20_000)
	out := Resample(s, H4)
	if cap(out) > 4*len(out)+8 {
		t.Fatalf("capacité %d pour %d bougies : la réserve ne suit pas l'unité",
			cap(out), len(out))
	}
}

// --- Stockage : Parquet contre l'ancien format maison ---------------------
//
// Les deux chiffres qui décident sont la TAILLE et la LECTURE : un
// historique complet fait plusieurs gigaoctets, et le walk-forward relit
// chaque année à chaque entraînement. L'écriture, elle, se produit une
// fois derrière un téléchargement réseau qui dure des minutes.

func benchHeader() FileHeader {
	return FileHeader{Symbol: "EURUSD", Year: 2022, Scale: 100000}
}

func BenchmarkStoreWriteParquet(b *testing.B) {
	s := benchM1(benchBars)
	path := filepath.Join(b.TempDir(), "EURUSD_m1_2022.parquet")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := WriteSeries(path, benchHeader(), s); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if info, err := os.Stat(path); err == nil {
		b.ReportMetric(float64(info.Size())/1e6, "Mo/fichier")
	}
}

func BenchmarkStoreReadParquet(b *testing.B) {
	s := benchM1(benchBars)
	path := filepath.Join(b.TempDir(), "EURUSD_m1_2022.parquet")
	if err := WriteSeries(path, benchHeader(), s); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ReadSeries(path, time.Time{}, time.Time{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStoreReadParquetOneMonth : la lecture bornée ne doit pas
// coûter la lecture de l'année. C'est ce que l'ancien format obtenait par
// seek, et que les groupes de lignes doivent rendre.
func BenchmarkStoreReadParquetOneMonth(b *testing.B) {
	s := benchM1(benchBars)
	path := filepath.Join(b.TempDir(), "EURUSD_m1_2022.parquet")
	if err := WriteSeries(path, benchHeader(), s); err != nil {
		b.Fatal(err)
	}
	from := s[0].Time.AddDate(0, 6, 0)
	to := from.AddDate(0, 1, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ReadSeries(path, from, to); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreReadLegacy(b *testing.B) {
	s := benchM1(benchBars)
	path := filepath.Join(b.TempDir(), "EURUSD_m1_2022.gwb")
	writeLegacySeries(b, path, benchHeader(), s)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ReadSeries(path, time.Time{}, time.Time{}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if info, err := os.Stat(path); err == nil {
		b.ReportMetric(float64(info.Size())/1e6, "Mo/fichier")
	}
}
