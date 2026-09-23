package gbdt

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
)

// histogram agrège gradient, hessienne et effectif par (feature, bin).
// Disposition à plat avec une foulée commune : une seule allocation par
// nœud, et la soustraction d'histogramme devient une boucle triviale.
type histogram struct {
	stride int
	g      []float64
	h      []float64
	c      []int32
}

func newHistogram(cols, stride int) *histogram {
	return &histogram{
		stride: stride,
		g:      make([]float64, cols*stride),
		h:      make([]float64, cols*stride),
		c:      make([]int32, cols*stride),
	}
}

func (hi *histogram) reset() {
	clear(hi.g)
	clear(hi.h)
	clear(hi.c)
}

// histPool recycle les histogrammes d'un entraînement.
//
// Un arbre de 31 feuilles en fabrique une soixantaine, et un entraînement
// en compte trois cents : sans recyclage, ce sont des milliers de blocs de
// plusieurs centaines de kilo-octets à allouer puis à ramasser. Le profil
// le montrait sans ambiguïté — près d'un dixième du temps passait dans le
// ramasse-miettes.
//
// La distinction get/getZeroed compte : l'histogramme du GRAND frère est
// entièrement réécrit par soustraction, le remettre à zéro d'abord serait
// du travail pur perdu.
type histPool struct {
	cols, stride int
	free         []*histogram
}

func newHistPool(cols, stride int) *histPool {
	return &histPool{cols: cols, stride: stride}
}

func (p *histPool) get() *histogram {
	if n := len(p.free); n > 0 {
		h := p.free[n-1]
		p.free = p.free[:n-1]
		return h
	}
	return newHistogram(p.cols, p.stride)
}

func (p *histPool) getZeroed() *histogram {
	h := p.get()
	h.reset()
	return h
}

func (p *histPool) put(h *histogram) {
	if h != nil {
		p.free = append(p.free, h)
	}
}

// subtract calcule hi = parent - sibling (astuce classique : le frère le
// plus gros ne se recalcule jamais, il se déduit).
func (hi *histogram) subtract(parent, sibling *histogram) {
	for i := range hi.g {
		hi.g[i] = parent.g[i] - sibling.g[i]
		hi.h[i] = parent.h[i] - sibling.h[i]
		hi.c[i] = parent.c[i] - sibling.c[i]
	}
}

// splitCandidate décrit la meilleure coupure trouvée pour un nœud.
type splitCandidate struct {
	valid       bool
	gain        float64
	feature     int
	threshold   float64
	defaultLeft bool
	categorical bool
	leftBins    []uint8 // bins partant à gauche (catégoriel)
	leftCats    []float64
	binLimit    int // numérique : dernier bin à gauche
}

// growNode est l'état d'une feuille en cours de croissance.
type growNode struct {
	indices []int32
	hist    *histogram
	sumG    float64
	sumH    float64
	count   int
	depth   int
	nodeID  int
	best    splitCandidate
}

