package data

import (
	"bytes"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/ulikunitz/xz/lzma"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func sample(n int) core.Series {
	start := time.Date(2023, 5, 15, 8, 0, 0, 0, time.UTC)
	out := make(core.Series, n)
	for i := range out {
		p := 1.08 + float64(i)*0.0001
		out[i] = core.Bar{
			Time:    start.Add(time.Duration(i) * time.Minute),
			BidOpen: p, BidHigh: p + 0.0002, BidLow: p - 0.0002, BidClose: p + 0.0001,
			AskOpen: p + 0.00012, AskHigh: p + 0.00032, AskLow: p - 0.00008, AskClose: p + 0.00022,
			Volume: float64(10 + i),
		}
	}
	return out
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, "EURUSD", 2023)
	series := sample(500)
	header := FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}
	if err := WriteSeries(path, header, series); err != nil {
		t.Fatal(err)
	}

	back, h, err := ReadSeries(path, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Symbol != "EURUSD" || h.Year != 2023 || h.Count != int64(len(series)) {
		t.Fatalf("en-tête relu incohérent : %+v", h)
	}
	if len(back) != len(series) {
		t.Fatalf("%d bougies relues pour %d écrites", len(back), len(series))
	}
	for i := range series {
		a, b := series[i], back[i]
		if !a.Time.Equal(b.Time) {
			t.Fatalf("bougie %d : horodatage %s relu %s", i, a.Time, b.Time)
		}
		// Les prix sont stockés en entiers mis à l'échelle : la
		// conversion doit être sans perte à 1/scale près.
		for _, pair := range [][2]float64{
			{a.BidOpen, b.BidOpen}, {a.BidHigh, b.BidHigh},
			{a.BidLow, b.BidLow}, {a.BidClose, b.BidClose},
			{a.AskClose, b.AskClose},
		} {
			if math.Abs(pair[0]-pair[1]) > 1e-5 {
				t.Fatalf("bougie %d : prix %v relu %v", i, pair[0], pair[1])
			}
		}
	}
}

func TestReadSeriesSeeksByDate(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, "EURUSD", 2023)
	series := sample(1000)
	if err := WriteSeries(path, FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}
	from := series[600].Time
	to := series[700].Time
	back, _, err := ReadSeries(path, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 101 {
		t.Fatalf("%d bougies pour une fenêtre de 101", len(back))
	}
	if !back[0].Time.Equal(from) || !back[len(back)-1].Time.Equal(to) {
		t.Fatalf("fenêtre décalée : %s → %s", back[0].Time, back[len(back)-1].Time)
	}
}

func TestLoadRejectsInvertedPeriod(t *testing.T) {
	_, err := Load(t.TempDir(), "EURUSD",
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("une période inversée doit être refusée d'emblée")
	}
}

func TestLoadIgnoresStrayFiles(t *testing.T) {
	dir := t.TempDir()
	symDir := filepath.Join(dir, "EURUSD")
	if err := WriteSeries(FilePath(dir, "EURUSD", 2023),
		FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}, sample(10)); err != nil {
		t.Fatal(err)
	}
	// Copie manuelle au nom non conforme : elle DOIT être ignorée, sinon
	// les bougies seraient comptées deux fois.
	stray := filepath.Join(symDir, "EURUSD_m1_sauvegarde.gwb")
	if err := WriteSeries(stray, FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}, sample(10)); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "EURUSD", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("%d bougies chargées : le fichier parasite n'a pas été ignoré", len(got))
	}
}

func TestSanitizeSortsAndDeduplicates(t *testing.T) {
	t0 := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	s := core.Series{
		{Time: t0.Add(2 * time.Minute), BidClose: 3},
		{Time: t0, BidClose: 1},
		{Time: t0.Add(time.Minute), BidClose: 2},
		{Time: t0.Add(time.Minute), BidClose: 99}, // doublon : le dernier gagne
	}
	got := Sanitize(s)
	if len(got) != 3 {
		t.Fatalf("%d bougies après déduplication, 3 attendues", len(got))
	}
	if got[1].BidClose != 99 {
		t.Fatalf("le doublon le plus récent doit l'emporter, reçu %v", got[1].BidClose)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Time.After(got[i-1].Time) {
			t.Fatal("la série n'est pas triée strictement")
		}
	}
}

