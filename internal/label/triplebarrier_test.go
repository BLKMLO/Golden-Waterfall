package label

import (
	"math"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// bars construit une série horaire à partir de quadruplets OHLC.
func bars(ohlc [][4]float64) core.Series {
	start := time.Date(2022, 1, 3, 0, 0, 0, 0, time.UTC)
	out := make(core.Series, len(ohlc))
	for i, v := range ohlc {
		out[i] = core.Bar{
			Time:     start.Add(time.Duration(i) * time.Hour),
			BidOpen:  v[0],
			BidHigh:  v[1],
			BidLow:   v[2],
			BidClose: v[3],
			Volume:   1,
		}
	}
	return out
}

// flat génère assez de bougies pour que l'ATR de Wilder (14) soit défini,
// avec une amplitude connue.
func flat(n int, price, amplitude float64) [][4]float64 {
	out := make([][4]float64, n)
	for i := range out {
		out[i] = [4]float64{price, price + amplitude, price - amplitude, price}
	}
	return out
}

func TestUpperBarrierFirstGivesLabelOne(t *testing.T) {
	rows := flat(40, 100, 1) // ATR ≈ 2 → barrières à ±3 autour du close
	// La bougie suivante monte franchement au-dessus de la barrière haute.
	rows = append(rows, [4]float64{100, 120, 99.5, 119})
	rows = append(rows, flat(140, 119, 1)...) // > 5 jours horaires après l'événement
	series := bars(rows)

	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	i := 39
	if !res.Defined(i) {
		t.Fatalf("la bougie %d doit être étiquetable (ATR défini, fenêtre complète)", i)
	}
	if res.Value[i] != 1 || res.Which[i] != BarrierTP {
		t.Fatalf("barrière haute touchée en premier → label 1/tp, reçu %v/%v", res.Value[i], res.Which[i])
	}
}

func TestLowerBarrierFirstGivesLabelZero(t *testing.T) {
	rows := flat(40, 100, 1)
	rows = append(rows, [4]float64{100, 100.5, 80, 81})
	rows = append(rows, flat(140, 81, 1)...)
	series := bars(rows)

	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	i := 39
	if res.Value[i] != 0 || res.Which[i] != BarrierSL {
		t.Fatalf("barrière basse touchée en premier → label 0/sl, reçu %v/%v", res.Value[i], res.Which[i])
	}
}

// TestBothBarriersInSameBarPrefersStop verrouille la convention la plus
// importante du labeling : quand une bougie franchit les DEUX barrières,
// on retient la basse. Toute autre règle apprendrait au modèle des gains
// que le moteur d'exécution ne délivre jamais.
func TestBothBarriersInSameBarPrefersStop(t *testing.T) {
	rows := flat(40, 100, 1)
	rows = append(rows, [4]float64{100, 130, 70, 128}) // touche haut ET bas
	rows = append(rows, flat(140, 128, 1)...)
	series := bars(rows)

	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	i := 39
	if res.Value[i] != 0 || res.Which[i] != BarrierSL {
		t.Fatalf("les deux barrières dans la même bougie → stop (0/sl), reçu %v/%v",
			res.Value[i], res.Which[i])
	}
}

// TestIncompleteForwardWindowIsUnlabeled : une bougie dont l'horizon
// dépasse la fin de la série ne doit PAS recevoir de label. Sans cette
// règle, la queue de chaque bloc serait étiquetée sur quelques minutes au
// lieu de plusieurs jours — du bruit appris comme un signal.
func TestIncompleteForwardWindowIsUnlabeled(t *testing.T) {
	// 100 bougies horaires = ~4 jours : l'horizon de 5 jours ne tient
	// dans la série pour AUCUNE bougie.
	series := bars(flat(100, 100, 1))
	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	for i := range series {
		if res.Defined(i) {
			t.Fatalf("bougie %d étiquetée alors que son horizon dépasse la série", i)
		}
	}

	// Avec 300 bougies (12,5 jours), les premières deviennent étiquetables
	// et les DERNIÈRES doivent rester sans label.
	long := bars(flat(300, 100, 1))
	res = TripleBarrier(long, BarrierATRMult, MaxHoldDays)
	if !res.Defined(50) {
		t.Fatal("une bougie avec 5 jours de marge devant elle doit être étiquetable")
	}
	if res.Defined(len(long) - 1) {
		t.Fatal("la dernière bougie ne peut pas être étiquetée : aucune bougie après elle")
	}
}

func TestTimeBarrierUsesReturnSign(t *testing.T) {
	// Marché plat : aucune barrière n'est touchée, l'horizon décide.
	series := bars(flat(400, 100, 0.2))
	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	i := 20
	if !res.Defined(i) {
		t.Fatalf("bougie %d non étiquetée", i)
	}
	if res.Which[i] != BarrierTime {
		t.Fatalf("sur un marché plat, l'horizon doit trancher, reçu %v", res.Which[i])
	}
	// close[end] == close[i] → retour nul → label 1 (>= 0, par convention).
	if res.Value[i] != 1 {
		t.Fatalf("retour nul à l'horizon → label 1 par convention, reçu %v", res.Value[i])
	}
}

func TestATRUndefinedGivesNoLabel(t *testing.T) {
	series := bars(flat(5, 100, 1)) // trop court pour un ATR(14)
	res := TripleBarrier(series, BarrierATRMult, MaxHoldDays)
	for i := range series {
		if !math.IsNaN(res.Value[i]) {
			t.Fatalf("sans ATR, la bougie %d ne peut pas être étiquetée", i)
		}
	}
}

func TestLabelsAreBinary(t *testing.T) {
	series := bars(flat(500, 100, 0.5))
	res := Default(series)
	for i, v := range res.Value {
		if math.IsNaN(v) {
			continue
		}
		if v != 0 && v != 1 {
			t.Fatalf("label non binaire à l'indice %d : %v", i, v)
		}
	}
}
