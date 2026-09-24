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

## `interactive_brokers` — TWS et IB Gateway

Parle le protocole TWS à un **TWS** ou un **IB Gateway** lancé sur la même
machine (ou joignable par le réseau). Forex **IDEALPRO** uniquement.

### Mise en place

1. Lancer TWS ou IB Gateway et s'y connecter — en **papier** pour
   commencer.
2. Dans TWS : *Configure → API → Settings* : cocher « Enable ActiveX and
   Socket Clients », **décocher « Read-Only API »**, noter le port.
3. Dans `config.yaml` (ou l'écran Paramètres) :

   ```yaml
   broker:
     name: "interactive_brokers"
     mode: "paper"        # "live" pour un compte réel
     host: "127.0.0.1"
     port: 7497           # TWS papier ; 7496 TWS réel ; 4002/4001 IB Gateway
     client_id: 1         # unique par programme connecté au même TWS
     account: ""          # requis si la session gère plusieurs comptes
   backtest:
     account_currency: "USD"   # DOIT être la devise du compte IB
   ```

4. Écran **Live**, `c`.

### Ce que la connexion vérifie avant de se dire connectée

| Vérification | Refus si… |
|---|---|
| Version du protocole | TWS plus ancien que 10.10 (protocole < 163) |
| Compte | plusieurs comptes gérés sans `broker.account`, ou compte absent |
| **Mode** | `paper` sur un compte réel (identifiant sans « D »), `live` sur un compte papier (« DU… ») |
| **Devise** | la valeur liquidative n'est pas dans `backtest.account_currency` |
| Données | identifiant d'ordre, comptes, résumé du compte ou positions non reçus en 15 s |

Le contrôle du mode est la seule preuve disponible côté API — l'identifiant
d'un compte papier IB commence par « D » — et il empêche les deux
confusions graves : croire tester sur un compte réel, ou afficher « LIVE —
ARGENT RÉEL » sur un compte fictif.

### Les ordres

**Entrée** : un **bracket**. Parent au marché (`DAY`), limite et stop
attachés par `parentId`, en `GTC`. Les trois partent avec `transmit=false`
sauf le dernier, qui transmet l'ensemble — transmis plus tôt, le parent
partirait seul. TWS lie les deux enfants en OCA. Les prix sont alignés sur
le demi-pip IDEALPRO (0,00005 ; 0,005 pour le yen).

**Sortie** (signal de la stratégie ou barrière verticale) : les barrières
attachées sont d'abord **annulées, et l'annulation confirmée**, puis l'ordre
au marché part. Sans cette attente, un stop orphelin rouvrirait une
position dès son déclenchement. Si la confirmation n'arrive pas en 10 s,
la sortie n'est **pas** envoyée : la position reste protégée, l'erreur est
journalisée, et la prochaine bougie retentera. Si une barrière s'est
exécutée pendant l'annulation, la position est déjà fermée : aucun ordre
ne part.

**Barrière perdue** : si IB annule une barrière sans que sa jumelle se
soit exécutée, et qu'une position reste ouverte cinq secondes plus tard, la
passerelle annule l'autre barrière et **ferme la position au marché**. Une
position « protégée » à l'écran et nue chez le courtier est exactement ce
que `SupportsBracket` promet d'empêcher.

**Redémarrage** : les identifiants des brackets sont écrits dans
`<données>/ib_brackets.json` AVANT l'envoi. Après un redémarrage (même
`client_id`, même compte), une sortie annule bien les barrières de la
séance précédente, et une barrière qui se déclenche est rapportée comme
une sortie. Le moteur ne journalise pas ce trade — il n'en a pas vu
l'entrée — et le dit.

### Les comptes rendus

- Une exécution est rapportée quand **toute** la quantité est exécutée
  (messages `execDetails`), avec le prix moyen et l'heure **d'IB**.
- Le **P&L d'une sortie est celui qu'IB rapporte** (`realizedPNL` des
  rapports de commission). S'il manque au bout de 3 s, ou s'il n'est pas
  dans la devise du compte, la sortie est rapportée **sans P&L** : le
  journal l'inscrit à zéro avec « P&L non rapporté par le broker », jamais
  avec un chiffre recalculé ici.
