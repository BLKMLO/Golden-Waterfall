package core

import (
	"testing"
	"time"
)

// TestExpiredDesignatesTheLastBarOfTheWindow : la règle de la barrière
// verticale est appelée par les DEUX moteurs et par l'étiquetage. Elle doit désigner la même
// bougie que la fenêtre (t, t+horizon] de l'étiquetage.
func TestExpiredDesignatesTheLastBarOfTheWindow(t *testing.T) {
	entry := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	deadline := HoldDeadline(entry, 5*24*time.Hour)
	const h4 = 4 * time.Hour

	if want := entry.Add(5 * 24 * time.Hour); !deadline.Equal(want) {
		t.Fatalf("échéance %s, %s attendue", deadline, want)
	}
	// Une bougie qui se termine AVANT l'échéance : la fenêtre continue.
	if HoldExpired(deadline.Add(-2*h4), h4, deadline) {
		t.Fatal("une bougie entièrement dans la fenêtre ne doit pas expirer")
	}
	// La bougie qui CONTIENT l'échéance est la dernière de la fenêtre.
	if !HoldExpired(deadline.Add(-h4/2), h4, deadline) {
		t.Fatal("la bougie qui atteint l'échéance doit fermer la position")
	}
	if !HoldExpired(deadline, h4, deadline) {
		t.Fatal("la bougie qui commence à l'échéance doit fermer la position")
	}
}

func TestExpiredWithoutCadenceFallsBackToStrictOverrun(t *testing.T) {
	entry := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	deadline := HoldDeadline(entry, 5*24*time.Hour)
	if HoldExpired(deadline, 0, deadline) {
		t.Fatal("sans cadence connue, on attend un dépassement STRICT")
	}
	if !HoldExpired(deadline.Add(time.Minute), 0, deadline) {
		t.Fatal("après l'échéance, la position doit être fermée")
	}
}

// TestExpiredWithoutDeadlineNeverFires : une position sans échéance (aucune
// entrée) ne doit jamais déclencher de sortie.
func TestExpiredWithoutDeadlineNeverFires(t *testing.T) {
	if HoldExpired(time.Now(), 4*time.Hour, time.Time{}) {
		t.Fatal("sans échéance posée, rien ne doit expirer")
	}
}

func TestNoHorizonMeansNoDeadline(t *testing.T) {
	if !HoldDeadline(time.Now(), 0).IsZero() {
		t.Fatal("une stratégie sans horizon ne doit poser aucune échéance")
	}
}

func TestLastBarsOfWeekMarksTheBarBeforeTheWeekBreak(t *testing.T) {
	s := Series{
		{Time: time.Date(2024, 1, 5, 20, 0, 0, 0, time.UTC)}, // vendredi
		{Time: time.Date(2024, 1, 5, 21, 0, 0, 0, time.UTC)}, // dernière de la semaine
		{Time: time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)},  // lundi
		{Time: time.Date(2024, 1, 8, 1, 0, 0, 0, time.UTC)},  // dernière de la série
	}
	got := LastBarsOfWeek(s)
	want := []bool{false, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bougie %d : %v, %v attendu", i, got[i], want[i])
		}
	}
}

func TestMedianSpreadIgnoresMissingAsk(t *testing.T) {
	s := Series{
		{BidClose: 1, AskClose: 1.0002},
		{BidClose: 1}, // pas de côté ask
		{BidClose: 1, AskClose: 1.0004},
		{BidClose: 1, AskClose: 1.0010},
	}
	m, ok := s.MedianSpread()
	if !ok || m < 0.00039 || m > 0.00041 {
		t.Fatalf("médiane %v (%v), 0,0004 attendue", m, ok)
	}
	if _, ok := (Series{{BidClose: 1}}).MedianSpread(); ok {
		t.Fatal("sans côté ask, aucun spread ne doit être annoncé")
	}
}

func TestWeeklyCloseFollowsNewYorkDaylightSaving(t *testing.T) {
	cases := []struct {
		at   time.Time
		want time.Time
	}{
		// Hiver (heure normale de l'Est, UTC−5) : 17 h = 22 h UTC.
		{time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC), time.Date(2024, 1, 5, 22, 0, 0, 0, time.UTC)},
		// Été (UTC−4) : 17 h = 21 h UTC.
		{time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 7, 5, 21, 0, 0, 0, time.UTC)},
		// Pile sur la clôture : c'est elle.
		{time.Date(2024, 1, 5, 22, 0, 0, 0, time.UTC), time.Date(2024, 1, 5, 22, 0, 0, 0, time.UTC)},
		// Juste après : la semaine suivante.
		{time.Date(2024, 1, 5, 22, 0, 1, 0, time.UTC), time.Date(2024, 1, 12, 22, 0, 0, 0, time.UTC)},
		// Dimanche soir, réouverture : la clôture du vendredi qui vient.
		{time.Date(2024, 1, 7, 22, 30, 0, 0, time.UTC), time.Date(2024, 1, 12, 22, 0, 0, 0, time.UTC)},
		// Semaine du passage à l'heure d'été (dimanche 10 mars 2024).
		{time.Date(2024, 3, 8, 12, 0, 0, 0, time.UTC), time.Date(2024, 3, 8, 22, 0, 0, 0, time.UTC)},
		{time.Date(2024, 3, 11, 12, 0, 0, 0, time.UTC), time.Date(2024, 3, 15, 21, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := WeeklyClose(c.at); !got.Equal(c.want) {
			t.Errorf("WeeklyClose(%s) = %s, %s attendu", c.at, got.UTC(), c.want)
		}
	}
}

func TestLastBarBeforeWeekend(t *testing.T) {
	const h4 = 4 * time.Hour
	// Hiver : clôture vendredi 22 h UTC. La bougie H4 de 20 h la contient.
	if !LastBarBeforeWeekend(time.Date(2024, 1, 5, 20, 0, 0, 0, time.UTC), h4) {
		t.Fatal("la bougie H4 de 20 h UTC est la dernière de la semaine")
	}
	if LastBarBeforeWeekend(time.Date(2024, 1, 5, 16, 0, 0, 0, time.UTC), h4) {
		t.Fatal("la bougie H4 de 16 h UTC se clôt avant la garde : ce n'est pas la dernière")
	}
	// M1 : la garde de cinq minutes compte.
	if !LastBarBeforeWeekend(time.Date(2024, 1, 5, 21, 54, 0, 0, time.UTC), time.Minute) {
		t.Fatal("une bougie qui se clôt dans la garde est la dernière")
	}
	if LastBarBeforeWeekend(time.Date(2024, 1, 5, 21, 50, 0, 0, time.UTC), time.Minute) {
		t.Fatal("une bougie close avant la garde n'est pas la dernière")
	}
}
