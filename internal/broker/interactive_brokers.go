package broker

import (
	"context"
	"fmt"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// ibGateway est le POINT D'ANCRAGE de la passerelle Interactive Brokers.
//
// Elle est volontairement NON IMPLÉMENTÉE, et le dit franchement à chaque
// appel. C'est un choix, pas un oubli : la règle d'honnêteté du projet
// interdit une passerelle qui répondrait « connecté » sans l'être, ou qui
// renverrait un compte plausible sans courtier derrière. Tant que le
// protocole TWS n'est pas réellement parlé, mieux vaut une erreur nette
// qu'un écran rempli de chiffres inventés.
//
// Ce qu'il reste à écrire pour l'activer (cf. docs/brokers.md) :
//   - la poignée de main du socket TWS/IB Gateway et la négociation de
//     version ;
//   - reqAccountSummary → core.AccountState ;
//   - reqPositions → []core.Position ;
//   - placeOrder en BRACKET (parent marché + stop + limite liés en OCA),
//     pour que les barrières de la stratégie vivent CHEZ le courtier et
//     survivent à un arrêt du programme ;
//   - execDetails / orderStatus → core.ExecutionReport, sans quoi aucun
//     trade ne sera jamais journalisé.
type ibGateway struct {
	opts Options
}

func init() {
	Register(Info{
		Name:        "interactive_brokers",
		Label:       "Interactive Brokers",
		Description: "TWS / IB Gateway. Point d'ancrage présent, protocole non encore implémenté.",
		Simulated:   false,
		Requirements: "TWS ou IB Gateway lancé et connecté, API activée, port autorisé. " +
			"NON FONCTIONNEL à ce stade : la passerelle refuse la connexion plutôt que de la simuler.",
	}, func(opts Options) (Gateway, error) {
		return &ibGateway{opts: opts}, nil
	})
}

var errIBNotImplemented = fmt.Errorf(
	"passerelle Interactive Brokers non implémentée : le protocole TWS n'est pas encore parlé. " +
		"Utiliser la passerelle \"replay\" pour éprouver la chaîne complète, ou implémenter " +
		"broker/interactive_brokers.go (voir docs/brokers.md)")

func (g *ibGateway) Info() Info {
	for _, i := range List() {
		if i.Name == "interactive_brokers" {
			return i
		}
	}
	return Info{Name: "interactive_brokers"}
}

func (g *ibGateway) Connect(ctx context.Context) error { return errIBNotImplemented }
func (g *ibGateway) Disconnect() error                 { return nil }
func (g *ibGateway) Connected() bool                   { return false }

func (g *ibGateway) Account(ctx context.Context) (core.AccountState, error) {
	return core.AccountState{}, errIBNotImplemented
}

func (g *ibGateway) Positions(ctx context.Context) ([]core.Position, error) {
	return nil, errIBNotImplemented
}

func (g *ibGateway) PlaceOrder(ctx context.Context, req core.OrderRequest) (string, error) {
	return "", errIBNotImplemented
}

func (g *ibGateway) Subscribe(ctx context.Context, symbols []string) error {
	return errIBNotImplemented
}

func (g *ibGateway) OnTick(func(core.Tick))                 {}
func (g *ibGateway) OnExecution(func(core.ExecutionReport)) {}
