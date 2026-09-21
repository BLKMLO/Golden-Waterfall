package gbdt

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

// ModelFormatVersion protège contre le rechargement d'un modèle produit
// par une version incompatible : mieux vaut refuser que prédire de travers.
const ModelFormatVersion = 1

// Node est un nœud d'arbre. Un nœud est soit une feuille (Leaf, Value),
// soit une coupure (Feature + Threshold ou LeftCats).
type Node struct {
	Leaf  bool    `json:"leaf,omitempty"`
	Value float64 `json:"value,omitempty"`

	Feature int `json:"feature"`
	// Threshold (numérique) : on va à GAUCHE si x <= Threshold.
	Threshold float64 `json:"threshold,omitempty"`
	// DefaultLeft : direction d'une valeur MANQUANTE. Elle est APPRISE
	// (le côté qui maximise le gain), pas imposée — c'est ce qui permet
	// de traiter les NaN de chauffe sans les imputer.
	DefaultLeft bool `json:"default_left,omitempty"`
	// Categorical : on va à gauche si x appartient à LeftCats.
	Categorical bool      `json:"categorical,omitempty"`
	LeftCats    []float64 `json:"left_cats,omitempty"`

	Left  int `json:"left,omitempty"`
	Right int `json:"right,omitempty"`

	// Count : effectif d'entraînement passé par ce nœud (diagnostic).
	Count int `json:"count,omitempty"`
}

// Tree est un arbre stocké à plat ; la racine est le nœud 0.
type Tree struct {
	Nodes []Node `json:"nodes"`
}

// predict descend l'arbre pour une ligne de features.
func (t *Tree) predict(x []float64) float64 {
	id := 0
	for {
		n := &t.Nodes[id]
		if n.Leaf {
			return n.Value
		}
		var goLeft bool
		v := x[n.Feature]
		switch {
		case math.IsNaN(v):
			goLeft = n.DefaultLeft
		case n.Categorical:
			goLeft = containsFloat(n.LeftCats, v)
		default:
			goLeft = v <= n.Threshold
		}
		if goLeft {
			id = n.Left
		} else {
			id = n.Right
		}
	}
}

func containsFloat(set []float64, v float64) bool {
	// LeftCats est trié à la construction : dichotomie.
	i := sort.SearchFloat64s(set, v)
	return i < len(set) && set[i] == v
}

// Model est un ensemble d'arbres additifs sur l'échelle des LOG-ODDS.
type Model struct {
	FormatVersion int    `json:"format_version"`
	Objective     string `json:"objective"`
	// BaseScore : log-odds de la proportion de positifs du jeu
	// d'entraînement. C'est la prédiction « sans arbre ».
	BaseScore    float64  `json:"base_score"`
	FeatureNames []string `json:"feature_names"`
	// Categorical : indices des colonnes traitées comme catégories.
	Categorical []int  `json:"categorical,omitempty"`
	Trees       []Tree `json:"trees"`
	// BestIteration : nombre d'arbres réellement utilisés en prédiction.
	// Il peut être inférieur à len(Trees) quand l'arrêt anticipé a retenu
	// un tour antérieur — les arbres suivants sont conservés pour le
	// diagnostic mais JAMAIS évalués.
	BestIteration int   `json:"best_iteration"`
	Seed          int64 `json:"seed"`
	// Metrics : métriques d'entraînement (in-sample) et de validation.
	// Elles mesurent la MÉMORISATION, pas la compétence : seule l'AUC
	// out-of-sample du walk-forward dit quelque chose d'honnête.
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Params  Params             `json:"params"`
}

// NumFeatures renvoie le nombre de colonnes attendu.
func (m *Model) NumFeatures() int { return len(m.FeatureNames) }

// RawScore renvoie la somme des contributions (échelle log-odds).
func (m *Model) RawScore(x []float64) float64 {
	score := m.BaseScore
	limit := m.BestIteration
	if limit <= 0 || limit > len(m.Trees) {
		limit = len(m.Trees)
	}
	for i := 0; i < limit; i++ {
		score += m.Trees[i].predict(x)
	}
	return score
}

// Predict renvoie P(label = 1) pour une ligne.
func (m *Model) Predict(x []float64) float64 { return sigmoid(m.RawScore(x)) }

// PredictBatch applique le modèle à une matrice dense en ligne-majeur.
// Retourne une erreur si le nombre de colonnes ne correspond PAS à celui
// de l'entraînement : prédire avec des colonnes décalées produirait des
// probabilités crédibles et totalement fausses.
func (m *Model) PredictBatch(x []float64, cols int) ([]float64, error) {
	if cols != len(m.FeatureNames) {
		return nil, fmt.Errorf("modèle entraîné sur %d features, %d fournies",
			len(m.FeatureNames), cols)
	}
	if cols == 0 || len(x)%cols != 0 {
		return nil, fmt.Errorf("matrice incohérente : %d valeurs pour %d colonnes", len(x), cols)
	}
	rows := len(x) / cols
	out := make([]float64, rows)
	for i := 0; i < rows; i++ {
		out[i] = m.Predict(x[i*cols : (i+1)*cols])
	}
	return out, nil
}

// FeatureImportance compte les coupures par feature (gain cumulé).
// Utile pour repérer une feature qui domine — souvent le signe d'une fuite.
func (m *Model) FeatureImportance() map[string]int {
	out := make(map[string]int, len(m.FeatureNames))
	limit := m.BestIteration
	if limit <= 0 || limit > len(m.Trees) {
		limit = len(m.Trees)
	}
	for t := 0; t < limit; t++ {
		for _, n := range m.Trees[t].Nodes {
			if n.Leaf {
				continue
			}
			if n.Feature < len(m.FeatureNames) {
				out[m.FeatureNames[n.Feature]]++
			}
		}
	}
	return out
}

// Save écrit le modèle en JSON (lisible, diffable, stable dans le temps).
// Le volume reste modeste : 300 arbres de 31 feuilles ≈ 1 à 2 Mo.
func (m *Model) Save(path string) error {
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadModel relit un modèle et refuse un format inconnu.
func LoadModel(path string) (*Model, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("modèle illisible (%s) : %w", path, err)
	}
	if m.FormatVersion != ModelFormatVersion {
		return nil, fmt.Errorf("modèle %s au format v%d, attendu v%d — le réentraîner",
			path, m.FormatVersion, ModelFormatVersion)
	}
	if len(m.Trees) == 0 {
		return nil, fmt.Errorf("modèle %s sans arbre", path)
	}
	return &m, nil
}

func sigmoid(z float64) float64 {
	// Formulation stable : exp(+z) déborde pour z grand.
	if z >= 0 {
		return 1.0 / (1.0 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1.0 + e)
}