- Un refus (message d'erreur TWS, statut `Inactive`) est rapporté avec le
  texte d'IB.
- Le **P&L latent** des positions n'est pas dans le flux de positions
  d'IB : l'écran Live affiche « — ».

### Limites connues

- **Forex seulement.** Pour les métaux et les indices, la nature du
  contrat IB (CFD, CMDTY, future) change quantité, prix et marge ; une
  correspondance approximative enverrait un ordre faux. Ils sont refusés et
  le journal le dit.
- **Unités entières** : IB n'exécute pas de fraction d'unité ; la quantité
  est arrondie à l'unité inférieure.
- **Pas de reconnexion automatique.** Une coupure du socket est
  journalisée en erreur et l'écran passe « déconnecté » ; `c` relance.
  Une coupure entre TWS et IB (code 1100) rend `Connected()` faux jusqu'au
  rétablissement ; les données de marché sont redemandées au code 1101.
- **Valeur liquidative rafraîchie par IB toutes les trois minutes** : la
  limite de perte journalière s'appuie sur ce chiffre.
- Les positions **non forex** du compte ne sont pas vues par le risque.

### Comment le protocole a été écrit

Pas de mémoire, et pas de dépendance : le seul portage Go maintenu exige
Go 1.26 et tire protobuf. Chaque message a été écrit contre la source du
**client officiel IB (API 10.30, client Python)**, et
`testdata/ib/gen_golden.py` fait produire à ce client officiel les
messages de référence :

- les 39 requêtes que la passerelle envoie (versions de serveur 163, 176,
  187) doivent être identiques **octet pour octet** à celles du client
  officiel (`TestRequestsMatchOfficialClientByteForByte`) — ce test a
  trouvé un champ envoyé à tort en version 163 ;
- les messages entrants doivent donner les mêmes valeurs que le décodeur
  officiel (`TestDecoderAgreesWithOfficialDecoder`).

Un faux TWS (`ib_gateway_test.go`) éprouve ensuite la passerelle entière :
vérifications de connexion, flux, bracket, stop, sortie, refus, barrière
perdue, redémarrage.

⚠ **Ce qui n'a PAS été fait** : une séance contre un vrai TWS. Le bac à
sable de développement n'en a pas. Avant tout usage en `live`, faire
tourner la passerelle sur un compte **papier** : entrée, stop, limite,
sortie sur signal, et comparer le journal au relevé d'IB.

## Écrire une nouvelle passerelle

Six points de vigilance, tirés de ce qui a mal tourné ailleurs :

1. **Ne jamais se déclarer connecté par optimisme.** `Connected()` doit
   refléter l'état réel du socket, pas l'intention.
2. **Ne jamais fabriquer une donnée de compte.** Une erreur vaut mieux
   qu'une valeur par défaut : l'interface affiche des tirets.
3. **Rapporter les REFUS**, pas seulement les exécutions. Un ordre rejeté
   en silence bloque son symbole pour toujours (l'ordre en vol n'est
   jamais libéré).
4. **Déclarer `SupportsBracket` honnêtement.** Le moteur s'appuie dessus
   pour décider si une entrée protégée peut partir. Une passerelle qui
   accepte l'`OrderRequest` mais laisse tomber `StopLoss` / `TakeProfit`
   doit déclarer `false` : l'entête affiche alors « SANS BARRIÈRES » et
   les entrées sont refusées et comptées (`Stats.UnprotectedRefused`),
   plutôt que d'ouvrir des positions sans protection.
5. **Les trois statuts sont TERMINAUX** (`FILLED`, `CANCELLED`,
   `REJECTED`) : un ordre qui en reçoit un ne bougera plus.
6. **Ne jamais bloquer la boucle de flux.** Les callbacks sont appelés
   depuis les goroutines de la passerelle ; le bus d'événements est conçu
   pour ne jamais les faire attendre.