func TestTimeframeFloor(t *testing.T) {
	ts := time.Date(2024, 3, 14, 13, 47, 23, 0, time.UTC) // jeudi
	cases := []struct {
		tf   Timeframe
		want time.Time
	}{
		{M5, time.Date(2024, 3, 14, 13, 45, 0, 0, time.UTC)},
		{M15, time.Date(2024, 3, 14, 13, 45, 0, 0, time.UTC)},
		{H1, time.Date(2024, 3, 14, 13, 0, 0, 0, time.UTC)},
		{H4, time.Date(2024, 3, 14, 12, 0, 0, 0, time.UTC)},
		{D1, time.Date(2024, 3, 14, 0, 0, 0, 0, time.UTC)},
		{W1, time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC)}, // lundi ISO
		{MN1, time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := c.tf.Floor(ts); !got.Equal(c.want) {
			t.Fatalf("%s : bucket %s, attendu %s", c.tf, got, c.want)
		}
	}
}

func TestResampleAggregates(t *testing.T) {
	series := sample(60) // 60 bougies M1 consécutives à partir de 08:00
	h1 := Resample(series, H1)
	if len(h1) != 1 {
		t.Fatalf("60 bougies M1 alignées sur une heure → 1 bougie H1, reçu %d", len(h1))
	}
	b := h1[0]
	if !b.Time.Equal(time.Date(2023, 5, 15, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket H1 mal aligné : %s", b.Time)
	}
	if b.BidOpen != series[0].BidOpen {
		t.Fatal("l'open de la bougie agrégée doit être celui de la PREMIÈRE M1")
	}
	if b.BidClose != series[len(series)-1].BidClose {
		t.Fatal("le close doit être celui de la DERNIÈRE M1")
	}
	var volume float64
	high, low := math.Inf(-1), math.Inf(1)
	for _, s := range series {
		volume += s.Volume
		high = math.Max(high, s.BidHigh)
		low = math.Min(low, s.BidLow)
	}
	if math.Abs(b.Volume-volume) > 1e-9 {
		t.Fatalf("volume agrégé %v, somme attendue %v", b.Volume, volume)
	}
	if b.BidHigh != high || b.BidLow != low {
		t.Fatal("extrêmes mal agrégés")
	}
	if !b.HasAsk() {
		t.Fatal("le côté ask doit survivre au ré-échantillonnage (il sert à mesurer le spread)")
	}
}

func TestResampleSkipsEmptyBuckets(t *testing.T) {
	// Deux bougies séparées par un week-end : aucun bucket vide ne doit
	// être inventé entre les deux.
	s := core.Series{
		{Time: time.Date(2024, 3, 8, 21, 0, 0, 0, time.UTC), BidOpen: 1, BidHigh: 1, BidLow: 1, BidClose: 1},
		{Time: time.Date(2024, 3, 11, 1, 0, 0, 0, time.UTC), BidOpen: 2, BidHigh: 2, BidLow: 2, BidClose: 2},
	}
	got := Resample(s, H4)
	if len(got) != 2 {
		t.Fatalf("%d bougies H4 : le week-end ne doit pas être comblé", len(got))
	}
}

func TestCandleURLUsesZeroBasedMonth(t *testing.T) {
	inst, err := LookupInstrument("EURUSD")
	if err != nil {
		t.Fatal(err)
	}
	got := CandleURL(inst, time.Date(2024, time.January, 3, 0, 0, 0, 0, time.UTC), SideBid)
	want := BaseURL + "/EURUSD/2024/00/03/BID_candles_min_1.bi5"
	if got != want {
		t.Fatalf("URL Dukascopy : %s\nattendue        : %s", got, want)
	}
	got = CandleURL(inst, time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), SideAsk)
	want = BaseURL + "/EURUSD/2024/11/31/ASK_candles_min_1.bi5"
	if got != want {
		t.Fatalf("URL de décembre : %s\nattendue        : %s", got, want)
	}
}

