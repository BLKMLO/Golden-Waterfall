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
│   │   ├── bus.go              Pub/sub qui ne bloque JAMAIS un producteur.
│   │   └── log.go              Journal fichier + tampon mémoire pour la TUI.
│   │
│   ├── indicator/              RSI, ATR, ADX, MACD, stochastique, Bollinger,
│   │                           OBV… tous STRICTEMENT causaux.
│   ├── feature/                Les 34 features de Colibri + la matrice dense.
│   ├── label/                  Triple barrière (la CIBLE d'apprentissage).
│   ├── ml/gbdt/                Gradient boosting histogramme, Go pur.
│   │
│   ├── data/
│   │   ├── instrument.go       Symboles, décimales, devises base/cotation.
│   │   ├── dukascopy.go        Téléchargeur bi5 (LZMA), backoff 429.
│   │   ├── store.go            Format .gwb + inventaire + relecture bornée.
│   │   └── timeframe.go        M1…MN1, planchers de bucket, agrégation.
│   │
│   ├── strategy/
│   │   ├── strategy.go         CONTRAT + registre plugin.
│   │   ├── colibri.go        Inférence (chauffe, décision, barrières).
│   │   └── colibri_train.go  Entraînement, artefacts, AUC out-of-sample.
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
│   │   └── interactive_brokers.go  Ancrage : refuse au lieu de faire semblant.
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
- **clôture de fin de semaine ISO** et **liquidation finale** au close ;
  une bougie de clôture forcée ne rouvre rien ;
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
| Nouvelle stratégie | `strategy/<nom>.go` + `Register()` dans un `init()` | Moteurs, risque, brokers |
| Nouveau broker | `broker/<nom>.go` + `Register()` dans un `init()` | Stratégies, risque, TUI |
| Nouvel instrument | une ligne dans `data.Instruments` | Le reste de `data` |
| Nouvel indicateur | `indicator/` (causal, NaN pendant la chauffe) | `feature` si la définition d'un modèle publié change |
| Nouvel écran | `tui/view/<nom>.go` + une ligne dans `tui.New()` | Les autres écrans |
| Nouveau format de sortie | `export/<format>.go` | Ce qui a produit les chiffres |
| Nouveau réglage | `config.Config` + `Validate()` + `default_config.yaml` | Toute lecture directe d'env ailleurs — interdite |

⚠ **Ne jamais modifier une définition de modèle publiée** (features,
barrières, seuils). Toute évolution crée une **nouvelle version** de
stratégie, sinon les modèles archivés ne veulent plus rien dire.
