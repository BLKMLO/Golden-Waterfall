package label

import (
	"math"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// constATR : ATR fixe, pour que les barrières soient connues d'avance.
func constATR(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func lastIndex(n int) []int {
	ends := make([]int, n)
	for i := range ends {
		ends[i] = n - 1
	}
	return ends
}

// TestSidedBothBarriersInSameBarIsALossForBothSides : le cas que le label
// symétrique donnait GAGNANT au short. Le moteur, lui, suppose le stop
// touché d'abord dans les deux sens.
func TestSidedBothBarriersInSameBarIsALossForBothSides(t *testing.T) {
	s := bars([][4]float64{
		{100, 100, 100, 100},
		{100, 102, 98, 100}, // ± 1,5 × 1 franchies toutes les deux
	})
	lab := Sided(s, constATR(2, 1), 1.5, lastIndex(2), nil)
	if lab.Long.Value[0] != 0 || lab.Short.Value[0] != 0 {
		t.Fatalf("stop prioritaire dans les deux sens : long %v, short %v",
			lab.Long.Value[0], lab.Short.Value[0])
	}
	if lab.Long.Which[0] != BarrierSL || lab.Short.Which[0] != BarrierSL {
		t.Fatal("les deux trades doivent sortir au stop")
	}
}

func TestSidedTakeProfitAndMirroredShort(t *testing.T) {
	s := bars([][4]float64{
		{100, 100, 100, 100},
		{100, 101.6, 99.9, 101.5}, // limite du long, stop du short
	})
	lab := Sided(s, constATR(2, 1), 1.5, lastIndex(2), nil)
	if lab.Long.Value[0] != 1 || lab.Long.GrossR[0] != 1 || lab.Long.Exit[0] != 1 {
		t.Fatalf("long : limite touchée attendue, %+v", lab.Long)
	}
	if lab.Short.Value[0] != 0 || lab.Short.GrossR[0] != -1 {
		t.Fatalf("short : stop touché attendu, R = %v", lab.Short.GrossR[0])
	}
}

// TestSidedGapStopIsWorseThanTheBarrier : un stop sauté par un gap est
// rempli à l'ouverture, comme au moteur. Le R mesuré le montre.
func TestSidedGapStopIsWorseThanTheBarrier(t *testing.T) {
	s := bars([][4]float64{
		{100, 100, 100, 100},
		{97, 97, 96, 96.5}, // ouvre sous le stop à 98,5
	})
	lab := Sided(s, constATR(2, 1), 1.5, lastIndex(2), nil)
	if want := (97.0 - 100) / 1.5; math.Abs(lab.Long.GrossR[0]-want) > 1e-12 {
		t.Fatalf("stop en gap : R %v, %v attendu", lab.Long.GrossR[0], want)
	}
}

// TestSidedCostTurnsASmallGainIntoALoss : la cible est NETTE. Un trade qui
// finit au temps avec un gain inférieur au spread est perdant.
func TestSidedCostTurnsASmallGainIntoALoss(t *testing.T) {
	s := bars([][4]float64{
		{100, 100, 100, 100},
		{100, 100.2, 99.9, 100.1},
	})
	gross := Sided(s, constATR(2, 1), 1.5, lastIndex(2), nil)
	if gross.Long.Value[0] != 1 {
		t.Fatal("sans coût, un gain au temps est gagnant")
	}
	net := Sided(s, constATR(2, 1), 1.5, lastIndex(2), []float64{0.2, 0.2})
	if net.Long.Value[0] != 0 {
		t.Fatal("un gain de 0,1 pour un spread de 0,2 est une PERTE nette")
	}
}

func TestSidedNoWindowMeansNoLabel(t *testing.T) {
	s := bars(flat(5, 100, 0.1))
	ends := []int{-1, 1, 4, 4, 4}
	lab := Sided(s, constATR(5, 1), 1.5, ends, nil)
	if lab.Long.Defined(0) || lab.Short.Defined(0) {
		t.Fatal("sans fenêtre d'exécution, aucune issue ne doit être inventée")
	}
	if lab.Long.Defined(1) {
		t.Fatal("une fenêtre qui se termine sur la bougie d'entrée n'a pas d'issue")
	}
	if !lab.Long.Defined(2) || lab.Long.Exit[2] != 4 {
		t.Fatalf("fenêtre complète attendue jusqu'à 4 : %+v", lab.Long)
	}
}

// TestExecutionWindowFollowsTheEngineRules : fin de semaine ISO, barrière
// verticale et pas d'entrée sur la dernière bougie de la semaine.
func TestExecutionWindowFollowsTheEngineRules(t *testing.T) {
	fri := time.Date(2024, 1, 5, 16, 0, 0, 0, time.UTC)
	s := core.Series{
		{Time: fri},                                   // 0
		{Time: fri.Add(4 * time.Hour)},                // 1 : dernière de la semaine
		{Time: fri.Add(56 * time.Hour)},               // 2 : lundi 00:00
		{Time: fri.Add(60 * time.Hour)},               // 3
		{Time: fri.Add(64 * time.Hour)},               // 4
		{Time: fri.Add(68 * time.Hour)},               // 5
		{Time: fri.Add(7 * 24 * time.Hour)},           // 6 : vendredi suivant 16:00
		{Time: fri.Add(7*24*time.Hour + 4*time.Hour)}, // 7
		{Time: fri.Add(10 * 24 * time.Hour)},          // 8 : lundi suivant
	}
	ends := ExecutionWindow(s, 4*time.Hour, 0)
	want := []int{1, -1, 7, 7, 7, 7, 7, -1, -1}
	for i := range want {
		if ends[i] != want[i] {
			t.Fatalf("sans horizon, bougie %d : fin %d, %d attendue (%v)", i, ends[i], want[i], ends)
		}
	}
	// Horizon de 8 heures depuis lundi 00:00 : la fenêtre (t, t+8 h]
	// contient la bougie qui COMMENCE à 08:00, comme au moteur
	// (core.HoldExpired) et à l'étiquetage symétrique.
	ends = ExecutionWindow(s, 4*time.Hour, 8*time.Hour)
	if ends[2] != 4 {
		t.Fatalf("horizon de 8 h depuis l'indice 2 : fin %d, 4 attendue", ends[2])
	}
}

func TestAverageUniqueness(t *testing.T) {
	// Trois labels : 0 couvre (0,2], 1 couvre (1,2], 2 n'est pas défini.
	u := AverageUniqueness([]int{2, 2, -1, -1})
	// Bougie 1 : un seul label actif (0) ; bougie 2 : deux labels (0 et 1).
	if math.Abs(u[0]-(1+0.5)/2) > 1e-12 {
		t.Fatalf("unicité du label 0 : %v, 0,75 attendue", u[0])
	}
	if math.Abs(u[1]-0.5) > 1e-12 {
		t.Fatalf("unicité du label 1 : %v, 0,5 attendue", u[1])
	}
	if !math.IsNaN(u[2]) {
		t.Fatal("un label indéfini n'a pas d'unicité")
	}
	// Un label isolé vaut 1.
	if u := AverageUniqueness([]int{3, -1, -1, -1}); u[0] != 1 {
		t.Fatalf("label isolé : %v, 1 attendu", u[0])
	}
}
