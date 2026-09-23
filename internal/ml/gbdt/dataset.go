package gbdt

import (
	"fmt"
	"math"
	"sort"
)

// Dataset est un jeu d'apprentissage dense en ligne-majeur.
//
// X contient Rows*Cols valeurs ; NaN signifie « manquant » et est géré
// nativement (l'arbre apprend une direction par défaut). Y vaut 0 ou 1.
type Dataset struct {
	Rows  int
	Cols  int
	X     []float64
	Y     []float64
	Names []string
	// W : poids par ligne, optionnel (nil = toutes les lignes pèsent 1).
	// Posé par SetWeights, jamais deviné.
	W []float64
}

// SetWeights attache un poids par ligne.
//
// Les poids sont RENORMALISÉS à une moyenne de 1 : la régularisation L2
// et le seuil de masse de courbure (MinSumHessianInLeaf) sont exprimés en
// unités d'échantillon, et des poids de moyenne 0,1 les rendraient
// silencieusement dix fois plus forts. Seule la RÉPARTITION du poids entre
// les lignes change, pas l'échelle du problème.
func (d *Dataset) SetWeights(w []float64) error {
	if len(w) != d.Rows {
		return fmt.Errorf("%d poids pour %d lignes", len(w), d.Rows)
	}
	var sum float64
	for i, v := range w {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fmt.Errorf("poids invalide à la ligne %d : %g", i, v)
		}
		sum += v
	}
	if sum <= 0 {
		return fmt.Errorf("somme des poids nulle")
	}
	scale := float64(d.Rows) / sum
	d.W = make([]float64, d.Rows)
	for i, v := range w {
		d.W[i] = v * scale
	}
	return nil
}

// weightedPositiveRate : proportion PONDÉRÉE de positifs, point de départ
// du boosting quand des poids sont posés.
func (d *Dataset) weightedPositiveRate() float64 {
	if d.W == nil {
		return d.PositiveRate()
	}
	var pos, total float64
	for i, v := range d.Y {
		pos += v * d.W[i]
		total += d.W[i]
	}
	return pos / total
}

// NewDataset assemble un jeu et vérifie sa cohérence.
func NewDataset(x []float64, y []float64, names []string) (*Dataset, error) {
	cols := len(names)
	if cols == 0 {
		return nil, fmt.Errorf("jeu sans feature")
	}
	if len(x)%cols != 0 {
		return nil, fmt.Errorf("matrice incohérente : %d valeurs pour %d colonnes", len(x), cols)
	}
	rows := len(x) / cols
	if rows != len(y) {
		return nil, fmt.Errorf("%d lignes de features contre %d labels", rows, len(y))
	}
	if rows == 0 {
		return nil, fmt.Errorf("jeu vide")
	}
	for i, v := range y {
		if v != 0 && v != 1 {
			return nil, fmt.Errorf("label non binaire à la ligne %d : %g", i, v)
		}
	}
	return &Dataset{Rows: rows, Cols: cols, X: x, Y: y, Names: names}, nil
}

// Row renvoie une vue sur la ligne i.
func (d *Dataset) Row(i int) []float64 { return d.X[i*d.Cols : (i+1)*d.Cols] }

// PositiveRate est la proportion de labels à 1 : c'est le point de départ
// du boosting (le modèle « sans arbre » prédit déjà cette base).
func (d *Dataset) PositiveRate() float64 {
	var sum float64
	for _, v := range d.Y {
		sum += v
	}
	return sum / float64(d.Rows)
}

// --- Binning ---------------------------------------------------------------

// binMapper décrit, pour UNE feature, la conversion valeur → bin.
//
// Deux natures :
//   - numérique : bornes supérieures croissantes ; bin(v) = premier k tel
//     que v <= Upper[k]. Le bin des MANQUANTS est un indice dédié, placé
//     après les bins réels, jamais confondu avec un extrême ;
//   - catégorielle : table valeur → bin. Une catégorie jamais vue à
//     l'entraînement tombe dans le bin des manquants — honnête : le modèle
//     n'a rien appris sur elle et ne doit pas faire semblant.
type binMapper struct {
	Categorical bool
	Upper       []float64
	Categories  []float64
	catIndex    map[float64]uint8
	NumBins     int // bins réels, hors manquants
}

// missingBin renvoie l'indice réservé aux valeurs manquantes.
func (m *binMapper) missingBin() uint8 { return uint8(m.NumBins) }

// totalBins inclut le bin des manquants.
func (m *binMapper) totalBins() int { return m.NumBins + 1 }