// encodeBi5 fabrique un fichier bi5 valide pour éprouver le décodage sans
// dépendre du réseau.
func encodeBi5(t *testing.T, recs [][6]float64) []byte {
	t.Helper()
	var raw bytes.Buffer
	for _, r := range recs {
		buf := make([]byte, 24)
		binary.BigEndian.PutUint32(buf[0:], uint32(r[0]))
		binary.BigEndian.PutUint32(buf[4:], uint32(r[1]))  // open
		binary.BigEndian.PutUint32(buf[8:], uint32(r[2]))  // close
		binary.BigEndian.PutUint32(buf[12:], uint32(r[3])) // low
		binary.BigEndian.PutUint32(buf[16:], uint32(r[4])) // high
		binary.BigEndian.PutUint32(buf[20:], math.Float32bits(float32(r[5])))
		raw.Write(buf)
	}
	var out bytes.Buffer
	w, err := lzma.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestDecodeCandlesFieldOrder verrouille le piège du format bi5 :
// l'ordre des champs est OPEN, CLOSE, LOW, HIGH — pas OHLC.
func TestDecodeCandlesFieldOrder(t *testing.T) {
	inst, _ := LookupInstrument("EURUSD")
	day := time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC)
	payload := encodeBi5(t, [][6]float64{
		{60, 108000, 108050, 107900, 108100, 42}, // o=1.08 c=1.0805 l=1.079 h=1.081
	})
	got, err := DecodeCandles(payload, inst, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d bougies décodées", len(got))
	}
	c := got[0]
	if !c.Time.Equal(day.Add(time.Minute)) {
		t.Fatalf("horodatage %s, attendu %s", c.Time, day.Add(time.Minute))
	}
	near := func(got, want float64, what string) {
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s : %v attendu %v", what, got, want)
		}
	}
	near(c.Open, 1.08, "open")
	near(c.Close, 1.0805, "close")
	near(c.Low, 1.079, "low")
	near(c.High, 1.081, "high")
	near(c.Volume, 42, "volume")
	if !(c.Low <= c.Open && c.Open <= c.High) {
		t.Fatal("incohérence OHLC : l'ordre des champs bi5 est probablement inversé")
	}
}

func TestDecodeCandlesRejectsCorruptFile(t *testing.T) {
	inst, _ := LookupInstrument("EURUSD")
	day := time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	w, _ := lzma.NewWriter(&out)
	w.Write(make([]byte, 30)) // 30 n'est pas un multiple de 24
	w.Close()
	if _, err := DecodeCandles(out.Bytes(), inst, day); err == nil {
		t.Fatal("un fichier de taille incohérente doit être refusé, pas décodé partiellement")
	}
}

func TestDecodeEmptyPayloadIsNotAnError(t *testing.T) {
	inst, _ := LookupInstrument("EURUSD")
	got, err := DecodeCandles(nil, inst, time.Now())
	if err != nil || got != nil {
		t.Fatalf("un jour sans donnée n'est pas une erreur : %v / %v", got, err)
	}
}

func TestUnknownSymbolIsRejected(t *testing.T) {
	if _, err := LookupInstrument("XXXYYY"); err == nil {
		t.Fatal("un symbole inconnu doit être refusé avec la liste des symboles supportés")
	}
}

func TestParseTimeframeRejectsUnknown(t *testing.T) {
	if _, err := ParseTimeframe("H3"); err == nil {
		t.Fatal("une unité de temps inconnue doit être refusée, jamais devinée")
	}
	tf, err := ParseTimeframe("  h4 ")
	if err != nil || tf != H4 {
		t.Fatalf("la casse et les espaces doivent être tolérés : %v / %v", tf, err)
	}
}