// Train entraîne un modèle de classification binaire.
//
// `valid` est optionnel : fourni, il active l'arrêt anticipé sur la perte
// logistique de validation. Dans ce projet, cette validation est toujours
// la QUEUE du bloc d'entraînement (donc strictement dans le passé du bloc
// out-of-sample) — l'arrêt anticipé ne doit jamais voir l'avenir.
//
// `progress` est appelée après chaque tour ; renvoyer une erreur (ou
// annuler le contexte) interrompt proprement l'entraînement en conservant
// les arbres déjà construits.
func Train(ctx context.Context, train *Dataset, valid *Dataset, p Params,
	progress func(round int, trainLoss, validLoss float64)) (*Model, error) {

	if err := p.Validate(); err != nil {
		return nil, err
	}
	if train == nil || train.Rows == 0 {
		return nil, fmt.Errorf("jeu d'entraînement vide")
	}
	if valid != nil && valid.Cols != train.Cols {
		return nil, fmt.Errorf("validation à %d colonnes, entraînement à %d", valid.Cols, train.Cols)
	}

	categorical := make(map[int]bool, len(p.CategoricalFeatures))
	for _, f := range p.CategoricalFeatures {
		if f < 0 || f >= train.Cols {
			return nil, fmt.Errorf("feature catégorielle %d hors des %d colonnes", f, train.Cols)
		}
		categorical[f] = true
	}

	rate := train.weightedPositiveRate()
	// Une classe absente rend le problème dégénéré : autant le dire tout
	// de suite plutôt que de renvoyer un modèle constant déguisé.
	if rate <= 0 || rate >= 1 {
		return nil, fmt.Errorf("une seule classe dans le jeu d'entraînement (taux de positifs = %.3f)", rate)
	}
	baseScore := math.Log(rate / (1 - rate))

	bin := buildBinned(train, p, categorical)
	pool := newHistPool(bin.cols, bin.maxTotalBins())

	model := &Model{
		FormatVersion: ModelFormatVersion,
		Objective:     "binary",
		BaseScore:     baseScore,
		FeatureNames:  append([]string(nil), train.Names...),
		Categorical:   append([]int(nil), p.CategoricalFeatures...),
		Seed:          p.Seed,
		Params:        p,
		Metrics:       map[string]float64{},
	}

	scores := make([]float64, train.Rows)
	for i := range scores {
		scores[i] = baseScore
	}
	var validScores []float64
	if valid != nil {
		validScores = make([]float64, valid.Rows)
		for i := range validScores {
			validScores[i] = baseScore
		}
	}

	grad := make([]float64, train.Rows)
	hess := make([]float64, train.Rows)
	rng := rand.New(rand.NewSource(p.Seed))

	bestLoss := math.Inf(1)
	bestIter := 0
	sinceBest := 0

	for round := 1; round <= p.NumRounds; round++ {
		select {
		case <-ctx.Done():
			// Annulation : on rend ce qui a été construit, jamais rien de
			// fabriqué pour « finir ».
			model.BestIteration = pickBestIteration(bestIter, len(model.Trees))
			return model, ctx.Err()
		default:
		}

		for i := range scores {
			pr := sigmoid(scores[i])
			grad[i] = pr - train.Y[i]
			hess[i] = pr * (1 - pr)
			if hess[i] < 1e-16 {
				hess[i] = 1e-16
			}
			if train.W != nil {
				// Poids d'échantillon : gradient et courbure d'une ligne
				// comptent pour son poids, ni plus ni moins.
				grad[i] *= train.W[i]
				hess[i] *= train.W[i]
			}
		}

		rows := sampleRows(train.Rows, p, round, rng)
		feats := sampleFeatures(train.Cols, p.FeatureFraction, rng)
		tree := buildTree(bin, grad, hess, rows, feats, p, pool)
		model.Trees = append(model.Trees, tree)

		for i := 0; i < train.Rows; i++ {
			scores[i] += tree.predict(train.Row(i))
		}
		trainLoss := weightedLogLoss(scores, train)

		validLoss := math.NaN()
		if valid != nil {
			for i := 0; i < valid.Rows; i++ {
				validScores[i] += tree.predict(valid.Row(i))
			}
			validLoss = weightedLogLoss(validScores, valid)
			if validLoss < bestLoss-1e-9 {
				bestLoss, bestIter, sinceBest = validLoss, round, 0
			} else {
				sinceBest++
			}
		} else {
			bestIter = round
		}

		if progress != nil {
			progress(round, trainLoss, validLoss)
		}

		if valid != nil && p.EarlyStoppingRounds > 0 && sinceBest >= p.EarlyStoppingRounds {
			break
		}
	}

	model.BestIteration = pickBestIteration(bestIter, len(model.Trees))

	// Les métriques décrivent le modèle TEL QU'IL PRÉDIRA, c'est-à-dire
	// limité à BestIteration. Les calculer sur l'accumulateur `scores`
	// inclurait les arbres construits APRÈS le meilleur tour — ceux que
	// l'arrêt anticipé a précisément décidé d'écarter — et publierait donc
	// des chiffres qu'aucune prédiction ne reproduira jamais.
	trainScores := model.rawScores(train)
	model.Metrics["train_logloss"] = logLossFromScores(trainScores, train.Y)
	model.Metrics["train_auc"] = AUCFromScores(trainScores, train.Y)
	if valid != nil {
		validFinal := model.rawScores(valid)
		model.Metrics["valid_logloss"] = logLossFromScores(validFinal, valid.Y)
		model.Metrics["valid_auc"] = AUCFromScores(validFinal, valid.Y)
	}
	model.Metrics["rounds"] = float64(len(model.Trees))
	model.Metrics["best_iteration"] = float64(model.BestIteration)
	return model, nil
}

