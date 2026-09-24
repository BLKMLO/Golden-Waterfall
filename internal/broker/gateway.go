// Package broker définit le CONTRAT générique d'une passerelle de courtage
// et le registre qui les rend interchangeables.
//
// Deux règles gouvernent tout ce paquet.
//
//  1. Le chemin RETOUR compte autant que l'aller. PlaceOrder ne fait que
//     SOUMETTRE ; c'est la gateway qui rapporte ce que le broker a
//     réellement fait, via le callback d'exécution. Un broker incapable de
//     rapporter ses exécutions n'alimente AUCUN trade — et le programme
//     n'en invente jamais pour combler le vide.
//
//  2. Une passerelle simulée le DÉCLARE (Info.Simulated). L'interface
//     affiche alors un bandeau explicite : l'utilisateur ne doit jamais
//     pouvoir confondre un rejeu avec un compte réel.
package broker

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Info décrit une passerelle pour l'interface.
type Info struct {
	Name        string
	Label       string
	Description string
	// Simulated : true si le compte, les positions et les prix sont
	// produits par le programme lui-même et non par un courtier.
	Simulated bool
	// SupportsBracket : la passerelle porte-t-elle le stop et la limite
	// CHEZ le courtier, liés à l'entrée ?
	//
	// Une stratégie propose ses barrières dans le Signal, le risque les
	// reporte sur l'OrderRequest, et le moteur les envoie. Si la
	// passerelle ignore ces deux champs, l'entrée part NUE : l'écran
	// affiche un stop qui n'existe nulle part, et une position censée
	// risquer 1,5 ATR risque tout le compte. Déclarer la capacité permet
	// au moteur de refuser l'entrée plutôt que de découvrir le problème
	// sur un relevé de courtier.
	SupportsBracket bool
	// Requirements : ce qu'il faut avoir installé/lancé à côté.
	Requirements string
}

// Gateway : contrat d'une passerelle de courtage.
type Gateway interface {
	Info() Info

	// Connect établit la connexion. Un échec doit être EXPLICITE : jamais
	// de « connecté » optimiste.
	Connect(ctx context.Context) error
	Disconnect() error
	Connected() bool

	// Account renvoie l'état RÉEL du compte. Une erreur vaut mieux qu'une
	// valeur par défaut : l'interface affiche des tirets, pas un zéro.
	Account(ctx context.Context) (core.AccountState, error)
	// Positions renvoie les positions telles que le broker les voit.
	Positions(ctx context.Context) ([]core.Position, error)

	// PlaceOrder SOUMET un ordre et renvoie son identifiant courtier.
	PlaceOrder(ctx context.Context, req core.OrderRequest) (string, error)

	// Subscribe demande le flux de prix des symboles.
	Subscribe(ctx context.Context, symbols []string) error

	// OnTick / OnExecution branchent les callbacks. Ils doivent être posés
	// AVANT Connect.
	OnTick(func(core.Tick))
	OnExecution(func(core.ExecutionReport))
}

// Factory construit une passerelle à partir de sa configuration.
type Factory func(Options) (Gateway, error)

// Options : tout ce dont une passerelle peut avoir besoin, sans qu'elle
// ait à connaître le paquet config (qui dépendrait alors d'elle).
type Options struct {
	Host string
	Port int
	Mode string // "paper" ou "live"
	// HistoryDir sert aux passerelles qui rejouent des données locales.
	HistoryDir string
	// InitialCapital : capital du compte simulé d'une passerelle de rejeu.
	InitialCapital float64
	// Speed : bougies par seconde d'un rejeu (0 = défaut).
	Speed float64
	// Leverage du compte simulé.
	Leverage float64
	// ClientID : identifiant de connexion auprès de TWS, unique par
	// connexion simultanée.
	ClientID int
	// Account : compte courtier à utiliser quand l'identifiant en gère
	// plusieurs. Vide = le seul compte géré, ou un refus s'il y en a
	// plusieurs.
	Account string
	// AccountCurrency : devise attendue du compte. Une passerelle réelle
	// refuse de démarrer sur un compte dans une autre devise : tout le
	// dimensionnement au risque en dépend.
	AccountCurrency string
	// StateDir : dossier où une passerelle garde ce qui doit survivre à un
	// redémarrage (identifiants des ordres attachés chez le courtier).
	StateDir string
	Logger   Logger
}

// Logger : minimum dont une gateway a besoin, sans importer log/slog
// partout.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
	infos      = map[string]Info{}
)

// Register déclare une passerelle.
func Register(info Info, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[info.Name]; exists {
		panic(fmt.Sprintf("passerelle %q déjà enregistrée", info.Name))
	}
	registry[info.Name] = f
	infos[info.Name] = info
}

// New instancie une passerelle par son nom.
func New(name string, opts Options) (Gateway, error) {
	registryMu.RLock()
	f, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("passerelle inconnue %q. Disponibles : %v", name, ListNames())
	}
	return f(opts)
}

// List renvoie les fiches de toutes les passerelles, triées par nom.
func List() []Info {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Info, 0, len(infos))
	for _, i := range infos {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListNames renvoie les noms enregistrés, triés.
func ListNames() []string {
	list := List()
	out := make([]string, len(list))
	for i, info := range list {
		out[i] = info.Name
	}
	return out
}

// Status : état de connexion publié sur le bus.
type Status struct {
	Gateway   string
	Connected bool
	Simulated bool
	Mode      string
	Message   string
}