// TestTimeframesMatchConfig garantit que la liste dupliquée dans le
// paquet config (qui ne peut pas dépendre de data) ne diverge pas.
func TestTimeframesMatchConfig(t *testing.T) {
	known := config.KnownTimeframes()
	if len(known) != len(Timeframes) {
		t.Fatalf("%d unités côté config, %d côté data", len(known), len(Timeframes))
	}
	for i, tf := range Timeframes {
		if known[i] != string(tf) {
			t.Fatalf("divergence à l'indice %d : config %q, data %q", i, known[i], tf)
		}
	}
}

// TestIncompleteYearIsRedownloaded : une année écrite avec des trous doit
// être REFAITE à la relance. Sans cette distinction, elle passe pour faite
// et ses jours manquants le restent définitivement.
func TestIncompleteYearIsRedownloaded(t *testing.T) {
	dir := t.TempDir()
	const currentYear = 2026

	// Année complète : rien à refaire.
	if err := WriteSeries(FilePath(dir, "EURUSD", 2023),
		FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000}, sample(10)); err != nil {
		t.Fatal(err)
	}
	if NeedsDownload(dir, "EURUSD", 2023, currentYear) {
		t.Fatal("une année complète ne doit pas être retéléchargée")
	}

	// Année trouée : à refaire.
	if err := WriteSeries(FilePath(dir, "EURUSD", 2022),
		FileHeader{Symbol: "EURUSD", Year: 2022, Scale: 100000, Failures: 7}, sample(10)); err != nil {
		t.Fatal(err)
	}
	if !NeedsDownload(dir, "EURUSD", 2022, currentYear) {
		t.Fatal("une année incomplète DOIT être retéléchargée")
	}

	// Année absente : à faire.
	if !NeedsDownload(dir, "EURUSD", 2021, currentYear) {
		t.Fatal("une année absente doit être téléchargée")
	}

	// Année en cours : toujours à refaire, elle est incomplète par nature.
	// (Série vide : seul l'en-tête compte pour ce que le test vérifie.)
	if err := WriteSeries(FilePath(dir, "EURUSD", currentYear),
		FileHeader{Symbol: "EURUSD", Year: currentYear, Scale: 100000}, nil); err != nil {
		t.Fatal(err)
	}
	if !NeedsDownload(dir, "EURUSD", currentYear, currentYear) {
		t.Fatal("l'année en cours doit toujours être rafraîchie")
	}
}

func TestHeaderCarriesFailureCount(t *testing.T) {
	path := FilePath(t.TempDir(), "EURUSD", 2023)
	if err := WriteSeries(path,
		FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000, Failures: 3}, sample(5)); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(path)
	if err != nil {
		t.Fatal(err)
	}
	if h.Failures != 3 {
		t.Fatalf("%d échecs relus, 3 attendus", h.Failures)
	}
	if h.Complete() {
		t.Fatal("un fichier avec des échecs n'est pas complet")
	}
}

func TestCatalogReportsPartialYears(t *testing.T) {
	dir := t.TempDir()
	WriteSeries(FilePath(dir, "EURUSD", 2022),
		FileHeader{Symbol: "EURUSD", Year: 2022, Scale: 100000}, sample(5))
	WriteSeries(FilePath(dir, "EURUSD", 2023),
		FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000, Failures: 2}, sample(5))

	inv, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 1 {
		t.Fatalf("%d symbole(s) inventorié(s)", len(inv))
	}
	if len(inv[0].Years) != 2 {
		t.Fatalf("%d année(s) présente(s)", len(inv[0].Years))
	}
	if len(inv[0].Partial) != 1 || inv[0].Partial[0] != 2023 {
		t.Fatalf("les années incomplètes doivent être signalées : %v", inv[0].Partial)
	}
}
