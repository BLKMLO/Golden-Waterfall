package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAppendAndReadTrades(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 3; i++ {
		id, err := s.AppendTrade(core.Trade{Symbol: "EURUSD", PnL: float64(i)})
		if err != nil {
			t.Fatal(err)
		}
		if id != int64(i+1) {
			t.Fatalf("identifiant %d, attendu %d", id, i+1)
		}
	}
	trades, err := s.Trades(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 3 {
		t.Fatalf("%d trades relus", len(trades))
	}
	// Du plus récent au plus ancien.
	if trades[0].PnL != 2 || trades[2].PnL != 0 {
		t.Fatalf("ordre inattendu : %v", trades)
	}
}

func TestTradesLimit(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 10; i++ {
		s.AppendTrade(core.Trade{Symbol: "EURUSD"})
	}
	trades, _ := s.Trades(4)
	if len(trades) != 4 {
		t.Fatalf("%d trades pour une limite de 4", len(trades))
	}
}

func TestTradingDefaultsToOff(t *testing.T) {
	s := openTemp(t)
	// Une paire ne trade JAMAIS parce qu'on a oublié de la désarmer.
	if s.Trading("EURUSD") {
		t.Fatal("une paire inconnue doit être considérée comme désarmée")
	}
}

func TestTradingPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrading("EURUSD", true); err != nil {
		t.Fatal(err)
	}
	s.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if !again.Trading("EURUSD") {
		t.Fatal("l'interrupteur doit survivre à un redémarrage")
	}
	if armed := again.ArmedSymbols(); len(armed) != 1 || armed[0] != "EURUSD" {
		t.Fatalf("paires armées : %v", armed)
	}
}

// TestDayStartEquityIsNeverOverwritten verrouille la propriété qui donne
// tout son sens à la limite de perte journalière : un redémarrage ne doit
// pas remettre le compteur à zéro et rouvrir le robinet des entrées.
func TestDayStartEquityIsNeverOverwritten(t *testing.T) {
	s := openTemp(t)
	day := time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC)

	if _, ok, _ := s.DayStartEquity(day); ok {
		t.Fatal("aucun repère ne doit exister avant le premier relevé")
	}
	if err := s.SetDayStartEquity(day, 10000); err != nil {
		t.Fatal(err)
	}
	// Deuxième relevé le même jour, après une perte : il ne doit RIEN
	// écraser.
	if err := s.SetDayStartEquity(day.Add(6*time.Hour), 9000); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.DayStartEquity(day)
	if err != nil || !ok {
		t.Fatalf("repère introuvable : %v / %v", ok, err)
	}
	if v != 10000 {
		t.Fatalf("le repère du jour doit rester %v, reçu %v", 10000.0, v)
	}
	// Le lendemain, un nouveau repère est posé.
	next := day.AddDate(0, 0, 1)
	if _, ok, _ := s.DayStartEquity(next); ok {
		t.Fatal("chaque journée UTC a son propre repère")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	s := openTemp(t)
	type pref struct {
		Timeframe string `json:"tf"`
		Rows      int    `json:"rows"`
	}
	if err := s.PutMeta("ui", pref{Timeframe: "H4", Rows: 12}); err != nil {
		t.Fatal(err)
	}
	var back pref
	found, err := s.GetMeta("ui", &back)
	if err != nil || !found {
		t.Fatalf("clé introuvable : %v / %v", found, err)
	}
	if back.Timeframe != "H4" || back.Rows != 12 {
		t.Fatalf("valeur altérée : %+v", back)
	}
	if found, _ := s.GetMeta("absente", &back); found {
		t.Fatal("une clé absente doit renvoyer false, pas une valeur par défaut")
	}
}

func TestCloseTwiceIsSafe(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// bbolt renvoie une erreur sur une seconde fermeture ; le programme
	// ne doit pas paniquer pour autant.
	_ = s.Close()
}
