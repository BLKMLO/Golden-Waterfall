# Passerelles de courtage

## Le contrat

```go
type Gateway interface {
    Info() Info
    Connect(ctx) error;  Disconnect() error;  Connected() bool
    Account(ctx) (core.AccountState, error)
    Positions(ctx) ([]core.Position, error)
    PlaceOrder(ctx, core.OrderRequest) (orderID string, err error)
    Subscribe(ctx, symbols []string) error
    OnTick(func(core.Tick))
    OnExecution(func(core.ExecutionReport))
}
```

Deux règles gouvernent toute implémentation.

**1. Le chemin retour compte autant que l'aller.** `PlaceOrder` ne fait que
SOUMETTRE. C'est `OnExecution` qui rapporte ce que le broker a réellement
fait : c'est lui qui libère l'ordre en vol côté moteur et qui inscrit le
trade au journal. **Une passerelle incapable de rapporter ses exécutions
n'alimente aucun trade** — et le programme n'en invente jamais pour
combler le vide.

**2. Une passerelle simulée le DÉCLARE** (`Info.Simulated`). L'interface
affiche alors un bandeau **REJEU — COMPTE SIMULÉ** en permanence.
L'utilisateur ne doit jamais pouvoir confondre un compte fictif avec un
compte réel.

Une passerelle s'enregistre dans un `init()` :

```go
func init() {
    Register(Info{Name: "mon_broker", Label: "…", Simulated: false}, factory)
}
```

Le registre la rend disponible partout — aucun autre fichier à modifier.

## `replay` — rejeu (simulation)

Rejoue l'historique M1 local comme un flux temps réel, sur un compte
simulé, à la vitesse `broker.replay_speed` (bougies par seconde).

À quoi elle sert vraiment : éprouver de bout en bout la chaîne
`flux → agrégation → stratégie → risque → ordre → compte rendu` sans
dépendre d'un terminal tiers ni risquer un centime. C'est aussi ce qui
permet de tester le moteur live en intégration continue.

Ce qu'elle fait honnêtement :

- exécute au dernier prix connu et **rapporte immédiatement** ;
- surveille stop et limite à chaque bougie et rapporte la sortie, comme le
  ferait un courtier dont les ordres attachés se déclenchent ;
- refuse — et le RAPPORTE — quand il n'y a pas de prix, quand une position
  est déjà ouverte, ou quand la marge manque ;
- horodate tout avec l'horloge du **marché rejoué**, jamais l'heure
  réelle. Un rejeu de 2020 qui daterait ses entrées d'aujourd'hui et ses
  sorties de 2020 produirait un journal incohérent et des durées de trade
  absurdes.

Ce qu'elle n'est pas : un courtier. Aucun chiffre qu'elle affiche ne doit
être lu comme une performance réelle.

## `interactive_brokers` — ancrage, non implémenté

La passerelle existe et **refuse explicitement** à chaque appel. C'est un
choix, pas un oubli : la règle d'honnêteté interdit une passerelle qui
répondrait « connecté » sans l'être, ou qui renverrait un compte plausible
sans courtier derrière. Tant que le protocole TWS n'est pas réellement
parlé, une erreur nette vaut mieux qu'un écran rempli de chiffres inventés.

### Ce qu'il reste à écrire

1. **Poignée de main** du socket TWS/IB Gateway et négociation de version.
2. **`reqAccountSummary`** → `core.AccountState` (équité, marge).
3. **`reqPositions`** → `[]core.Position`.
4. **`placeOrder` en BRACKET** : parent marché + stop + limite liés en OCA.
   Les barrières doivent vivre **chez le courtier**, pas dans le
   programme : elles survivent alors à un arrêt de Golden Waterfall.
5. **`execDetails` / `orderStatus`** → `core.ExecutionReport`. Sans cette
   étape, aucun trade ne sera jamais journalisé — c'est la plus importante.
6. **`reqMktData`** → `core.Tick`.

Quelques pièges connus du protocole : le `clientId` doit être unique par
connexion ; les identifiants d'ordre sont alloués par `reqIds` et ne
doivent jamais être devinés ; un ordre bracket doit être transmis avec
`transmit=false` sur le parent jusqu'au dernier enfant, sans quoi le
parent part seul.

## Écrire une nouvelle passerelle

Cinq points de vigilance, tirés de ce qui a mal tourné ailleurs :

1. **Ne jamais se déclarer connecté par optimisme.** `Connected()` doit
   refléter l'état réel du socket, pas l'intention.
2. **Ne jamais fabriquer une donnée de compte.** Une erreur vaut mieux
   qu'une valeur par défaut : l'interface affiche des tirets.
3. **Rapporter les REFUS**, pas seulement les exécutions. Un ordre rejeté
   en silence bloque son symbole pour toujours (l'ordre en vol n'est
   jamais libéré).
4. **Les trois statuts sont TERMINAUX** (`FILLED`, `CANCELLED`,
   `REJECTED`) : un ordre qui en reçoit un ne bougera plus.
5. **Ne jamais bloquer la boucle de flux.** Les callbacks sont appelés
   depuis les goroutines de la passerelle ; le bus d'événements est conçu
   pour ne jamais les faire attendre.
