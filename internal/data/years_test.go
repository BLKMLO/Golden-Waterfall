package data

import (
	"testing"
	"time"
)

func TestYearRangeNormalizesHumanInput(t *testing.T) {
	current := time.Now().UTC().Year()
	cases := []struct {
		name string
		in   YearRange
		want YearRange
	}{
		{"bornes inversées", YearRange{2021, 2019}, YearRange{2019, 2021}},
		{"avant le service", YearRange{1987, 2005}, YearRange{FirstYear, 2005}},
		{"dans le futur", YearRange{2019, current + 5}, YearRange{2019, current}},
		{"année seule", YearRange{2019, 2019}, YearRange{2019, 2019}},
	}
	for _, c := range cases {
		if got := c.in.Normalize(); got != c.want {
			t.Errorf("%s : %v normalisé en %v, %v attendu", c.name, c.in, got, c.want)
		}
	}
}

func TestYearRangeEnumerates(t *testing.T) {
	got := YearRange{2019, 2021}.Years()
	if len(got) != 3 || got[0] != 2019 || got[2] != 2021 {
		t.Fatalf("années énumérées : %v", got)
	}
	if n := len(YearRange{2019, 2019}.Years()); n != 1 {
		t.Fatalf("une année seule doit donner un élément, reçu %d", n)
	}
}

func TestYearRangeStringReadsNaturally(t *testing.T) {
	if got := (YearRange{2019, 2019}).String(); got != "2019" {
		t.Fatalf("année seule affichée %q", got)
	}
	if got := (YearRange{2019, 2021}).String(); got != "2019 → 2021" {
		t.Fatalf("intervalle affiché %q", got)
	}
}

func TestYearRangeKnowsWhenItIsComplete(t *testing.T) {
	full := FullRange(2010)
	if !full.Covers(full) {
		t.Fatal("l'intervalle complet se couvre lui-même")
	}
	if (YearRange{2019, 2020}).Covers(full) {
		t.Fatal("un intervalle partiel ne couvre pas l'historique complet")
	}
}
