package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/ff_week_2026-09-20.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseFeedReadsTheRealFormat(t *testing.T) {
	events, err := ParseFeedJSON(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 {
		t.Fatalf("%d annonces lues, 9 attendues", len(events))
	}
	// « 2026-09-23T21:30:00-04:00 » = 24 septembre 1 h 30 UTC.
	var found bool
	for _, e := range events {
		if e.Title == "Employment Change" {
			found = true
			if want := time.Date(2026, 9, 24, 1, 30, 0, 0, time.UTC); !e.Time.Equal(want) || e.Currency != "AUD" || e.Impact != ImpactHigh {
				t.Fatalf("annonce mal lue : %+v", e)
			}
		}
		if e.Title == "Bank Holiday" && e.Impact != ImpactNone {
			t.Fatalf("un jour férié n'est jamais filtrant : %+v", e)
		}
	}
	if !found {
		t.Fatal("annonce « Employment Change » absente")
	}
	if w := WeeksOf(events); len(w) != 1 || !w[0].Equal(time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("une semaine attendue, commençant le dimanche 20 à 0 h New York (4 h UTC) : %v", w)
	}
}

func TestParseFeedRefusesMalformedEvents(t *testing.T) {
	for _, raw := range []string{
		`[{"title":"x","country":"USD","date":"hier","impact":"High"}]`,
		`[{"title":"x","country":"USD","date":"2026-09-21T10:00:00-04:00","impact":"Énorme"}]`,
		`[{"title":"x","country":"US","date":"2026-09-21T10:00:00-04:00","impact":"High"}]`,
		`pas du json`,
	} {
		if _, err := ParseFeedJSON([]byte(raw)); err == nil {
			t.Fatalf("entrée invalide acceptée : %s", raw)
		}
	}
}

func gateFrom(t *testing.T, min Impact) *Gate {
	t.Helper()
	events, err := ParseFeedJSON(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	a := NewArchive(t.TempDir())
	if _, err := a.Save(Batch{Events: events, Weeks: WeeksOf(events), Origin: "test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cal, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &Gate{Calendar: cal, Before: 30 * time.Minute, After: 30 * time.Minute, MinImpact: min}
}

func TestGateBlocksAroundAnnouncementsOfThePairCurrencies(t *testing.T) {
	g := gateFrom(t, ImpactHigh)
	employment := time.Date(2026, 9, 24, 1, 30, 0, 0, time.UTC)
	cases := []struct {
		symbol string
		at     time.Time
		want   Verdict
	}{
		{"AUDUSD", employment.Add(-20 * time.Minute), Blocked}, // annonce dans les 30 min qui suivent
		{"AUDUSD", employment.Add(25 * time.Minute), Blocked},  // annonce il y a moins de 30 min
		{"AUDUSD", employment.Add(-45 * time.Minute), Clear},   // hors fenêtre
		{"EURAUD", employment, Blocked},                        // AUD en cotation
		{"EURUSD", employment, Clear},                          // aucune devise concernée
		{"AUDUSD", time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), Uncovered},
		// Lagarde (EUR, impact moyen) ne bloque pas avec un seuil « high ».
		{"EURUSD", time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC), Clear},
	}
	for _, c := range cases {
		if got, ev := g.Check(c.symbol, c.at); got != c.want {
			t.Errorf("%s à %s : verdict %d, %d attendu (%v)", c.symbol, c.at, got, c.want, ev)
		}
	}
	medium := gateFrom(t, ImpactMedium)
	if got, ev := medium.Check("EURUSD", time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)); got != Blocked || ev.Currency != "EUR" {
		t.Fatalf("seuil « medium » : Lagarde doit bloquer EURUSD, reçu %d %v", got, ev)
	}
	// Un jour férié ne bloque jamais, même au seuil le plus bas.
	low := gateFrom(t, ImpactLow)
	if got, _ := low.Check("USDJPY", time.Date(2026, 9, 20, 23, 0, 0, 0, time.UTC)); got == Blocked {
		t.Fatal("un jour férié n'est pas une annonce")
	}
}

func TestNilGateSaysUncovered(t *testing.T) {
	var g *Gate
	if v, _ := g.Check("EURUSD", time.Now()); v != Uncovered {
		t.Fatal("sans calendrier, le verdict est « non couvert », jamais « rien à signaler »")
	}
}

func TestServiceRefreshArchivesAndPublishes(t *testing.T) {
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()

	dir := t.TempDir()
	s, err := NewService(Options{Enabled: true, Source: "forexfactory", MinImpact: ImpactHigh,
		Before: 30 * time.Minute, After: 30 * time.Minute, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.source = &forexFactory{url: srv.URL, client: srv.Client()}
	if s.Gate().Calendar.Weeks() != 0 {
		t.Fatal("archive vide attendue au départ")
	}
	n, err := s.Refresh(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("une semaine attendue, reçu %d (%v)", n, err)
	}
	if st := s.Status(); st.Weeks != 1 || st.Events != 9 || st.LastError != "" {
		t.Fatalf("état inattendu : %+v", st)
	}
	// Relue par un service NEUF : l'archive survit au redémarrage.
	again, _ := NewService(Options{Enabled: true, Source: "none", MinImpact: ImpactHigh, Dir: dir}, nil)
	if again.Gate().Calendar.Weeks() != 1 {
		t.Fatal("l'archive doit être relue au démarrage")
	}
}

func TestServiceRefreshFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "trop de requêtes", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	s, _ := NewService(Options{Enabled: true, Source: "forexfactory", Dir: t.TempDir()}, nil)
	s.source = &forexFactory{url: srv.URL, client: srv.Client()}
	if _, err := s.Refresh(context.Background()); err == nil {
		t.Fatal("un 429 est une erreur")
	}
	if st := s.Status(); st.LastError == "" || st.Weeks != 0 {
		t.Fatalf("l'échec doit être visible et ne rien déclarer couvert : %+v", st)
	}
}

func TestDisabledServiceHasNoGate(t *testing.T) {
	s, err := NewService(Options{Enabled: false, Source: "forexfactory", Dir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Gate() != nil {
		t.Fatal("filtre désactivé : aucun Gate")
	}
	if _, err := NewService(Options{Source: "inconnue"}, nil); err == nil {
		t.Fatal("une source inconnue doit faire refuser le démarrage")
	}
}

func TestArchiveIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/notes.json", []byte(`[1,2,3]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cal, err := NewArchive(dir).Load()
	if err != nil || cal.Weeks() != 0 {
		t.Fatalf("un fichier étranger n'est pas une semaine : %v, %d", err, cal.Weeks())
	}
}

func TestImportDeclaresOnlyWeeksItContains(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewService(Options{Enabled: true, Source: "none", MinImpact: ImpactHigh, Dir: dir}, nil)
	file := t.TempDir() + "/import.json"
	if err := os.WriteFile(file, []byte(`[
		{"title":"CPI m/m","country":"USD","date":"2024-03-12T08:30:00-04:00","impact":"High"},
		{"title":"FOMC","country":"USD","date":"2024-03-20T14:00:00-04:00","impact":"High"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	weeks, events, err := s.Import(file)
	if err != nil || weeks != 2 || events != 2 {
		t.Fatalf("2 semaines et 2 annonces attendues : %d %d %v", weeks, events, err)
	}
	g := s.Gate()
	if !g.Calendar.Covers(time.Date(2024, 3, 13, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("la semaine du CPI est couverte")
	}
	if g.Calendar.Covers(time.Date(2024, 3, 27, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("une semaine sans annonce dans le fichier n'est PAS couverte")
	}
}