// rawScores évalue le modèle (limité à BestIteration) sur tout un jeu.
func (m *Model) rawScores(d *Dataset) []float64 {
	out := make([]float64, d.Rows)
	for i := 0; i < d.Rows; i++ {
		out[i] = m.RawScore(d.Row(i))
	}
	return out
}

func pickBestIteration(best, total int) int {
	if best <= 0 || best > total {
		return total
	}
	return best
}

func sampleRows(n int, p Params, round int, rng *rand.Rand) []int32 {
	all := p.BaggingFraction >= 1 || p.BaggingFreq <= 0 || round%p.BaggingFreq != 0
	if all {
		rows := make([]int32, n)
		for i := range rows {
			rows[i] = int32(i)
		}
		return rows
	}
	rows := make([]int32, 0, int(float64(n)*p.BaggingFraction)+1)
	for i := 0; i < n; i++ {
		if rng.Float64() < p.BaggingFraction {
			rows = append(rows, int32(i))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, int32(rng.Intn(n)))
	}
	return rows
}

func sampleFeatures(cols int, fraction float64, rng *rand.Rand) []int {
	if fraction >= 1 {
		feats := make([]int, cols)
		for i := range feats {
			feats[i] = i
		}
		return feats
	}
	want := int(math.Ceil(float64(cols) * fraction))
	if want < 1 {
		want = 1
	}
	perm := rng.Perm(cols)[:want]
	sort.Ints(perm)
	return perm
}