func (m *binMapper) bin(v float64) uint8 {
	if math.IsNaN(v) {
		return m.missingBin()
	}
	if m.Categorical {
		if b, ok := m.catIndex[v]; ok {
			return b
		}
		return m.missingBin()
	}
	// Recherche dichotomique de la première borne >= v.
	idx := sort.SearchFloat64s(m.Upper, v)
	if idx >= m.NumBins {
		idx = m.NumBins - 1
	}
	return uint8(idx)
}

// buildNumericMapper construit des bins d'effectifs ÉQUILIBRÉS (quantiles)
// plutôt que de largeur égale : sur des retours financiers très
// leptokurtiques, des bins de largeur égale mettraient 99 % des points
// dans un seul bin et rendraient l'histogramme aveugle.
func buildNumericMapper(values []float64, maxBins int) *binMapper {
	finite := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			finite = append(finite, v)
		}
	}
	if len(finite) == 0 {
		// Feature entièrement manquante : un seul bin réel, aucune
		// coupure possible. L'arbre l'ignorera d'elle-même.
		return &binMapper{Upper: []float64{math.Inf(1)}, NumBins: 1}
	}
	sort.Float64s(finite)

	distinct := finite[:1]
	for _, v := range finite[1:] {
		if v != distinct[len(distinct)-1] {
			distinct = append(distinct, v)
		}
	}
	if len(distinct) <= maxBins {
		// Assez peu de valeurs distinctes : un bin par valeur, aucune
		// perte de résolution.
		upper := make([]float64, len(distinct))
		copy(upper, distinct)
		upper[len(upper)-1] = math.Inf(1)
		return &binMapper{Upper: upper, NumBins: len(upper)}
	}

	upper := make([]float64, 0, maxBins)
	step := float64(len(finite)) / float64(maxBins)
	for k := 1; k < maxBins; k++ {
		pos := int(float64(k) * step)
		if pos >= len(finite) {
			pos = len(finite) - 1
		}
		v := finite[pos]
		if len(upper) > 0 && v <= upper[len(upper)-1] {
			continue // quantile dupliqué (valeur très fréquente)
		}
		upper = append(upper, v)
	}
	upper = append(upper, math.Inf(1))
	return &binMapper{Upper: upper, NumBins: len(upper)}
}

func buildCategoricalMapper(values []float64, maxBins int) *binMapper {
	seen := make(map[float64]struct{})
	for _, v := range values {
		if !math.IsNaN(v) {
			seen[v] = struct{}{}
		}
	}
	cats := make([]float64, 0, len(seen))
	for v := range seen {
		cats = append(cats, v)
	}
	sort.Float64s(cats)
	if len(cats) > maxBins {
		// Plus de catégories que de bins : on garde les premières (ordre
		// stable) et le reste bascule dans le bin des manquants. Cas
		// théorique ici (une trentaine de paires au maximum), mais il vaut
		// mieux tronquer franchement que déborder d'un uint8.
		cats = cats[:maxBins]
	}
	index := make(map[float64]uint8, len(cats))
	for i, v := range cats {
		index[v] = uint8(i)
	}
	if len(cats) == 0 {
		return &binMapper{Categorical: true, Categories: nil, catIndex: index, NumBins: 1}
	}
	return &binMapper{Categorical: true, Categories: cats, catIndex: index, NumBins: len(cats)}
}

// binnedData est le jeu converti en bins, stocké en COLONNE-majeur : le
// calcul d'histogramme parcourt une feature à la fois, la disposition par
// colonne divise par plusieurs les défauts de cache.
type binnedData struct {
	rows    int
	cols    int
	bins    [][]uint8 // [feature][ligne]
	mappers []*binMapper
}

func buildBinned(d *Dataset, p Params, categorical map[int]bool) *binnedData {
	b := &binnedData{
		rows:    d.Rows,
		cols:    d.Cols,
		bins:    make([][]uint8, d.Cols),
		mappers: make([]*binMapper, d.Cols),
	}
	column := make([]float64, d.Rows)
	for f := 0; f < d.Cols; f++ {
		for i := 0; i < d.Rows; i++ {
			column[i] = d.X[i*d.Cols+f]
		}
		if categorical[f] {
			b.mappers[f] = buildCategoricalMapper(column, p.MaxBins)
		} else {
			b.mappers[f] = buildNumericMapper(column, p.MaxBins)
		}
		col := make([]uint8, d.Rows)
		for i := 0; i < d.Rows; i++ {
			col[i] = b.mappers[f].bin(column[i])
		}
		b.bins[f] = col
	}
	return b
}

// maxTotalBins est la largeur de ligne des histogrammes (toutes features
// partagent la même foulée, ce qui simplifie l'allocation).
func (b *binnedData) maxTotalBins() int {
	m := 1
	for _, mp := range b.mappers {
		if mp.totalBins() > m {
			m = mp.totalBins()
		}
	}
	return m
}
