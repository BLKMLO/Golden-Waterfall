// Package strategy définit le CONTRAT que toute stratégie doit respecter,
// le registre qui les rend interchangeables, et les implémentations.
//
// Le moteur ne connaît que ce contrat : il pousse des bougies CLOSES et
// récupère des signaux. Une stratégie ne parle jamais au broker ni à la
// base — c'est ce qui la rend testable et remplaçable.
//
// # Pourquoi des bougies et non des ticks
//
// Passer des TICKS à la stratégie l'obligerait à embarquer son propre
// agrégateur tick→bougie. Deux conséquences fâcheuses : chaque stratégie
// dupliquerait cet agrégateur, et le backtest (qui part de bougies) ne
// suivrait pas exactement le même chemin que le live (qui part de ticks).
//
// Ici, l'agrégation est faite UNE fois par le moteur live (package live),
// et le contrat ne parle que de bougies closes. Backtest et live entrent
// donc par la MÊME porte, avec les mêmes données.
package strategy

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// Description : métadonnées affichées par l'interface.
type Description struct {
	Name    string
	Version string
	Summary string
	// Definition : paramètres FIGÉS du modèle (features, barrières,
	// seuils). Source unique de vérité — l'interface ne les code jamais
	// en dur, elle les lit ici.
	Definition map[string]any
}

// WarmupRequest : ce que le moteur fournit avant la première décision.
type WarmupRequest struct {
	Symbol string
	// Series : contexte de chauffe SUIVI du bloc à évaluer. La stratégie
	// peut la pré-traiter en entier (features vectorisées) : c'est sans
	// fuite, puisque les features sont causales.
	Series core.Series
	// Timeframe des bougies de Series.
	Timeframe data.Timeframe
	// ModelDir : dossier du modèle à charger. Vide = aucun modèle
	// disponible ; une stratégie ML doit alors rester MUETTE plutôt que
	// d'improviser des signaux.
	ModelDir string
}

// Strategy est le contrat commun.
type Strategy interface {
	// Describe renvoie les métadonnées de la stratégie.
	Describe() Description
	// Warmup prépare la stratégie (modèle, features, tampons).
	Warmup(ctx context.Context, req WarmupRequest) error
	// OnBar traite la bougie CLOSE d'indice i de `series` et renvoie un
	// signal. `series` est la même tranche qu'au Warmup en backtest, un
	// tampon glissant en live.
	OnBar(ctx context.Context, symbol string, series core.Series, i int) (core.Signal, error)
	// Shutdown libère les ressources.
	Shutdown() error
	// Ready indique si la stratégie peut décider (modèle chargé, chauffe
	// faite). Une stratégie non prête ne produit AUCUN signal, et
	// l'interface affiche pourquoi plutôt que de laisser croire au calme.
	Ready() (bool, string)
}

// TrainRequest : un pli d'entraînement.
type TrainRequest struct {
	// Datasets : une entrée par symbole. Une stratégie mono-actif n'en
	// reçoit qu'une ; une stratégie poolée les reçoit toutes.
	Datasets  map[string]core.Series
	Timeframe data.Timeframe
	// OutputDir : dossier où écrire les artefacts du modèle.
	OutputDir string
	// Seed : graine, archivée avec le run — l'entraînement est
	// reproductible à l'identique.
	Seed int64
	// Threads : parallélisme interne de l'apprentissage. 0 = tous les
	// cœurs. Le walk-forward le réduit quand il lance plusieurs plis en
	// parallèle, sinon les plis se disputent les mêmes cœurs et
	// l'ensemble va moins vite qu'en séquentiel.
	Threads int
	// Progress : appelée pendant l'entraînement (0..1 et libellé).
	Progress func(ratio float64, step string)
}

// TrainReport : ce que l'entraînement a produit. Tous les chiffres sont
// MESURÉS ; aucun n'est estimé.
type TrainReport struct {
	ModelDir     string             `json:"model_dir"`
	Samples      int                `json:"samples"`
	Features     int                `json:"features"`
	PositiveRate float64            `json:"positive_rate"`
	Rounds       int                `json:"rounds"`
	Metrics      map[string]float64 `json:"metrics"`
	Importance   map[string]int     `json:"importance,omitempty"`
	Symbols      []string           `json:"symbols"`
	Seed         int64              `json:"seed"`
}

// Trainable : stratégie entraînable. Une stratégie à règles ne
// l'implémente pas, et le walk-forward le détecte proprement.
type Trainable interface {
	Strategy
	Train(ctx context.Context, req TrainRequest) (*TrainReport, error)
	// ScoreOOS renvoie l'AUC du modèle chargé sur un bloc out-of-sample.
	// `series` = contexte + bloc ; `from` = premier indice du bloc évalué.
	// Le second retour est false quand la métrique n'est pas calculable
	// (pas de modèle, une seule classe) — l'interface affiche « — »
	// plutôt qu'un 0,5 qui ressemblerait à une mesure.
	ScoreOOS(symbol string, series core.Series, from int) (float64, bool)
}

// Pooled : stratégie qui s'entraîne sur PLUSIEURS actifs à la fois.
// Le walk-forward s'en sert pour savoir s'il doit lancer un entraînement
// par actif ou un seul entraînement mutualisé.
type Pooled interface {
	PoolsSymbols() bool
}

// --- Registre --------------------------------------------------------------

// Factory construit une stratégie neuve.
type Factory func() Strategy

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register déclare une stratégie. Appelée depuis un init() de fichier de
// stratégie ; un doublon de nom est une erreur de programmation et panique
// au démarrage — ce qu'on veut, plutôt qu'un modèle silencieusement
// remplacé par un autre.
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("stratégie %q déjà enregistrée", name))
	}
	registry[name] = f
}

// New instancie une stratégie par son nom.
func New(name string) (Strategy, error) {
	registryMu.RLock()
	f, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("stratégie inconnue %q. Disponibles : %v", name, List())
	}
	return f(), nil
}

// List renvoie les noms enregistrés, triés.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NoSignal est le signal neutre, renvoyé quand rien n'est décidé.
func NoSignal(symbol string) core.Signal {
	return core.Signal{Symbol: symbol, Action: core.Hold}
}