// buildTree fait croître UN arbre feuille par feuille : à chaque étape on
// coupe la feuille dont le gain est le plus élevé (et non la plus
// profonde). C'est ce qui donne, à nombre de feuilles égal, des arbres
// nettement plus expressifs qu'une croissance par niveaux.
func buildTree(bin *binnedData, grad, hess []float64, rows []int32, feats []int,
	p Params, pool *histPool) Tree {

	tree := Tree{Nodes: make([]Node, 0, 2*p.NumLeaves)}
	tree.Nodes = append(tree.Nodes, Node{Leaf: true})

	root := &growNode{indices: rows, depth: 0, nodeID: 0, count: len(rows)}
	root.hist = pool.getZeroed()
	buildHistogram(root.hist, bin, grad, hess, rows, feats, p.Threads)
	root.sumG, root.sumH = sumOf(grad, hess, rows)
	root.best = findBestSplit(root, bin, feats, p)
	tree.Nodes[0] = Node{Leaf: true, Value: leafValue(root.sumG, root.sumH, p), Count: root.count}

	leaves := []*growNode{root}
	// Les histogrammes des feuilles restantes retournent au recycleur dès
	// que l'arbre est terminé : sans cela, le recyclage ne servirait qu'à
	// l'intérieur d'un arbre et pas d'un arbre au suivant.
	defer func() {
		for _, leaf := range leaves {
			pool.put(leaf.hist)
			leaf.hist = nil
		}
	}()
	for len(leaves) < p.NumLeaves {
		// Choix de la feuille au meilleur gain.
		bestIdx, bestGain := -1, p.MinGainToSplit
		for i, leaf := range leaves {
			if leaf.best.valid && leaf.best.gain > bestGain {
				bestIdx, bestGain = i, leaf.best.gain
			}
		}
		if bestIdx < 0 {
			break
		}
		parent := leaves[bestIdx]
		// Partition EN PLACE : les deux enfants sont des tranches du
		// tableau du parent, qui n'est plus utilisé ensuite. Deux
		// allocations de moins par coupure, soit une soixantaine par arbre.
		cut := partitionInPlace(parent.indices, bin, parent.best)
		leftIdx, rightIdx := parent.indices[:cut], parent.indices[cut:]
		if len(leftIdx) < p.MinDataInLeaf || len(rightIdx) < p.MinDataInLeaf {
			// Garde-fou : la recherche de coupure travaille sur les
			// effectifs de l'histogramme ; si la partition réelle ne les
			// respecte pas, on invalide cette feuille plutôt que de créer
			// une feuille sous-peuplée.
			parent.best.valid = false
			continue
		}

		leftSumG, leftSumH := sumOf(grad, hess, leftIdx)
		rightSumG, rightSumH := parent.sumG-leftSumG, parent.sumH-leftSumH

		left := &growNode{indices: leftIdx, depth: parent.depth + 1, count: len(leftIdx),
			sumG: leftSumG, sumH: leftSumH}
		right := &growNode{indices: rightIdx, depth: parent.depth + 1, count: len(rightIdx),
			sumG: rightSumG, sumH: rightSumH}

		// Le petit enfant est calculé, le grand est DÉDUIT par
		// soustraction : deux fois moins de parcours de données.
		small, large := left, right
		if len(left.indices) > len(right.indices) {
			small, large = right, left
		}
		small.hist = pool.getZeroed()
		buildHistogram(small.hist, bin, grad, hess, small.indices, feats, p.Threads)
		// Le grand frère est entièrement réécrit par la soustraction : le
		// remettre à zéro d'abord serait du travail perdu.
		large.hist = pool.get()
		large.hist.subtract(parent.hist, small.hist)
		pool.put(parent.hist)
		parent.hist = nil // rendu au recycleur dès que les enfants l'ont consommé

		leftNode := len(tree.Nodes)
		tree.Nodes = append(tree.Nodes,
			Node{Leaf: true, Value: leafValue(left.sumG, left.sumH, p), Count: left.count},
			Node{Leaf: true, Value: leafValue(right.sumG, right.sumH, p), Count: right.count})
		left.nodeID, right.nodeID = leftNode, leftNode+1

		tree.Nodes[parent.nodeID] = splitNode(parent.best, leftNode, leftNode+1, parent.count)

		if p.MaxDepth > 0 && left.depth >= p.MaxDepth {
			left.best.valid, right.best.valid = false, false
		} else {
			left.best = findBestSplit(left, bin, feats, p)
			right.best = findBestSplit(right, bin, feats, p)
		}

		leaves[bestIdx] = left
		leaves = append(leaves, right)
	}
	return tree
}

func splitNode(s splitCandidate, left, right, count int) Node {
	return Node{
		Feature:     s.feature,
		Threshold:   s.threshold,
		DefaultLeft: s.defaultLeft,
		Categorical: s.categorical,
		LeftCats:    s.leftCats,
		Left:        left,
		Right:       right,
		Count:       count,
	}
}

func leafValue(sumG, sumH float64, p Params) float64 {
	return -sumG / (sumH + p.LambdaL2) * p.LearningRate
}

func sumOf(grad, hess []float64, idx []int32) (g, h float64) {
	for _, i := range idx {
		g += grad[i]
		h += hess[i]
	}
	return g, h
}

// buildHistogram remplit l'histogramme d'un nœud. Les features sont
// réparties entre goroutines : chacune écrit dans SA tranche de
// l'histogramme, aucun verrou n'est nécessaire.
// histParallelThreshold : en deçà de ce volume de travail (lignes ×
// features), la répartition entre goroutines coûte plus cher que le calcul
// lui-même.
//
// Ce n'est pas une intuition : le profil montrait un dixième du temps passé
// dans les verrous et le réveil de goroutines, parce que les nœuds profonds
// d'un arbre ne contiennent plus que quelques dizaines de lignes.
const histParallelThreshold = 1 << 15

