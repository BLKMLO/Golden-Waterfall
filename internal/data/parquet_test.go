package data

import (
	"bufio"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// m1Year fabrique `n` bougies M1 aux prix ARRONDIS à cinq décimales,
// comme Dukascopy les publie. Une bougie sur `askGap` n'a pas de côté ask.
func m1Year(year, n, askGap int) core.Series {
	start := time.Date(year, time.January, 3, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, n)
	// Les prix sont construits DEPUIS des entiers, comme Dukascopy les
	// publie. Les fabriquer par additions de flottants donnerait des
	// cotations à quinze décimales qui n'existent sur aucun marché, et le
	// test mesurerait alors l'arrondi de la machine, pas le format.
	const pip = 100000.0
	for i := range out {
		k := 110000 + int64(i%997)
		price := func(ticks int64) float64 { return float64(k+ticks) / pip }
		bar := core.Bar{
			Time:     start.Add(time.Duration(i) * time.Minute),
			BidOpen:  price(0),
			BidHigh:  price(20),
			BidLow:   price(-20),
			BidClose: price(1),
			Volume:   float64(i%17) + 1,
		}
		if askGap == 0 || i%askGap != 0 {
			bar.AskOpen = price(8)
			bar.AskHigh = price(28)
			bar.AskLow = price(-12)
			bar.AskClose = price(9)
		}
		out[i] = bar
	}
	return out
}

// TestParquetRoundTripIsExact : les prix reviennent au CENTIÈME DE PIP
// près. Un format de stockage qui arrondit invente des cotations, et
// toutes les mesures de spread qui en découlent sont fausses.
func TestParquetRoundTripIsExact(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, "EURUSD", 2023)
	series := m1Year(2023, 5000, 0)
	header := FileHeader{Symbol: "EURUSD", Year: 2023, Scale: 100000, Failures: 3}

	if err := WriteSeries(path, header, series); err != nil {
		t.Fatal(err)
	}
	back, h, err := ReadSeries(path, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sameSeries(series, back); err != nil {
		t.Fatal(err)
	}
	if h.Symbol != "EURUSD" || h.Year != 2023 || h.Count != int64(len(series)) {
		t.Fatalf("en-tête perdu : %+v", h)
	}
	if h.Failures != 3 || h.Complete() {
		t.Fatalf("les jours en échec doivent survivre au format : %+v", h)
	}
	if h.Imported {
		t.Fatal("un fichier que nous avons écrit n'est pas un import")
	}
}

// TestMissingAskIsNullNotZero : le côté ask absent s'écrit NULL.
//
// Zéro serait un PRIX, et l'outil tiers qui ouvre le fichier l'ajouterait
// à ses moyennes. C'est la règle « — plutôt que zéro » de l'interface,
// appliquée au fichier — et elle ne se vérifie qu'en relisant le fichier
// comme le ferait cet outil tiers, pas par nos propres accesseurs.
func TestMissingAskIsNullNotZero(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, "EURUSD", 2023)
	series := m1Year(2023, 300, 10) // une bougie sur dix sans ask
	if err := WriteSeries(path, FileHeader{
		Symbol: "EURUSD", Year: 2023, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}

	rows, err := parquet.ReadFile[parquetBar](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(series) {
		t.Fatalf("%d lignes relues pour %d écrites", len(rows), len(series))
	}
	nulls := 0
	for i, r := range rows {
		if series[i].HasAsk() {
			if r.AskClose == nil {
				t.Fatalf("bougie %d : côté ask perdu", i)
			}
			continue
		}
		nulls++
		if r.AskOpen != nil || r.AskHigh != nil || r.AskLow != nil || r.AskClose != nil {
			t.Fatalf("bougie %d : ask absent écrit comme une valeur (%v)", i, r.AskClose)
		}
	}
	if nulls != 30 {
		t.Fatalf("%d bougies nulles, attendu 30", nulls)
	}

	back, _, err := ReadSeries(path, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range back {
		if back[i].HasAsk() != series[i].HasAsk() {
			t.Fatalf("bougie %d : présence du côté ask non conservée à la relecture", i)
		}
	}
}

// TestRangeReadSkipsRowGroups : lire trois mois ne doit pas coûter la
// lecture de l'année — c'est la propriété que l'ancien format tenait par
// seek et que les groupes de lignes tiennent ici.
func TestRangeReadSkipsRowGroups(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, "EURUSD", 2023)
	series := m1Year(2023, 3*rowGroupSize+1000, 0)
	if err := WriteSeries(path, FileHeader{
		Symbol: "EURUSD", Year: 2023, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}

	f, pf, err := openParquet(path)
	if err != nil {
		t.Fatal(err)
	}
	groups := len(pf.RowGroups())
	f.Close()
	if groups < 3 {
		t.Fatalf("%d groupe(s) de lignes : le découpage ne sert à rien", groups)
	}

	from := series[len(series)-500].Time
	to := series[len(series)-1].Time
	got, _, err := ReadSeries(path, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 500 {
		t.Fatalf("%d bougies pour une fenêtre de 500", len(got))
	}
	if !got[0].Time.Equal(from) || !got[len(got)-1].Time.Equal(to) {
		t.Fatalf("fenêtre décalée : %s → %s", got[0].Time, got[len(got)-1].Time)
	}
}

// TestForeignColumnNamesAreAccepted : un fichier venu d'ailleurs nomme
// ses colonnes `open`, `high`, `low`, `close`. Le refuser reviendrait à
// avoir changé de format sans en récolter le bénéfice.
func TestForeignColumnNamesAreAccepted(t *testing.T) {
	type foreignBar struct {
		Timestamp int64   `parquet:"timestamp,timestamp(millisecond)"`
		Open      float64 `parquet:"open"`
		High      float64 `parquet:"high"`
		Low       float64 `parquet:"low"`
		Close     float64 `parquet:"close"`
		Volume    float64 `parquet:"volume"`
	}
	path := filepath.Join(t.TempDir(), "EURUSD_m1_2021.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := parquet.NewGenericWriter[foreignBar](f)
	start := time.Date(2021, time.March, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]foreignBar, 240)
	for i := range rows {
		rows[i] = foreignBar{
			Timestamp: start.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Open:      1.2, High: 1.21, Low: 1.19, Close: 1.205, Volume: 7,
		}
	}
	if _, err := w.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, h, err := ReadSeries(path, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("fichier tiers refusé : %v", err)
	}
	if len(got) != 240 {
		t.Fatalf("%d bougies relues pour 240", len(got))
	}
	if got[0].BidClose != 1.205 || got[0].HasAsk() {
		t.Fatalf("colonnes mal appariées : %+v", got[0])
	}
	if !h.Imported {
		t.Fatal("un fichier sans nos métadonnées doit être signalé comme importé")
	}
	if h.Complete() {
		t.Fatal("une année dont personne n'a compté les jours manquants n'est pas complète")
	}
	if h.Symbol != "EURUSD" || h.Year != 2021 {
		t.Fatalf("symbole et année devaient venir du nom de fichier : %+v", h)
	}
}

// TestMisnamedColumnIsRefused : mieux vaut refuser un fichier que
// d'apparier une colonne au hasard. Une colonne mal appariée produit des
// prix crédibles et faux.
func TestMisnamedColumnIsRefused(t *testing.T) {
	type odd struct {
		When  int64   `parquet:"when,timestamp(millisecond)"`
		Price float64 `parquet:"price"`
	}
	path := filepath.Join(t.TempDir(), "EURUSD_m1_2021.parquet")
	f, _ := os.Create(path)
	w := parquet.NewGenericWriter[odd](f)
	if _, err := w.Write([]odd{{When: 0, Price: 1}}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	f.Close()

	if _, _, err := ReadSeries(path, time.Time{}, time.Time{}); err == nil {
		t.Fatal("un fichier sans colonne de prix reconnaissable doit être refusé")
	}
}

// --- Ancien format : lecture et migration ---------------------------------

// writeLegacySeries écrit un vrai .gwb.
//
// Elle vit dans les TESTS et nulle part ailleurs : le programme ne doit
// plus pouvoir produire ce format — un format qu'on ne peut plus écrire
// ne peut plus se répandre — mais le lecteur doit rester éprouvé contre
// de vrais octets, pas contre une idée de ce qu'ils contiennent.
func writeLegacySeries(t testing.TB, path string, h FileHeader, series core.Series) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)

	head := make([]byte, headerSize)
	copy(head[0:4], fileMagic)
	binary.LittleEndian.PutUint16(head[4:], fileVersion)
	copy(head[8:8+symbolFieldLn], h.Symbol)
	binary.LittleEndian.PutUint32(head[24:], uint32(int32(h.Year)))
	binary.LittleEndian.PutUint32(head[28:], uint32(h.Scale))
	binary.LittleEndian.PutUint64(head[32:], uint64(len(series)))
	binary.LittleEndian.PutUint32(head[40:], uint32(h.Failures))
	if _, err := w.Write(head); err != nil {
		t.Fatal(err)
	}

	yearStart := time.Date(h.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	scale := float64(h.Scale)
	put := func(dst []byte, price float64) {
		if price <= 0 || math.IsNaN(price) {
			binary.LittleEndian.PutUint32(dst, 0)
			return
		}
		binary.LittleEndian.PutUint32(dst, uint32(int32(math.Round(price*scale))))
	}
	buf := make([]byte, recordSize)
	for _, bar := range series {
		secs := int64(bar.Time.UTC().Sub(yearStart) / time.Second)
		binary.LittleEndian.PutUint32(buf[0:], uint32(int32(secs)))
		put(buf[4:], bar.BidOpen)
		put(buf[8:], bar.BidHigh)
		put(buf[12:], bar.BidLow)
		put(buf[16:], bar.BidClose)
		put(buf[20:], bar.AskOpen)
		put(buf[24:], bar.AskHigh)
		put(buf[28:], bar.AskLow)
		put(buf[32:], bar.AskClose)
		binary.LittleEndian.PutUint32(buf[36:], math.Float32bits(float32(bar.Volume)))
		if _, err := w.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyFilesStillLoad : les gigaoctets déjà téléchargés doivent
// continuer de se lire sans rien faire. Casser la lecture d'un format
// qu'on abandonne, c'est demander à l'utilisateur de retélécharger
// quinze ans d'historique parce qu'on a changé d'avis.
func TestLegacyFilesStillLoad(t *testing.T) {
	dir := t.TempDir()
	series := m1Year(2018, 2000, 7)
	writeLegacySeries(t, legacyFilePath(dir, "EURUSD", 2018),
		FileHeader{Symbol: "EURUSD", Year: 2018, Scale: 100000, Failures: 2}, series)

	got, err := Load(dir, "EURUSD", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sameSeries(series, got); err != nil {
		t.Fatal(err)
	}

	cat, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) != 1 || len(cat[0].Legacy) != 1 || cat[0].Legacy[0] != 2018 {
		t.Fatalf("l'inventaire doit signaler l'année restée en ancien format : %+v", cat)
	}
	if len(cat[0].Partial) != 1 {
		t.Fatalf("les jours en échec de l'ancien en-tête doivent survivre : %+v", cat[0])
	}
}

// TestMigrateConvertsAndVerifies : la conversion relit ce qu'elle vient
// d'écrire AVANT d'effacer quoi que ce soit.
func TestMigrateConvertsAndVerifies(t *testing.T) {
	dir := t.TempDir()
	series := m1Year(2018, 4000, 13)
	legacy := legacyFilePath(dir, "EURUSD", 2018)
	writeLegacySeries(t, legacy,
		FileHeader{Symbol: "EURUSD", Year: 2018, Scale: 100000, Failures: 5}, series)

	// Sans --remove, l'original reste.
	report, err := Migrate(dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Converted != 1 || report.Failed != 0 {
		t.Fatalf("migration : %+v", report)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("sans --remove, l'ancien fichier doit rester")
	}
	if report.After >= report.Before {
		t.Fatalf("le Parquet devrait être plus petit : %d → %d", report.Before, report.After)
	}

	// Le Parquet gagne quand les deux existent, et il a gardé le compte
	// des jours en échec.
	h, err := ReadHeader(FilePath(dir, "EURUSD", 2018))
	if err != nil {
		t.Fatal(err)
	}
	if h.Failures != 5 {
		t.Fatalf("jours en échec perdus : %d", h.Failures)
	}
	got, err := Load(dir, "EURUSD", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sameSeries(series, got); err != nil {
		t.Fatal(err)
	}

	// Avec --remove, l'original part — et seulement après vérification.
	report, err = Migrate(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Converted != 1 || report.Freed == 0 {
		t.Fatalf("migration avec suppression : %+v", report)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("avec --remove, l'ancien fichier doit disparaître")
	}
	if rest, _ := LegacyFiles(dir); len(rest) != 0 {
		t.Fatalf("il reste des .gwb : %v", rest)
	}
}

// TestImportMarksWhatItDoesNotKnow : un historique importé n'a pas de
// compte de jours manquants. Le déclarer complet serait affirmer une
// chose que personne n'a vérifiée.
func TestImportMarksWhatItDoesNotKnow(t *testing.T) {
	src := filepath.Join(t.TempDir(), "eurusd.parquet")
	series := append(m1Year(2020, 600, 0), m1Year(2021, 600, 0)...)
	if err := WriteSeries(src, FileHeader{
		Symbol: "EURUSD", Year: 2020, Scale: 100000}, series); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	report, err := Import(dir, "EURUSD", []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Years) != 2 || report.Years[0] != 2020 || report.Years[1] != 2021 {
		t.Fatalf("l'import doit découper par année : %+v", report)
	}
	if report.Bars != int64(len(series)) {
		t.Fatalf("%d bougies importées pour %d", report.Bars, len(series))
	}
	for _, y := range report.Years {
		h, err := ReadHeader(FilePath(dir, "EURUSD", y))
		if err != nil {
			t.Fatal(err)
		}
		if !h.Imported || h.Complete() {
			t.Fatalf("%d : un import doit rester marqué comme tel (%+v)", y, h)
		}
	}
	back, err := Load(dir, "EURUSD", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sameSeries(series, back); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyYearIsNotRedownloaded : une année déjà présente dans
// l'ancien format ne doit pas repartir en téléchargement sous prétexte
// qu'on a changé d'extension. Quinze ans d'historique, c'est des heures
// de réseau — et un 429 au bout de trois minutes.
func TestLegacyYearIsNotRedownloaded(t *testing.T) {
	dir := t.TempDir()
	writeLegacySeries(t, legacyFilePath(dir, "EURUSD", 2018),
		FileHeader{Symbol: "EURUSD", Year: 2018, Scale: 100000}, m1Year(2018, 100, 0))

	if NeedsDownload(dir, "EURUSD", 2018, 2026) {
		t.Fatal("une année complète en .gwb ne doit pas être retéléchargée")
	}
	if !NeedsDownload(dir, "EURUSD", 2017, 2026) {
		t.Fatal("une année absente doit l'être")
	}

	// Incomplète en .gwb : toujours à refaire.
	writeLegacySeries(t, legacyFilePath(dir, "EURUSD", 2019),
		FileHeader{Symbol: "EURUSD", Year: 2019, Scale: 100000, Failures: 4}, m1Year(2019, 100, 0))
	if !NeedsDownload(dir, "EURUSD", 2019, 2026) {
		t.Fatal("une année incomplète doit être retéléchargée, quel que soit son format")
	}
}

// TestReadsAFileWrittenByAnotherTool : le fichier de testdata/ n'a PAS
// été écrit par ce programme — il vient de pyarrow, avec des noms de
// colonnes différents, un horodatage en microsecondes, aucun côté ask et
// aucune métadonnée. C'est exactement le cas qui a motivé l'abandon du
// format maison, et il ne se vérifie pas en relisant nos propres octets.
func TestReadsAFileWrittenByAnotherTool(t *testing.T) {
	path := filepath.Join("testdata", "foreign_EURUSD_m1_2021.parquet")
	series, h, err := ReadSeries(path, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("fichier tiers refusé : %v", err)
	}
	if len(series) != 120 {
		t.Fatalf("%d bougies relues pour 120", len(series))
	}
	want := time.Date(2021, time.March, 1, 0, 0, 0, 0, time.UTC)
	if !series[0].Time.Equal(want) {
		t.Fatalf("horodatage : %s au lieu de %s", series[0].Time, want)
	}
	if series[0].BidClose != 1.2 || series[0].BidHigh != 1.2001 {
		t.Fatalf("prix mal appariés : %+v", series[0])
	}
	if series[0].HasAsk() {
		t.Fatal("ce fichier n'a pas de côté ask : en inventer un fausserait tous les coûts")
	}
	if series[0].Volume != 1 {
		t.Fatalf("volume : %v", series[0].Volume)
	}
	if !h.Imported || h.Complete() {
		t.Fatalf("un fichier venu d'ailleurs ne peut pas être déclaré complet : %+v", h)
	}

	// Et il s'importe dans l'historique, découpé par année.
	dir := t.TempDir()
	report, err := Import(dir, "EURUSD", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Years) != 1 || report.Years[0] != 2021 || report.Bars != 120 {
		t.Fatalf("import : %+v", report)
	}
	back, err := Load(dir, "EURUSD", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sameSeries(series, back); err != nil {
		t.Fatal(err)
	}
}

// TestTimeUnitFallsBackOnMagnitude : un export « brut » écrit un entier
// sans type logique. Se tromper d'unité décale l'historique d'un facteur
// mille — soit des bougies datées de 1970, soit de l'an 55 000.
func TestTimeUnitFallsBackOnMagnitude(t *testing.T) {
	type bare struct {
		Time int64 `parquet:"time"`
	}
	field := parquet.SchemaOf(bare{}).Fields()[0]
	ms := time.Date(2021, time.March, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	for name, sample := range map[string]int64{
		"secondes":      ms / 1000,
		"millisecondes": ms,
		"microsecondes": ms * 1000,
		"nanosecondes":  ms * 1000000,
	} {
		div, err := timeUnit(field, sample)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		if got := toMillis(sample, div); got != ms {
			t.Errorf("%s : %d ms au lieu de %d", name, got, ms)
		}
	}
}
