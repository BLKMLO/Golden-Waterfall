# Architecture

> Document de référence : arborescence, rôle de chaque paquet, règles et
> conventions. À lire avant d'ajouter du code.

## Arborescence

```
Golden-Waterfall/
├── cmd/gw/main.go              Point d'entrée UNIQUE : TUI par défaut,
│                               sous-commandes pour l'usage non interactif.
│
├── internal/
│   ├── app/app.go              SEUL endroit où les modules sont câblés.
│   │                           Ordre : config (peut refuser de démarrer),
│   │                           journal, base, métier.
│   │
│   ├── config/
│   │   ├── config.go           Structure unique + validation fail-fast.
│   │   ├── paths.go            Emplacements par OS (XDG, AppData, macOS).
│   │   ├── timeframe.go        Liste des unités de temps (vérif croisée).
│   │   └── default_config.yaml Modèle commenté EMBARQUÉ dans le binaire.
│   │
│   ├── core/                   Briques transverses, sans dépendance métier.
│   │   ├── bar.go              Bar (bid+ask) et Series.
│   │   ├── domain.go           Signal, OrderRequest, Position, Trade,
│   │   │                       ExecutionReport, AccountState.
│   │   ├── horizon.go          Règles de sortie PARTAGÉES : barrière
│   │   │                       verticale, fin de semaine ISO, spread médian.
│   │   ├── bus.go              Pub/sub qui ne bloque JAMAIS un producteur.
│   │   └── log.go              Journal fichier + tampon mémoire pour la TUI.
│   │
│   ├── indicator/              RSI, ATR, ADX, MACD, stochastique, Bollinger,
│   │                           OBV… tous STRICTEMENT causaux.
│   ├── feature/                La matrice dense (générique, sans feature).
│   ├── label/                  Bibliothèque de CIBLES par barrières :
│   │                           symétrique (v1), par côté alignée sur
│   │                           l'exécution, unicité des labels.
│   ├── ml/gbdt/                Gradient boosting histogramme, Go pur :
│   │                           poids d'échantillon, calibrage par rétrécissement.
│   │
│   ├── data/
│   │   ├── instrument.go       Symboles, décimales, devises base/cotation.
│   │   ├── dukascopy.go        Téléchargeur bi5 (LZMA), backoff 429.
│   │   ├── store.go            Inventaire + relecture bornée, tous formats.
│   │   ├── parquet.go          Le stockage : lecture, écriture, import.
│   │   ├── gwb.go              L'ANCIEN format, en lecture seule.
│   │   ├── migrate.go          .gwb → Parquet, vérifié avant suppression.
│   │   └── timeframe.go        M1…MN1, planchers de bucket, agrégation.
│   │
│   ├── strategy/
│   │   ├── strategy.go         CONTRAT + registre. AUCUNE implémentation.
│   │   ├── strategytest/       Banc de CONFORMITÉ commun à toute stratégie.
│   │   ├── colibri/            Génération Colibri (classifieur GBDT) :
│   │   │   ├── revision.go     Définitions figées v1_0, v1_1, v1_2.
│   │   │   ├── features_v*.go  Jeux de features (v1 : 34, v2 : 33).
│   │   │   ├── strategy.go     Chauffe, décision (seuils ou espérance).
│   │   │   ├── train.go        Cible, purge, poids, entraînement, AUC OOS.
│   │   │   └── model.go        Manifeste, chargement vérifié colonne à colonne.
│   │   └── troglodyte/         Génération Troglodyte (tendance, Kalman) :
│   │       ├── kalman.go       Tendance locale linéaire, filtre, vraisemblance.
│   │       ├── mle.go          Maximum de vraisemblance (grille + Nelder-Mead).
│   │       ├── strategy.go     Révision v1_0, décision sur z, entraînement.
│   │       └── model.go        Manifeste = le modèle entier, chargement vérifié.
│   │
│   ├── strategies/             CATALOGUE : le seul paquet qui nomme une
│   │                           implémentation (importé par app).
│   │
│   ├── risk/manager.go         Le SEUL module qui transforme un signal en ordre.
│   │
│   ├── backtest/
│   │   ├── engine.go           Exécution bougie par bougie (voir plus bas).
│   │   ├── stats.go            Métriques + agrégats honnêtes.
│   │   └── json.go             Encodage des métriques non finies (NaN, ∞).
│   │
│   ├── training/
│   │   ├── walkforward.go      Découpe, plis parallèles, modèle de production.
│   │   └── catalog.go          Runs archivés, sélection du modèle live.
│   │
│   ├── broker/
│   │   ├── gateway.go          CONTRAT + registre.
│   │   ├── replay.go           Rejeu de l'historique local, compte SIMULÉ.
│   │   ├── interactive_brokers.go  TWS / IB Gateway : bracket, comptes rendus.
│   │   ├── ib_protocol.go      Messages TWS, écrits contre le client officiel.
│   │   └── ib_wire.go          Trames, champs, versions du protocole.
│   │
│   ├── live/
│   │   ├── aggregator.go       Ticks → bougies closes.
│   │   ├── engine.go           La chaîne de décision temps réel.
│   │   └── runtime.go          Assemblage, chauffe, photo d'état (Snapshot).
│   │
│   ├── storage/store.go        Base embarquée : trades, états, repères.
│   │
│   ├── export/csv.go           Sortie CSV. Ne calcule RIEN : recopie.
│   │
│   └── tui/
│       ├── tui.go              Routeur : onglets, entête, pied, aide.
│       ├── theme/              TOUTES les couleurs et styles du programme.
│       ├── component/          Formats, tableaux, panneaux, graphiques.
│       └── view/               Les six écrans.
│
└── docs/
```

