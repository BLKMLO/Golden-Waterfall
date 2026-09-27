package feature

import (
	"math"
	"strings"
	"testing"
)

// TestNewMatrixIsFilledWithNaN : aucune valeur par défaut n'est légitime ;
// un zéro se confondrait avec une mesure.
func TestNewMatrixIsFilledWithNaN(t *testing.T) {
	m := NewMatrix(3, []string{"a", "b"})
	if m.Rows != 3 || m.Cols != 2 || len(m.Data) != 6 {
		t.Fatalf("dimensions : %+v", m)
	}
	for i, v := range m.Data {
		if !math.IsNaN(v) {
			t.Fatalf("case %d = %v, NaN attendu", i, v)
		}
	}
	if m.RowComplete(0) {
		t.Fatal("une ligne de NaN n'est pas complète")
	}
}

func TestNamesAreCopied(t *testing.T) {
	names := []string{"a", "b"}
	m := NewMatrix(1, names)
	names[0] = "z"
	if m.Names[0] != "a" {
		t.Fatal("la matrice doit posséder ses noms : les modifier dehors changerait ses colonnes")
	}
}

func TestSetColumnRowAndLookup(t *testing.T) {
	m := NewMatrix(3, []string{"a", "b"})
	m.SetColumn(0, []float64{1, 2, 3})
	m.SetColumn(1, []float64{10, 20}) // plus courte : la dernière case reste NaN
	if m.At(2, 0) != 3 || m.At(1, 1) != 20 || !math.IsNaN(m.At(2, 1)) {
		t.Fatalf("données : %v", m.Data)
	}
	if !m.RowComplete(0) || m.RowComplete(2) {
		t.Fatal("RowComplete")
	}
	row := m.Row(1)
	row[0] = 99 // une VUE, pas une copie
	if m.At(1, 0) != 99 {
		t.Fatal("Row doit être une vue sur la matrice")
	}
	col := m.Column(0)
	col[0] = -1 // une COPIE
	if m.At(0, 0) != 1 {
		t.Fatal("Column doit être une copie")
	}
	if i, err := m.ColumnIndex("b"); err != nil || i != 1 {
		t.Fatalf("ColumnIndex(b) = %d, %v", i, err)
	}
	if _, err := m.ColumnIndex("nawak"); err == nil || !strings.Contains(err.Error(), "nawak") {
		t.Fatalf("colonne absente : l'erreur doit la nommer (%v)", err)
	}
}

func TestAppendColumn(t *testing.T) {
	m := NewMatrix(2, []string{"a"})
	m.SetColumn(0, []float64{1, 2})
	out := m.AppendColumn("symbol", []float64{7})
	if out.Cols != 2 || out.Names[1] != "symbol" || out.At(0, 1) != 7 || !math.IsNaN(out.At(1, 1)) || out.At(1, 0) != 2 {
		t.Fatalf("AppendColumn : %+v", out)
	}
	if m.Cols != 1 {
		t.Fatal("l'original ne doit pas changer")
	}
}

func TestConcatRefusesMismatchedColumns(t *testing.T) {
	a := NewMatrix(2, []string{"x", "y"})
	b := NewMatrix(1, []string{"x", "y"})
	a.SetColumn(0, []float64{1, 2})
	b.SetColumn(0, []float64{3})
	out, err := Concat(a, nil, NewMatrix(0, []string{"x", "y"}), b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Rows != 3 || out.At(2, 0) != 3 {
		t.Fatalf("concaténation : %+v", out)
	}
	if _, err := Concat(a, NewMatrix(1, []string{"y", "x"})); err == nil {
		t.Fatal("même nombre de colonnes, ordre différent : doit être refusé")
	}
	if _, err := Concat(a, NewMatrix(1, []string{"x"})); err == nil {
		t.Fatal("nombre de colonnes différent : doit être refusé")
	}
	if _, err := Concat(); err == nil {
		t.Fatal("rien à concaténer : erreur attendue")
	}
}

func TestSelectRows(t *testing.T) {
	m := NewMatrix(3, []string{"a"})
	m.SetColumn(0, []float64{1, 2, 3})
	out := m.SelectRows([]int{2, 0})
	if out.Rows != 2 || out.At(0, 0) != 3 || out.At(1, 0) != 1 {
		t.Fatalf("SelectRows : %v", out.Data)
	}
}