// buildHistogram remplit l'histogramme d'un nœud. L'histogramme reçu doit
// être À ZÉRO : la remise à zéro appartient à l'appelant, qui seul sait si
// le tampon sort du recycleur ou vient d'être alloué.
func buildHistogram(hi *histogram, bin *binnedData, grad, hess []float64,
	idx []int32, feats []int, threads int) {

	work := func(part []int) {
		for _, f := range part {
			col := bin.bins[f]
			base := f * hi.stride
			for _, i := range idx {
				b := base + int(col[i])
				hi.g[b] += grad[i]
				hi.h[b] += hess[i]
				hi.c[b]++
			}
		}
	}
	if threads <= 1 || len(feats) < 2 || len(idx)*len(feats) < histParallelThreshold {
		work(feats)
		return
	}
	chunks := splitWork(feats, threads)
	var wg sync.WaitGroup
	for _, chunk := range chunks {
		wg.Add(1)
		go func(part []int) {
			defer wg.Done()
			work(part)
		}(chunk)
	}
	wg.Wait()
}

func splitWork(feats []int, threads int) [][]int {
	if threads > len(feats) {
		threads = len(feats)
	}
	out := make([][]int, 0, threads)
	size := (len(feats) + threads - 1) / threads
	for start := 0; start < len(feats); start += size {
		end := start + size
		if end > len(feats) {
			end = len(feats)
		}
		out = append(out, feats[start:end])
	}
	return out
}

// findBestSplit balaie toutes les features retenues et garde la meilleure
// coupure du nœud.
func findBestSplit(n *growNode, bin *binnedData, feats []int, p Params) splitCandidate {
	best := splitCandidate{}
	if n.count < 2*p.MinDataInLeaf {
		return best
	}
	parentGain := gainOf(n.sumG, n.sumH, p.LambdaL2)
	for _, f := range feats {
		var cand splitCandidate
		if bin.mappers[f].Categorical {
			cand = bestCategoricalSplit(n, bin, f, parentGain, p)
		} else {
			cand = bestNumericSplit(n, bin, f, parentGain, p)
		}
		if cand.valid && (!best.valid || cand.gain > best.gain) {
			best = cand
		}
	}
	return best
}

func gainOf(g, h, lambda float64) float64 { return g * g / (h + lambda) }

func bestNumericSplit(n *growNode, bin *binnedData, f int, parentGain float64, p Params) splitCandidate {
	m := bin.mappers[f]
	base := f * n.hist.stride
	miss := base + int(m.missingBin())
	missG, missH, missC := n.hist.g[miss], n.hist.h[miss], int(n.hist.c[miss])

	totalG, totalH, totalC := n.sumG, n.sumH, n.count
	nonMissC := totalC - missC

	best := splitCandidate{feature: f}
	var prefG, prefH float64
	var prefC int
	for b := 0; b < m.NumBins-1; b++ {
		prefG += n.hist.g[base+b]
		prefH += n.hist.h[base+b]
		prefC += int(n.hist.c[base+b])
		if prefC == 0 || prefC == nonMissC {
			continue // coupure qui ne sépare rien
		}
		// Deux variantes : les manquants à gauche, puis à droite. La
		// meilleure est APPRISE, elle n'est pas conventionnelle.
		for _, missLeft := range [2]bool{true, false} {
			lg, lh, lc := prefG, prefH, prefC
			if missLeft {
				lg, lh, lc = lg+missG, lh+missH, lc+missC
			}
			rg, rh, rc := totalG-lg, totalH-lh, totalC-lc
			if lc < p.MinDataInLeaf || rc < p.MinDataInLeaf {
				continue
			}
			if lh < p.MinSumHessianInLeaf || rh < p.MinSumHessianInLeaf {
				continue
			}
			gain := gainOf(lg, lh, p.LambdaL2) + gainOf(rg, rh, p.LambdaL2) - parentGain
			if gain <= p.MinGainToSplit || (best.valid && gain <= best.gain) {
				continue
			}
			best = splitCandidate{
				valid: true, gain: gain, feature: f,
				threshold: m.Upper[b], defaultLeft: missLeft, binLimit: b,
			}
		}
	}
	return best
}