## Les règles non négociables

### 1. Flux strict, aller ET retour

```
Feed → Strategy → Signal → risk.Manager → OrderRequest → BrokerGateway
                                                              │
         journal ← live.Engine ← ExecutionReport ←────────────┘
```

Imposé **à la fois** en backtest (`backtest/engine.go`) et en live
(`live/engine.go`). Aucune stratégie ne connaît le broker ; aucun ordre ne
contourne le risque.

Le chemin RETOUR est aussi important que l'aller : `PlaceOrder` ne fait que
SOUMETTRE. C'est la passerelle qui rapporte ce que le broker a fait, ce qui
libère l'ordre en vol et inscrit le trade au journal. **Un broker incapable
de rapporter ses exécutions n'alimente aucun trade** — le programme n'en
invente jamais pour combler le vide.

### 2. Câblage uniquement dans `app.New()`

Un module n'en instancie jamais un autre. Pour savoir de quoi dépend quoi,
un seul fichier suffit ; et remplacer une brique (base, passerelle,
stratégie) ne touche qu'à cet endroit.

### 2 bis. Une stratégie est un module remplaçable

Les moteurs (backtest, live), le walk-forward, la TUI et la CLI ne
connaissent d'une stratégie que le contrat `strategy.Strategy` et sa
`Description` : **aucun n'importe le paquet d'une stratégie**. Ce qu'ils
supposaient autrefois de Colibri est désormais DÉCLARÉ par la stratégie :

| Autrefois codé en dur | Désormais déclaré |
|---|---|
| `feature.ContextBars` (chauffe) | `Description.ContextBars` |
| `label.MaxHoldDays` (barrière verticale) | `Description.MaxHold` |
| `model.json` + `metadata.json` (catalogue) | `strategy.ModelManifest` seul |
| Clôture de toute position avant le week-end | `Description.HoldsOverWeekend` (v0.7.0) |
| Signal opposé ignoré tant que la position vit | `Description.ExitOnReversal` (v0.7.0) |

