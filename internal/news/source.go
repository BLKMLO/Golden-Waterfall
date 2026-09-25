package news

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Source récupère des annonces. Fetch renvoie les annonces ET les semaines
// qu'elle déclare couvrir en entier : une semaine couverte sans annonce
// importante veut dire « pas d'annonce », une semaine non couverte veut
// dire « on ne sait pas ».
type Source interface {
	Name() string
	Fetch(ctx context.Context) (Batch, error)
}

// Batch : le résultat d'une récupération ou d'un import.
type Batch struct {
	Events []Event
	// Weeks : débuts (dimanche 0 h, New York) des semaines couvertes.
	Weeks []time.Time
	// Origin : d'où viennent ces annonces (source, fichier).
	Origin string
}

// Factory construit une source.
type Factory func() Source

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register déclare une source ; un doublon est une erreur de programmation.
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("source d'actualités %q déjà enregistrée", name))
	}
	registry[name] = f
}

// New instancie une source par son nom.
func New(name string) (Source, error) {
	registryMu.RLock()
	f, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source d'actualités inconnue %q. Disponibles : %v", name, List())
	}
	return f(), nil
}

// List renvoie les sources enregistrées, triées.
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

// noneSource : aucune récupération. Le filtre ne travaille alors que sur
// l'archive, alimentée par `gw news import`.
type noneSource struct{}

func (noneSource) Name() string { return "none" }
func (noneSource) Fetch(context.Context) (Batch, error) {
	return Batch{}, fmt.Errorf("source « none » : aucune récupération, l'archive seule est utilisée")
}

func init() { Register("none", func() Source { return noneSource{} }) }