// bestCategoricalSplit partitionne les catégories en deux ensembles.
//
// Méthode : trier les catégories par gradient moyen lissé puis balayer les
// préfixes. Chercher la meilleure partition arbitraire serait
// exponentiel ; ce tri donne l'optimum pour une perte convexe, et lisser
// par CatSmooth empêche une catégorie à 3 exemples de prendre la tête.
func bestCategoricalSplit(n *growNode, bin *binnedData, f int, parentGain float64, p Params) splitCandidate {
	m := bin.mappers[f]
	base := f * n.hist.stride
	miss := base + int(m.missingBin())
	missG, missH, missC := n.hist.g[miss], n.hist.h[miss], int(n.hist.c[miss])

	type catStat struct {
		bin   int
		g, h  float64
		count int
		key   float64
	}
	stats := make([]catStat, 0, m.NumBins)
	for b := 0; b < m.NumBins; b++ {
		c := int(n.hist.c[base+b])
		if c < p.MinDataPerGroup {
			continue // catégorie trop rare pour porter une décision
		}
		g, h := n.hist.g[base+b], n.hist.h[base+b]
		stats = append(stats, catStat{bin: b, g: g, h: h, count: c, key: g / (h + p.CatSmooth)})
	}
	if len(stats) < 2 {
		return splitCandidate{}
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].key < stats[j].key })

	totalG, totalH, totalC := n.sumG, n.sumH, n.count
	best := splitCandidate{feature: f, categorical: true}
	var prefG, prefH float64
	var prefC int
	for k := 0; k < len(stats)-1; k++ {
		prefG += stats[k].g
		prefH += stats[k].h
		prefC += stats[k].count
		for _, missLeft := range [2]bool{true, false} {
			lg, lh, lc := prefG, prefH, prefC
			if missLeft {
				lg, lh, lc = lg+missG, lh+missH, lc+missC
			}
			rg, rh, rc := totalG-lg, totalH-lh, totalC-lc
			if lc < p.MinDataInLeaf || rc < p.MinDataInLeaf {
				continue
			}
			if lh < p.MinSumHessianInLeaf || rh < p.MinSumHessianInLeaf {
				continue
			}
			gain := gainOf(lg, lh, p.LambdaL2) + gainOf(rg, rh, p.LambdaL2) - parentGain
			if gain <= p.MinGainToSplit || (best.valid && gain <= best.gain) {
				continue
			}
			leftBins := make([]uint8, 0, k+1)
			leftCats := make([]float64, 0, k+1)
			for j := 0; j <= k; j++ {
				leftBins = append(leftBins, uint8(stats[j].bin))
				leftCats = append(leftCats, m.Categories[stats[j].bin])
			}
			sort.Float64s(leftCats)
			best = splitCandidate{
				valid: true, gain: gain, feature: f, categorical: true,
				defaultLeft: missLeft, leftBins: leftBins, leftCats: leftCats,
			}
		}
	}
	return best
}

// partitionInPlace réordonne `idx` pour que les indices partant à GAUCHE
// occupent le préfixe, et renvoie la longueur de ce préfixe.
//
// Travaille sur les BINS (aucune relecture des valeurs réelles). L'ordre à
// l'intérieur d'un enfant n'a aucune importance : histogrammes et sommes
// sont commutatifs. Les deux enfants deviennent donc des tranches du
// tableau du parent, sans allocation.
//
// La décision d'appartenance est calculée par une table de 256 booléens
// plutôt que par une fermeture appelée à chaque ligne : sur des millions
// de lignes, l'appel indirect finit par se voir.
func partitionInPlace(idx []int32, bin *binnedData, s splitCandidate) int {
	col := bin.bins[s.feature]
	m := bin.mappers[s.feature]

	var goesLeft [256]bool
	if s.categorical {
		for _, b := range s.leftBins {
			goesLeft[b] = true
		}
	} else {
		for b := 0; b <= s.binLimit; b++ {
			goesLeft[b] = true
		}
	}
	goesLeft[m.missingBin()] = s.defaultLeft

	left := 0
	for i := 0; i < len(idx); i++ {
		if goesLeft[col[idx[i]]] {
			idx[left], idx[i] = idx[i], idx[left]
			left++
		}
	}
	return left
}
