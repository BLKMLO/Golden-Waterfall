package data

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func init() {
	// Un faux serveur qui répond 500 ne doit pas coûter quinze secondes.
	backoffUnit = time.Millisecond
}

func TestSourceRegistry(t *testing.T) {
	names := SourceNames()
	for _, want := range []string{"dukascopy", "fxcm"} {
		if !slices.Contains(names, want) {
			t.Fatalf("source %q absente du registre : %v", want, names)
		}
	}
	if _, err := NewSource("inconnue", SourceOptions{}); err == nil {
		t.Fatal("une source inconnue doit être refusée")
	}
	for _, name := range names {
		info, err := DescribeSource(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != name || info.Label == "" || info.Unit == "" || info.FirstYear < FirstYear || info.Note == "" {
			t.Fatalf("fiche incomplète pour %s : %+v", name, info)
		}
	}
}

func TestSourceNameMatchesDefaultSource(t *testing.T) {
	if _, err := DescribeSource(DefaultSource); err != nil {
		t.Fatalf("la source par défaut doit être enregistrée : %v", err)
	}
}

func TestServedSymbols(t *testing.T) {
	src, _ := NewSource("fxcm", SourceOptions{})
	served, unserved := ServedSymbols(src, []string{"EURUSD", "CHFJPY", "XAUUSD", "usdjpy"})
	if !slices.Equal(served, []string{"EURUSD", "usdjpy"}) || !slices.Equal(unserved, []string{"CHFJPY", "XAUUSD"}) {
		t.Fatalf("partage FXCM : servies %v, non servies %v", served, unserved)
	}
	duka, _ := NewSource("dukascopy", SourceOptions{})
	for _, sym := range SupportedSymbols() {
		if !duka.Serves(sym) {
			t.Fatalf("Dukascopy publie tout le catalogue, pas %s ?", sym)
		}
	}
}

// --- FXCM -----------------------------------------------------------------

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestDecodeFXCMRealExcerpt : extrait RÉEL du fichier 2017/1 d'EURUSD
// (26 lignes : l'entête, le début et la fin de la semaine).
func TestDecodeFXCMRealExcerpt(t *testing.T) {
	bars, err := DecodeFXCM(readFixture(t, "fxcm_EURUSD_2017_1_extrait.csv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 25 {
		t.Fatalf("%d bougies, 25 attendues", len(bars))
	}
	first := bars[0]
	want := time.Date(2017, time.January, 3, 0, 0, 0, 0, time.UTC)
	if !first.Time.Equal(want) || first.Time.Location() != time.UTC {
		t.Fatalf("première bougie à %v, attendue %v UTC", first.Time, want)
	}
	// 01/03/2017 00:00:00.000,1.05174,1.05174,1.04571,1.04581,1.05295,1.05295,1.04575,1.04586
	if first.BidOpen != 1.05174 || first.BidHigh != 1.05174 || first.BidLow != 1.04571 || first.BidClose != 1.04581 {
		t.Fatalf("bid mal lu : %+v", first)
	}
	if first.AskOpen != 1.05295 || first.AskClose != 1.04586 || !first.HasAsk() {
		t.Fatalf("ask mal lu : %+v", first)
	}
	if !math.IsNaN(first.Volume) {
		t.Fatalf("FXCM ne publie pas de volume : NaN attendu, pas %v", first.Volume)
	}
	last := bars[len(bars)-1]
	if !last.Time.Equal(time.Date(2017, time.January, 6, 21, 58, 0, 0, time.UTC)) {
		t.Fatalf("dernière bougie à %v", last.Time)
	}
}

func TestDecodeFXCMColumnsByName(t *testing.T) {
	// Colonnes réordonnées et sans ask : lues par leur nom.
	csv := "BidClose,DateTime,BidLow,BidHigh,BidOpen\r\n1.2,01/02/2018 10:00:00.000,1.1,1.3,1.15\r\n"
	bars, err := DecodeFXCM([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 || bars[0].BidOpen != 1.15 || bars[0].BidHigh != 1.3 || bars[0].BidLow != 1.1 ||
		bars[0].BidClose != 1.2 || bars[0].HasAsk() {
		t.Fatalf("lecture par nom : %+v", bars)
	}
}

func TestDecodeFXCMRejectsGarbage(t *testing.T) {
	for name, csv := range map[string]string{
		"entête":     "Time,Open,Close\n",
		"date":       "DateTime,BidOpen,BidHigh,BidLow,BidClose\n2018-01-02,1,1,1,1\n",
		"prix":       "DateTime,BidOpen,BidHigh,BidLow,BidClose\n01/02/2018 10:00:00.000,1,x,1,1\n",
		"prix nul":   "DateTime,BidOpen,BidHigh,BidLow,BidClose\n01/02/2018 10:00:00.000,1,1,0,1\n",
		"tronquée":   "DateTime,BidOpen,BidHigh,BidLow,BidClose\n01/02/2018 10:00:00.000,1,1\n",
		"gzip cassé": "\x1f\x8b\x08\x00garbage",
	} {
		if _, err := DecodeFXCM([]byte(csv)); err == nil {
			t.Errorf("%s : un fichier illisible doit être refusé, pas lu à moitié", name)
		}
	}
}

func TestFXCMCandidatesStopAtNow(t *testing.T) {
	now := time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC)
	c := fxcmCandidates(2026, now)
	if c[0] != (fxcmWeek{2025, 52}) || c[1] != (fxcmWeek{2025, 53}) {
		t.Fatalf("les semaines 52 et 53 de l'année précédente portent le début de l'année : %v", c[:2])
	}
	lastWeek := c[len(c)-1]
	// Semaine 4 : ne commence pas avant le 1/1 + 15 j = 16/1 ≤ 20/1 ;
	// semaine 5 : pas avant le 23/1 > 20/1.
	if lastWeek != (fxcmWeek{2026, 4}) {
		t.Fatalf("dernière semaine candidate %v, attendue 2026/4", lastWeek)
	}
	full := fxcmCandidates(2018, now)
	if len(full) != 2+53 {
		t.Fatalf("année passée : %d candidates, 55 attendues", len(full))
	}
}

func TestMissingMarketWeeks(t *testing.T) {
	bar := func(y int, m time.Month, d, h int) core.Bar {
		return core.Bar{Time: time.Date(y, m, d, h, 0, 0, 0, time.UTC), BidOpen: 1, BidHigh: 1, BidLow: 1, BidClose: 1}
	}
	now := time.Date(2025, time.January, 20, 0, 0, 0, 0, time.UTC)
	// Semaine du lundi 30/12/2024 : dans 2025, seuls le 2 et le 3 janvier
	// comptent (le 1er est férié) — elle est attendue. Semaine du 6/1 :
	// ouverte le DIMANCHE 5 au soir, rangée avec la semaine qui suit.
	// Semaine du 13/1 : vide, donc manquante.
	bars := core.Series{bar(2025, time.January, 2, 10), bar(2025, time.January, 5, 22), bar(2025, time.January, 8, 10)}
	got := missingMarketWeeks(bars, 2025, now)
	want := []time.Time{time.Date(2025, time.January, 13, 0, 0, 0, 0, time.UTC)}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Fatalf("semaines manquantes %v, attendues %v", got, want)
	}
	// Sans la bougie du 2 janvier, la première semaine manque aussi.
	got = missingMarketWeeks(bars[1:], 2025, now)
	if len(got) != 2 || !got[0].Equal(time.Date(2024, time.December, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("première semaine vide non comptée : %v", got)
	}
	// 2021 : le 1er janvier est un vendredi, seul jour de sa semaine dans
	// l'année, et férié : rien à y attendre.
	got = missingMarketWeeks(core.Series{bar(2021, time.January, 5, 10)}, 2021,
		time.Date(2021, time.January, 9, 0, 0, 0, 0, time.UTC))
	if len(got) != 0 {
		t.Fatalf("semaine du seul 1er janvier comptée manquante : %v", got)
	}
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

// TestFXCMDownloadYearEndToEnd : faux serveur FXCM, écriture du fichier,
// compte des semaines manquantes, provenance dans les métadonnées.
func TestFXCMDownloadYearEndToEnd(t *testing.T) {
	const head = "DateTime,BidOpen,BidHigh,BidLow,BidClose,AskOpen,AskHigh,AskLow,AskClose\r\n"
	files := map[string]string{
		// Semaine 52 de 2016 : entièrement hors de 2017, écartée.
		"/EURUSD/2016/52.csv.gz": head + "12/27/2016 00:00:00.000,1.04,1.05,1.03,1.045,1.041,1.051,1.031,1.046\r\n",
		"/EURUSD/2017/1.csv.gz": head +
			"01/03/2017 00:00:00.000,1.05174,1.05174,1.04571,1.04581,1.05295,1.05295,1.04575,1.04586\r\n" +
			"01/06/2017 21:58:00.000,1.05317,1.05319,1.05294,1.05319,1.05347,1.05373,1.05344,1.05372\r\n",
		// Semaine 2 absente (404) : c'est un TROU, pas un marché fermé.
		// Le dimanche soir (réouverture) est écarté : sinon la clôture de
		// fin de semaine du backtest tomberait APRÈS le week-end.
		"/EURUSD/2017/3.csv.gz": head +
			"01/15/2017 22:00:00.000,1.05,1.05,1.05,1.05,1.0502,1.0502,1.0502,1.0502\r\n" +
			"01/16/2017 10:00:00.000,1.06,1.06,1.06,1.06,1.0602,1.0602,1.0602,1.0602\r\n",
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(gz(t, body))
	}))
	defer srv.Close()

	src, err := NewSource("fxcm", SourceOptions{BaseURL: srv.URL, Concurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	now := time.Date(2017, time.January, 23, 12, 0, 0, 0, time.UTC)
	dl := &Downloader{Source: src, HistoryDir: dir, Now: func() time.Time { return now }}
	var last DownloadProgress
	n, err := dl.DownloadYear(context.Background(), "EURUSD", 2017, func(p DownloadProgress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("%d bougies écrites, 3 attendues (la semaine 52 de 2016 est hors de l'année, "+
			"le dimanche 15 est écarté)", n)
	}
	if last.Unit != "semaines" || last.UnitsDone != last.UnitsTotal || last.Source != "fxcm" {
		t.Fatalf("avancement : %+v", last)
	}
	h, err := ReadHeader(FilePath(dir, "EURUSD", 2017))
	if err != nil {
		t.Fatal(err)
	}
	if h.Source != "fxcm" || h.Failures != 1 || h.Complete() || h.Imported {
		t.Fatalf("en-tête : %+v — la semaine du 9/1 manque, l'année doit rester incomplète", h)
	}
	s, _, err := ReadSeries(FilePath(dir, "EURUSD", 2017), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(s[0].Volume) || !s[0].HasAsk() {
		t.Fatalf("relu : volume %v (NaN attendu), ask %v", s[0].Volume, s[0].HasAsk())
	}
	inv, err := Catalog(dir)
	if err != nil || len(inv) != 1 || !slices.Equal(inv[0].Sources, []string{"fxcm"}) {
		t.Fatalf("inventaire : %+v (%v)", inv, err)
	}

	// Une paire non publiée n'est pas une panne : ErrNotServed.
	if _, err := dl.DownloadYear(context.Background(), "XAUUSD", 2017, nil); !errors.Is(err, ErrNotServed) {
		t.Fatalf("XAUUSD chez FXCM : ErrNotServed attendu, reçu %v", err)
	}
	// Une année antérieure à la source n'est pas demandée.
	before := requests.Load()
	if n, err := dl.DownloadYear(context.Background(), "EURUSD", 2011, nil); n != 0 || err != nil || requests.Load() != before {
		t.Fatalf("2011 chez FXCM : %d bougies, %v, %d requêtes", n, err, requests.Load()-before)
	}
}

// --- Dukascopy -----------------------------------------------------------

// TestDukascopyDownloadYearEndToEnd : faux serveur Dukascopy. Un jour en
// 500 persistant est un jour MANQUANT, un 404 un jour fermé.
func TestDukascopyDownloadYearEndToEnd(t *testing.T) {
	day := func(d int) string { return fmt.Sprintf("/EURUSD/2024/00/%02d/", d) }
	bid := encodeBi5(t, [][6]float64{{0, 110000, 110010, 109990, 110020, 12}, {60, 110010, 110020, 110000, 110030, 7}})
	ask := encodeBi5(t, [][6]float64{{0, 110002, 110012, 109992, 110022, 0}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, day(2)) && strings.HasSuffix(r.URL.Path, "BID_candles_min_1.bi5"):
			w.Write(bid)
		case strings.HasPrefix(r.URL.Path, day(2)) && strings.HasSuffix(r.URL.Path, "ASK_candles_min_1.bi5"):
			w.Write(ask)
		case strings.HasPrefix(r.URL.Path, day(3)):
			http.Error(w, "panne", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src, err := NewSource("dukascopy", SourceOptions{BaseURL: srv.URL, Concurrency: 4, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	now := time.Date(2024, time.January, 5, 12, 0, 0, 0, time.UTC)
	dl := &Downloader{Source: src, HistoryDir: dir, Now: func() time.Time { return now }}
	n, err := dl.DownloadYear(context.Background(), "EURUSD", 2024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d bougies, 2 attendues", n)
	}
	h, err := ReadHeader(FilePath(dir, "EURUSD", 2024))
	if err != nil {
		t.Fatal(err)
	}
	if h.Source != "dukascopy" || h.Failures != 1 {
		t.Fatalf("en-tête %+v : source dukascopy et un jour manquant (le 3) attendus", h)
	}
	s, _, err := ReadSeries(FilePath(dir, "EURUSD", 2024), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if s[0].Volume != 12 || !s[0].HasAsk() || s[1].HasAsk() {
		t.Fatalf("fusion bid/ask : %+v", s)
	}
}

func TestHTTPGetGivesUpAndReportsTheURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "panne", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := httpGet(context.Background(), http.DefaultClient, srv.URL+"/x", "test", 3)
	if err == nil || !strings.Contains(err.Error(), "/x") || hits.Load() != 3 {
		t.Fatalf("après 3 tentatives : %v (%d requêtes)", err, hits.Load())
	}
}

func TestNewHTTPClientHonoursProxyEnvironment(t *testing.T) {
	// Sans Proxy, un poste derrière un proxy d'entreprise ne télécharge
	// rien, et le message ne parle que de délai dépassé.
	tr, ok := newHTTPClient().Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatal("le client HTTP doit honorer HTTPS_PROXY / HTTP_PROXY")
	}
}

func TestDropWeekend(t *testing.T) {
	at := func(d int) core.Bar { return core.Bar{Time: time.Date(2024, time.January, d, 22, 0, 0, 0, time.UTC)} }
	// Vendredi 5, samedi 6, dimanche 7, lundi 8.
	kept, dropped := DropWeekend(core.Series{at(5), at(6), at(7), at(8)})
	if dropped != 2 || len(kept) != 2 || kept[0].Time.Day() != 5 || kept[1].Time.Day() != 8 {
		t.Fatalf("gardées %v, écartées %d", kept, dropped)
	}
	// Ce qu'on protège : avec le dimanche, la dernière bougie de la
	// semaine ISO serait celle du dimanche soir.
	flags := core.LastBarsOfWeek(kept)
	if !flags[0] {
		t.Fatal("sans dimanche, le vendredi doit clore la semaine")
	}
}