Les règles d'exécution que la stratégie a besoin de connaître pour
étiqueter sa cible (fin de semaine ISO, barrière verticale, spread médian)
vivent dans `core/horizon.go`, appelées à l'identique par le moteur et par
la stratégie. La règle de retournement vit dans `strategy.ApplyReversal`,
appelée par les deux moteurs.

**Fin de semaine en live** (v0.7.0). Le live ne voit la dernière bougie du
vendredi se clore qu'au premier tick du dimanche soir : la règle ISO du
backtest y arriverait trop tard. Il lui faut une heure :
`core.WeeklyClose`, vendredi 17 h à New York (heure d'été comprise, base
des fuseaux embarquée par `time/tzdata`). Pour une stratégie qui ne porte
pas le week-end, le moteur live demande la sortie au premier tick qui
tombe dans les **5 minutes** précédant cette clôture (`core.WeekendGuard`,
une convention), ou au premier tick qui suit si aucun n'est arrivé à
temps — en retard, et le journal le dit ; et il ne transmet aucune
entrée décidée sur la dernière bougie avant la clôture
(`core.LastBarBeforeWeekend`). L'heure est celle des ticks, jamais celle
de la machine : un rejeu ferme au vendredi rejoué. Kill-switch ou paire
désarmés : rien n'est fermé de force, un avertissement l'écrit une fois.

Remplacer Colibri par la génération suivante : écrire
`strategy/<oiseau>/`, ajouter une ligne dans `strategies/strategies.go`,
choisir le nom dans `strategy.name`. Le banc de conformité
(`strategytest`) tourne d'office sur toute stratégie du catalogue :
silence sans modèle, entraînement puis rechargement, manifeste présent,
barrières du bon côté, et **stabilité par préfixe des décisions**.

### 3. Le bus découple, sans jamais bloquer

