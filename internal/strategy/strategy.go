// Package strategy définit le CONTRAT que toute stratégie doit respecter
// et le registre qui les rend interchangeables. Il ne contient AUCUNE
// implémentation : chaque génération de moteur vit dans son propre
// sous-paquet (`strategy/colibri`, puis le suivant), et le catalogue
// `internal/strategies` est le seul endroit qui les nomme.
//
// Le moteur ne connaît que ce contrat : il pousse des bougies CLOSES et
// récupère des signaux. Une stratégie ne parle jamais au broker ni à la
// base — c'est ce qui la rend testable et remplaçable.
//
// # Ce qu'une stratégie DÉCLARE au lieu que les moteurs le supposent
//
// Backtest, walk-forward, live, TUI et CLI ne connaissent d'une stratégie
// que sa Description : le contexte de chauffe qu'elle exige
// (ContextBars), l'horizon au-delà duquel ses positions doivent être
// liquidées (MaxHold), si elle porte ses positions pendant le week-end
// (HoldsOverWeekend) et si un signal opposé ferme la position
// (ExitOnReversal). Aucun d'eux n'importe le paquet d'une stratégie ;
// remplacer Colibri par la génération suivante ne touche donc ni aux
// moteurs ni à l'interface.
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
	"time"

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
	// ContextBars : bougies d'historique à fournir AVANT la première
	// bougie décidée, pour que les indicateurs (récursifs compris) soient
	// stabilisés. Le walk-forward, le backtest, la TUI et le live le lisent
	// ici ; aucun ne le code en dur.
	ContextBars int
	// MaxHold : barrière VERTICALE — durée au-delà de laquelle une
	// position ouverte sur un signal de cette stratégie est liquidée, en
	// backtest comme en live. 0 = aucune sortie forcée par le temps.
	MaxHold time.Duration
	// HoldsOverWeekend : la stratégie PORTE ses positions pendant la
	// fermeture du week-end. À false (Colibri), backtest et live ferment
	// toute position à la dernière bougie de la semaine et n'y ouvrent
	// rien ; à true (suivi de tendance), une tendance n'est pas coupée
	// chaque vendredi, et le risque de gap à la réouverture est ASSUMÉ —
	// le stop, ordre au marché, est alors servi à l'ouverture.
	HoldsOverWeekend bool
	// ExitOnReversal : un signal d'ENTRÉE de sens opposé à la position
	// ouverte la ferme (sans rouvrir sur la même bougie). Une stratégie
	// sans état ne sait pas dans quel sens le moteur est positionné :
	// sans cette déclaration, une tendance qui se retourne d'un coup
	// laisserait courir la position à contresens jusqu'au stop. À false
	// (Colibri), un signal opposé est ignoré tant que la position vit —
	// c'est la cible que ses révisions ont apprise.
	ExitOnReversal bool
	// UsesNews : la stratégie accepte le filtre d'actualités (paquet
	// news). Les MOTEURS l'appliquent aux entrées quand `news.enabled`
	// est vrai ; la stratégie ne va jamais elle-même sur internet. Colibri
	// ne le déclare pas : ses révisions ont appris une cible sans
	// actualités, et le filtre changerait ce qu'elles veulent dire.
	UsesNews bool
}

// ModelManifest : fichier que toute stratégie entraînable DOIT écrire dans
// chaque dossier de modèle. Le catalogue des entraînements reconnaît un
// modèle à sa présence, sans rien savoir des autres fichiers — qui
// appartiennent à la stratégie.
const ModelManifest = "metadata.json"

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
//
// PositiveRate et Rounds n'ont de sens que pour un classifieur à arbres
// (Colibri). Une stratégie qui n'en a pas les laisse à zéro, et ils
// disparaissent alors de run.json : un « taux de positifs : 0 » écrit
// pour un modèle sans étiquettes serait un chiffre inventé.
type TrainReport struct {
	ModelDir     string             `json:"model_dir"`
	Samples      int                `json:"samples"`
	Features     int                `json:"features"`
	PositiveRate float64            `json:"positive_rate,omitempty"`
	Rounds       int                `json:"rounds,omitempty"`
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

// ApplyReversal traduit, pour une stratégie qui déclare ExitOnReversal, un
// signal d'entrée OPPOSÉ à la position ouverte en sortie.
//
// `position` est la quantité signée détenue sur le symbole (positive =
// long, négative = short, 0 = rien). Backtest et live appellent CETTE
// fonction : une règle de sortie écrite deux fois finit par diverger.
func ApplyReversal(desc Description, sig core.Signal, position float64) core.Signal {
	if !desc.ExitOnReversal {
		return sig
	}
	if position > 0 && sig.Action == core.EnterShort || position < 0 && sig.Action == core.EnterLong {
		sig.Action = core.Exit
		sig.StopLoss, sig.TakeProfit = 0, 0
	}
	return sig
}
