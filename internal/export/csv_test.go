package export

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "\ufeff") {
		t.Fatal("marque d'ordre des octets absente : un tableur francophone lira mal les accents")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV illisible : %v", err)
	}
	return rows
}

func sampleTrades() []core.Trade {
	entry := time.Date(2024, 3, 4, 8, 0, 0, 0, time.UTC)
	return []core.Trade{
		{ID: 1, Symbol: "EURUSD", Strategy: "colibri_v1_0", Side: core.Buy, Quantity: 10000,
			EntryTime: entry, EntryPrice: 1.08123, ExitTime: entry.Add(6 * time.Hour),
			ExitPrice: 1.08456, PnL: 33.30, Cost: 1.20, ExitReason: "tp"},
		{ID: 2, Symbol: "EURUSD", Strategy: "colibri_v1_0", Side: core.Sell, Quantity: 10000,
			EntryTime: entry.Add(24 * time.Hour), EntryPrice: 1.08500,
			ExitTime: entry.Add(30 * time.Hour), ExitPrice: 1.08700, PnL: -20, Cost: 1.20,
			ExitReason: "sl"},
	}
}

// TestTradesExportsEveryFieldUnderItsHeader : une colonne décalée est le
// défaut le plus coûteux d'un export — le fichier s'ouvre, se lit, et
// ment. Chaque valeur est donc vérifiée SOUS son entête, par son nom.
func TestTradesExportsEveryFieldUnderItsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trades.csv")
	if err := Trades(path, sampleTrades()); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, path)
	if len(rows) != 3 {
		t.Fatalf("%d lignes, attendu 1 entête + 2 trades", len(rows))
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, name := range tradeHeader {
		if _, ok := col[name]; !ok {
			t.Fatalf("colonne %q absente", name)
		}
	}
	got := rows[1]
	for field, want := range map[string]string{
		"id": "1", "paire": "EURUSD", "sens": "BUY", "quantite": "10000,00",
		"entree": "2024-03-04 08:00:00", "prix_entree": "1,08123",
		"sortie": "2024-03-04 14:00:00", "duree_heures": "6,00",
		"pnl": "33,30", "cout": "1,20", "motif_sortie": "tp",
	} {
		if got[col[field]] != want {
			t.Errorf("colonne %s : %q, attendu %q", field, got[col[field]], want)
		}
	}
}

// TestUnavailableNumbersStayEmpty : la règle « — plutôt que zéro » de
// l'écran vaut aussi dans un fichier. Un zéro écrit à la place d'une
// métrique non mesurée serait additionné par le tableur.
func TestUnavailableNumbersStayEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.csv")
	s := backtest.Stats{
		Currency: "EUR", Trades: 4,
		MaxDrawdownPct: math.NaN(), Sharpe: math.NaN(),
		ProfitFactor: math.Inf(1), CostsModelled: false, CurrencyExact: true,
	}
	if err := Stats(path, s); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, row := range readCSV(t, path)[1:] {
		values[row[0]] = row[1]
	}
	for _, key := range []string{"drawdown_max_pct", "sharpe", "profit_factor"} {
		if values[key] != "" {
			t.Errorf("%s = %q : une valeur non finie doit rester une cellule VIDE", key, values[key])
		}
	}
	if values["couts_modelises"] != "non" || values["devise_exacte"] != "oui" {
		t.Errorf("les drapeaux d'honnêteté doivent voyager avec les chiffres : %v", values)
	}
}

// TestStatsExportIsReproducible : deux exports du même résultat donnent
// deux fichiers IDENTIQUES. L'ordre de parcours d'une map Go étant
// aléatoire, sans tri les motifs de refus changeaient de place à chaque
// écriture.
func TestStatsExportIsReproducible(t *testing.T) {
	dir := t.TempDir()
	s := backtest.Stats{
		Currency: "EUR",
		Rejections: map[string]int{
			"marge": 3, "plafond": 1, "kill-switch": 7, "sans stop": 2, "devise": 5,
		},
		ExitReasons: map[string]int{"tp": 9, "sl": 4, "time": 1},
	}
	var first string
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "stats.csv")
		if err := Stats(path, s); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(raw)
			continue
		}
		if string(raw) != first {
			t.Fatal("deux exports du même résultat diffèrent : l'ordre des motifs n'est pas déterministe")
		}
	}
}

// TestBacktestWritesThreeFiles : trois tables de formes différentes, donc
// trois fichiers — et des noms qui disent la paire, l'unité et l'instant.
func TestBacktestWritesThreeFiles(t *testing.T) {
	dir := t.TempDir()
	res := &backtest.Result{
		Stats:  backtest.Stats{Currency: "EUR", Trades: 2},
		Trades: sampleTrades(),
		Equity: []backtest.EquityPoint{
			{Time: time.Date(2024, 3, 4, 8, 0, 0, 0, time.UTC), Value: 10000},
			{Time: time.Date(2024, 3, 4, 12, 0, 0, 0, time.UTC), Value: 10033.3},
		},
	}
	at := time.Date(2026, 9, 22, 14, 30, 12, 0, time.UTC)
	paths, err := Backtest(dir, "EURUSD", "H4", res, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("%d fichiers, attendu 3", len(paths))
	}
	for _, p := range paths {
		base := filepath.Base(p)
		if !strings.Contains(base, "EURUSD") || !strings.Contains(base, "H4") ||
			!strings.Contains(base, "20260922-143012") {
			t.Errorf("nom peu parlant : %s", base)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("fichier annoncé mais absent : %v", err)
		}
	}
	// La courbe garde ses points, un par ligne.
	if rows := readCSV(t, paths[1]); len(rows) != 3 {
		t.Fatalf("courbe de valeur : %d lignes, attendu 1 entête + 2 points", len(rows))
	}
}

// TestNoPartialFileSurvivesAFailure : l'écriture passe par un fichier
// temporaire renommé. Un export interrompu ne doit pas laisser un CSV à
// moitié écrit qui passerait pour complet.
func TestNoPartialFileSurvivesAFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trades.csv")
	if err := Trades(path, sampleTrades()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("fichier temporaire laissé derrière : %s", e.Name())
		}
	}
}

// TestNameNeverBuildsAPath : un nom de fichier ne doit pas pouvoir
// s'échapper de son dossier, quelle que soit la paire qu'on lui donne.
func TestNameNeverBuildsAPath(t *testing.T) {
	name := Name("trades", time.Now(), "../../etc", "H 4")
	if strings.ContainsAny(name, `/\:`) {
		t.Fatalf("le nom contient un séparateur de chemin : %q", name)
	}
}