`core.Bus` est un pub/sub en mémoire. Chaque abonnement est un canal
tamponné ; quand le tampon déborde, l'événement le plus ANCIEN est jeté
(le prix périmé n'intéresse personne) et la perte est COMPTÉE.

Pourquoi c'est vital : le producteur d'un tick est la boucle de flux de
prix d'un broker. La faire attendre un abonné lent, ou la tuer sur une
panique d'abonné, coûterait le flux de marché du symbole — en silence.

### 4. Configuration centralisée, validée, fail-fast

Tout passe par `config.Load()`. Aucune lecture directe de l'environnement
ou du YAML ailleurs. Une valeur absurde **fait échouer le démarrage** avec
un message qui nomme la clé fautive ; rien n'est corrigé en silence, et une
clé inconnue (faute de frappe) est une erreur, pas une option ignorée.

Priorité : défauts du code → `config.yaml` → variables `GW_*`.
L'environnement a le dernier mot : une variable qu'on prend la peine
d'exporter doit agir, et un fichier qui primerait sur elle la rendrait
inopérante en silence.

### 5. Paper → live = un seul réglage

`broker.mode` dans `config.yaml`. Rien d'autre.

### 5 ter. Une instance trade toutes les paires

Paper ou live, un seul processus suit toutes les paires de `live.symbols`
(vide = `history.instruments`) : un abonnement, un moteur, une instance de
stratégie. Il n'y a rien à lancer par symbole — et bbolt, qui verrouille
son fichier, refuserait de toute façon une seconde instance.

Ce sont les goroutines et les canaux qui rendent cela naturel : la
passerelle livre les ticks de toutes les paires depuis sa propre
goroutine, le moteur tient son état **par symbole** (tampon de bougies,
ordre en vol, jambe ouverte) sous un seul verrou, puis rediffuse ticks,
signaux et exécutions sur le bus sans jamais bloquer (§ 3).

Les paires décident indépendamment, mais partagent le compte : l'équité
qui dimensionne chaque entrée, `max_open_positions` (plafond global),
`max_daily_loss_pct` (plus aucune entrée nulle part une fois atteint) et
le kill switch. Aucune gestion de corrélation entre paires : le seul lien
est ce plafond commun.

### 5 bis. Le dimensionnement REFUSE plutôt que de deviner

`risk_per_trade_pct` (0,5 % par défaut) calcule la taille pour que la
distance jusqu'au stop coûte ce pourcentage de l'équité. Quand l'équité,
le stop ou la conversion de devise manquent, l'entrée est **refusée avec
son motif** — jamais repliée sur une taille arbitraire.

Deux clés distinctes, et elles ne veulent pas dire la même chose :
`max_position_size` est une GARDE (aucune entrée ne la dépasse, quel que
soit le mode), `fixed_position_size` est la taille employée quand le
risque par trade vaut 0. Les confondre — ce qu'elles faisaient — obligeait
à relever le plafond pour activer le risque par trade, ce qui décuplait la
taille fixe dès qu'on le désactivait.

Conséquence à connaître : sur un compte en dollars, une paire croisée
(EURGBP, AUDJPY…) a son P&L dans une devise tierce. Sans taux, il n'y a
pas de budget de risque, et **toutes** ses entrées sont refusées. Ce n'est
pas masqué : le démarrage l'écrit au journal, `gw config` compte les
instruments dimensionnables, le sélecteur de paires annote chacun, et les
écrans Backtest et Entraînement affichent les refus par motif.

### 6. L'interface ne ment jamais

Détaillée dans le README. En pratique, dans le code :
`component.Dash` (« — ») pour toute donnée indisponible ;
`Stats.CostsModelled`, `Stats.CurrencyExact`, `Stats.RejectedOrders`,
`Info.Simulated`, `SymbolState.Notice` existent tous pour que l'écran
puisse dire ce qu'il ne sait pas.

La règle vaut aussi pour la **place** : ce qui ne tient pas est ANNONCÉ,
jamais escamoté. Une colonne retirée d'un tableau se dit (`+N col.`), une
carte de statistique écartée se compte (`+N`), et un contenu plus haut que
la fenêtre est coupé avec le nombre de lignes masquées
(`component.Fit`). Sans ce dernier garde-fou, un écran trop haut ne perd
pas son bas : il pousse l'entête et la barre de raccourcis hors de
l'écran, donc le bandeau de mode et « q quitter ».

## Le modèle d'exécution du backtest

Chaque hypothèse est EXPLICITE (c'est la raison d'avoir écrit le moteur
plutôt que d'en embarquer un généraliste) :

- **entrée au CLOSE** de la bougie de décision ;
- **triple barrière** : stop et limite liés en OCO, remplis au **prix
  exact** de la barrière dès qu'une bougie SUIVANTE la franchit. Les deux
  franchies dans la même bougie → le **stop** l'emporte (l'ordre intrabar
  réel est inconnu, on se pénalise) — exactement la convention du
  labeling, si bien que le modèle apprend ce que l'exécution délivre ;
- **barrière verticale** : l'horizon que la stratégie DÉCLARE
  (`Description.MaxHold`) ; aucune si elle n'en déclare pas ;
- **clôture de fin de semaine ISO** au close, pour une stratégie qui ne
  déclare pas `HoldsOverWeekend` ; une bougie de clôture forcée ne rouvre
  rien, et **aucune entrée n'est ouverte sur la dernière bougie de la
  semaine** (elle serait portée tout le week-end — corrigé en v0.4.1).
  Une stratégie qui porte le week-end garde sa position ; un stop franchi
  par le gap du lundi est servi à l'ouverture ;
- **sortie sur signal** (v0.7.0) au close de la bougie de décision : un
  signal `Exit`, ou une entrée opposée pour une stratégie qui déclare
  `ExitOnReversal` (motifs `signal` et `reversal`). Avant v0.7.0, le
  backtest ignorait `Exit` alors que le live l'exécutait — sans effet tant
  qu'aucune stratégie n'en émettait (Colibri n'en émet pas) ;
- **liquidation finale** au close de la dernière bougie ;
- **coûts** : spread MESURÉ (médiane de `ask_close − bid_close`) facturé
  par côté, plus commission optionnelle. Un aller-retour paie exactement
  un spread. Sans côté ask : aucun coût, et `CostsModelled = false` ;
- **levier et devise** : une entrée immobilise `notionnel / levier`, le
  notionnel étant converti dans la devise du compte (cf. plus bas). Tout
  refus de marge est COMPTÉ ;
- **la limite de perte journalière n'est PAS modélisée** : elle exige
  l'équité réelle d'un broker. Le backtest est donc, sur ce seul point,
  légèrement optimiste. C'est assumé et documenté plutôt que simulé de
  travers.

### Devises

Le prix d'une paire `XXXYYY` s'exprime en `YYY` : le P&L et le notionnel y
naissent aussi. `data.ConversionFor` ramène le tout dans la devise du
compte :

| Cas | Exemple (compte USD) | Traitement |
|---|---|---|
| cotation = devise du compte | EURUSD | rien à faire, exact |
| base = devise du compte | USDJPY | notionnel = quantité ; P&L ÷ prix, exact |
| aucune des deux | EURGBP | **non convertible** sans taux tiers |

Le troisième cas n'est pas inventé : les montants restent en devise de
cotation, `CurrencyExact` vaut `false`, et l'interface le dit.

## Conventions

- **Un paquet = une responsabilité**, nommé par ce qu'il fait. Le nom de
  fichier n'a pas besoin de répéter le paquet (`risk/manager.go`, pas
  `risk/risk_manager.go`) : en Go, l'appel s'écrit déjà `risk.New`.
- **Commentaires en français**, identifiants en anglais (convention Go).
  Un commentaire dit POURQUOI, pas QUOI.
- **Tests en miroir** : `internal/<paquet>/<fichier>_test.go`.
- **Aucune couleur littérale hors de `tui/theme`.**
- **Un écran MESURE sa mise en page, il ne la devine pas.** Les hauteurs
  de panneau se composent de bordures, de titres et d'enroulements dont
  l'addition de tête se trompe d'une à cinq lignes.
  `component.FitBlock(budget, min, max, render)` réduit un bloc jusqu'à ce
  qu'il tienne ; un rendu de plus coûte moins cher qu'un affichage faux.
- **Aucun `panic` sur une donnée** : une donnée fautive est une erreur
  remontée. Les seuls `panic` tolérés signalent une incohérence de code
  (un nom de colonne absent de sa propre liste).

## Où ajouter du code sans rien casser

| Besoin | Où | Ne pas toucher |
|---|---|---|
| Nouvelle stratégie | `strategy/<oiseau>/` + `Register()` dans un `init()` + une ligne dans `strategies/` | Moteurs, walk-forward, TUI, CLI, risque, brokers |
| Nouvelle révision d'une stratégie | une nouvelle valeur `revision` dans son paquet | Les révisions publiées |
| Nouveau broker | `broker/<nom>.go` + `Register()` dans un `init()` | Stratégies, risque, TUI |
| Nouvel instrument | une ligne dans `data.Instruments` | Le reste de `data` |
| Nouvel indicateur | `indicator/` (causal, NaN pendant la chauffe) | Un jeu de features publié |
| Nouvel écran | `tui/view/<nom>.go` + une ligne dans `tui.New()` | Les autres écrans |
| Nouveau format de sortie | `export/<format>.go` | Ce qui a produit les chiffres |
| Nouveau réglage | `config.Config` + `Validate()` + `default_config.yaml` | Toute lecture directe d'env ailleurs — interdite |

⚠ **Ne jamais modifier une définition de modèle publiée** (features,
barrières, seuils). Toute évolution crée une **nouvelle révision** de
stratégie, sinon les modèles archivés ne veulent plus rien dire. La
refonte modulaire de v0.4.1 l'a vérifié : colibri_v1_0 et v1_1 produisent,
après déplacement de leur code, des modèles, signaux et AUC **identiques
au bit près** à ceux d'avant.
