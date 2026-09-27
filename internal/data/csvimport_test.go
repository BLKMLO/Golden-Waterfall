package data

import (
	"compress/gzip"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeText(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if strings.HasSuffix(name, ".gz") {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		w := gzip.NewWriter(f)
		w.Write([]byte(body))
		w.Close()
		f.Close()
		return path
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCSVWithHeaderBidAndAsk(t *testing.T) {
	path := writeText(t, "eurusd.csv", "\ufefftime,bid_open,bid_high,bid_low,bid_close,ask_open,ask_high,ask_low,ask_close,volume\n"+
		"2024-01-02 10:00:00,1.1,1.2,1.0,1.15,1.1002,1.2002,1.0002,1.1502,42\n"+
		"2024-01-02 10:01:00,1.15,1.16,1.14,1.155,,,,,\n")
	s, err := ReadCSV(path, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 {
		t.Fatalf("%d bougies", len(s))
	}
	if !s[0].Time.Equal(time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)) || s[0].BidHigh != 1.2 ||
		s[0].AskClose != 1.1502 || s[0].Volume != 42 {
		t.Fatalf("première bougie : %+v", s[0])
	}
	if s[1].HasAsk() || !math.IsNaN(s[1].Volume) {
		t.Fatalf("ask et volume vides = ABSENTS, pas nuls : %+v", s[1])
	}
}

// TestReadCSVMetaTraderWithoutHeader : export MetaTrader, sans entête,
// date et heure séparées, à l'heure du courtier (UTC+2 en hiver).
func TestReadCSVMetaTraderWithoutHeader(t *testing.T) {
	body := "2024.01.02,02:00,1.10,1.11,1.09,1.105,120\n2024.01.02,02:01,1.105,1.106,1.104,1.1055,80\n"
	path := writeText(t, "EURUSD1.csv", body)
	if _, err := ReadCSV(path, CSVOptions{}); err == nil || !strings.Contains(err.Error(), "--columns") {
		t.Fatalf("sans entête ni --columns, refus attendu avec la marche à suivre : %v", err)
	}
	athens, err := time.LoadLocation("Europe/Athens")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadCSV(path, CSVOptions{Location: athens,
		Columns: []string{"date", "time", "open", "high", "low", "close", "volume"}})
	if err != nil {
		t.Fatal(err)
	}
	if !s[0].Time.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("02:00 à Athènes en janvier = 00:00 UTC, lu %v", s[0].Time)
	}
	if s[0].BidOpen != 1.10 || s[0].Volume != 120 || s[0].HasAsk() {
		t.Fatalf("prix : %+v", s[0])
	}
}

// TestReadCSVHistDataSemicolon : format HistData (point-virgule, date
// compacte, heure de New York sans changement d'heure = UTC−5).
func TestReadCSVHistDataSemicolon(t *testing.T) {
	path := writeText(t, "DAT_ASCII_EURUSD_M1_2024.csv.gz",
		"20240102 000000;1.104270;1.104290;1.104200;1.104210;0\n20240102 000100;1.104210;1.104300;1.104190;1.104280;0\n")
	est, _ := time.LoadLocation("Etc/GMT+5")
	s, err := ReadCSV(path, CSVOptions{Location: est,
		Columns: []string{"time", "open", "high", "low", "close", "volume"}})
	if err != nil {
		t.Fatal(err)
	}
	if !s[0].Time.Equal(time.Date(2024, 1, 2, 5, 0, 0, 0, time.UTC)) || s[0].BidClose != 1.10421 {
		t.Fatalf("lu %+v", s[0])
	}
}

func TestReadCSVFrenchDecimalComma(t *testing.T) {
	path := writeText(t, "fr.csv", "Date;Ouverture;open;high;low;close\n"+
		"2024-01-02T10:00:00Z;x;1,1;1,2;1,0;1,15\n")
	s, err := ReadCSV(path, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s[0].BidHigh != 1.2 || s[0].BidClose != 1.15 {
		t.Fatalf("virgule décimale : %+v", s[0])
	}
}

// TestReadCSVSemicolonWithoutHeaderAndDecimalComma : autant de virgules
// que de points-virgules sur la ligne ; c'est le point-virgule qui sépare.
func TestReadCSVSemicolonWithoutHeaderAndDecimalComma(t *testing.T) {
	path := writeText(t, "fr.csv", "2024-01-02 10:00;1,1;1,2;1,0;1,15\n2024-01-02 10:01;1,15;1,2;1,1;1,12\n")
	s, err := ReadCSV(path, CSVOptions{Columns: []string{"time", "open", "high", "low", "close"}})
	if err != nil {
		t.Fatal(err)
	}
	if s[0].BidOpen != 1.1 || s[0].BidClose != 1.15 || s[1].BidClose != 1.12 {
		t.Fatalf("lu %+v", s)
	}
}

func TestReadCSVCompactDateIsNotAnEpoch(t *testing.T) {
	path := writeText(t, "d.csv", "date,open,high,low,close\n20240102,1,1,1,1\n")
	if _, err := ReadCSV(path, CSVOptions{}); err == nil {
		t.Fatal("« 20240102 » lu comme une époque Unix : une bougie de 1970")
	}
}

func TestReadCSVEpochTimestamps(t *testing.T) {
	ms := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC).UnixMilli()
	for name, v := range map[string]int64{"secondes": ms / 1000, "millisecondes": ms} {
		path := writeText(t, "e.csv", fmt.Sprintf("timestamp,open,high,low,close\n%d,1,1,1,1\n", v))
		s, err := ReadCSV(path, CSVOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if s[0].Time.UnixMilli() != ms {
			t.Errorf("%s : %v", name, s[0].Time)
		}
	}
}

// TestReadCSVRefusesAmbiguousDates : 01/02 et 03/04 se lisent aussi bien
// JJ/MM que MM/JJ. Parier donnerait des bougies plausibles et fausses.
func TestReadCSVRefusesAmbiguousDates(t *testing.T) {
	path := writeText(t, "a.csv", "time,open,high,low,close\n01/02/2024 10:00,1,1,1,1\n03/04/2024 10:00,1,1,1,1\n")
	if _, err := ReadCSV(path, CSVOptions{}); err == nil || !strings.Contains(err.Error(), "AMBIGU") {
		t.Fatalf("dates ambiguës acceptées : %v", err)
	}
	// Un 13 du mois tranche : JJ/MM.
	path = writeText(t, "b.csv", "time,open,high,low,close\n01/02/2024 10:00,1,1,1,1\n13/02/2024 10:00,1,1,1,1\n")
	s, err := ReadCSV(path, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s[0].Time.Month() != time.February || s[0].Time.Day() != 1 {
		t.Fatalf("01/02 avec un 13/02 dans le fichier = 1er février, lu %v", s[0].Time)
	}
	// Et l'utilisateur peut trancher lui-même.
	s, err = ReadCSV(writeText(t, "c.csv", "time,open,high,low,close\n01/02/2024 10:00,1,1,1,1\n"),
		CSVOptions{TimeLayout: "01/02/2006 15:04"})
	if err != nil || s[0].Time.Month() != time.January {
		t.Fatalf("--time-format MM/JJ : %v %v", s, err)
	}
}

func TestReadCSVRefusesBadLines(t *testing.T) {
	for name, body := range map[string]string{
		"prix illisible": "time,open,high,low,close\n2024-01-02 10:00,1,1,1,1\n2024-01-02 10:01,1,abc,1,1\n",
		"prix négatif":   "time,open,high,low,close\n2024-01-02 10:00,1,1,-1,1\n",
		"high < low":     "time,open,high,low,close\n2024-01-02 10:00,1,0.9,1.1,1\n",
		"ligne courte":   "time,open,high,low,close\n2024-01-02 10:00,1,1\n",
		"entête seule":   "time,open,high,low,close\n",
		"sans prix":      "time,price\n2024-01-02 10:00,1\n",
	} {
		if _, err := ReadCSV(writeText(t, "x.csv", body), CSVOptions{}); err == nil {
			t.Errorf("%s : fichier accepté", name)
		}
	}
	_, err := ReadCSV(writeText(t, "y.csv", "time,open,high,low,close\n2024-01-02 10:00,1,1,1,1\n2024-01-02 10:01,1,abc,1,1\n"), CSVOptions{})
	if err == nil || !strings.Contains(err.Error(), "ligne 3") {
		t.Fatalf("le numéro de la ligne fautive doit être donné : %v", err)
	}
}

func m1CSV(start time.Time, n int, step time.Duration) string {
	var b strings.Builder
	b.WriteString("time,open,high,low,close\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%s,1.1,1.2,1.0,1.15\n", start.Add(time.Duration(i)*step).Format("2006-01-02 15:04:05"))
	}
	return b.String()
}

func TestImportCSVEndToEnd(t *testing.T) {
	dir := t.TempDir()
	// Vendredi 5 janvier 2024, 21 h 00 → dimanche : les bougies du samedi
	// et du dimanche sont écartées et comptées.
	start := time.Date(2024, 1, 5, 21, 0, 0, 0, time.UTC)
	path := writeText(t, "eurusd.csv", m1CSV(start, 3*24*60, time.Minute))
	report, err := Import(dir, "eurusd", []string{path}, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Symbol != "EURUSD" || report.WeekendDropped != 2*24*60 || report.Bars != 3*60+(24*60-3*60) {
		t.Fatalf("rapport : %+v", report)
	}
	h, err := ReadHeader(FilePath(dir, "EURUSD", 2024))
	if err != nil || !h.Imported || h.Complete() || h.Source != "import" {
		t.Fatalf("en-tête : %+v (%v)", h, err)
	}
	s, _, err := ReadSeries(FilePath(dir, "EURUSD", 2024), time.Time{}, time.Time{})
	if err != nil || !math.IsNaN(s[0].Volume) {
		t.Fatalf("volume absent du CSV = NaN, lu %v (%v)", s[0].Volume, err)
	}

	// Une deuxième fois : l'année existe, rien n'est écrasé sans --replace.
	if _, err := Import(dir, "EURUSD", []string{path}, ImportOptions{}); err == nil ||
		!strings.Contains(err.Error(), "--replace") {
		t.Fatalf("écrasement silencieux : %v", err)
	}
	if _, err := Import(dir, "EURUSD", []string{path}, ImportOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
}

// TestImportRefusesNonM1 : un fichier H1 importé comme du M1 ferait des
// bougies « d'une minute » qui durent une heure.
func TestImportRefusesNonM1(t *testing.T) {
	path := writeText(t, "h1.csv", m1CSV(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), 48, time.Hour))
	_, err := Import(t.TempDir(), "EURUSD", []string{path}, ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "M1") {
		t.Fatalf("historique H1 accepté : %v", err)
	}
}
