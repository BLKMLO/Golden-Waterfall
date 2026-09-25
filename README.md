<div align="center">

# 🌊 Golden Waterfall

**Un expert advisor de trading algorithmique qui tient entièrement dans votre terminal.**

Télécharger l'historique, entraîner un modèle, le valider honnêtement, puis le
laisser trader — six écrans, un seul binaire, aucun navigateur, aucun serveur,
aucune dépendance à installer.

[Télécharger](https://github.com/BLKMLO/Golden-Waterfall/releases/latest) ·
[Démarrer](#démarrer) ·
[Documentation](#documentation)

</div>

---

```
   1      2      3      4      5      6                        REJEU — COMPTE SIMULÉ  compte USD  replay  kill-switch  colibri_v1_2
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Compte                                                                                                                           │
│ Équité        Marge         Perte du jour Ticks         Bougies       Ordres                                                     │
│ 10000.00      0.00          0.00 %        23 725        99            0                                                          │
│                             plafond 2.0 % dernier 20:2…               0 exécutés                                                 │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────╮╭────────────────────────────────────────────────────────────────╮
│ Paires suivies                                                 ││ AUDUSD · H1                                                    │
│  Paire           Bid     Var. État      Signal                 ││  ██  │││     ██                                                │
│ ▸AUDUSD      0.67473  +0.70 % armée     pas de modèle          ││ ██████████████████                                             │
│  EURUSD      1.11239  +0.03 % arrêtée   pas de modèle          ││ ██  │ │██ ││  ██│█                                             │
│  GBPUSD      1.28395  +0.47 % arrêtée   pas de modèle          ││                  ███████│ ││                                   │
│  USDJPY      153.045  -0.19 % arrêtée   pas de modèle          ││                   │││ │████████ │██                        █   │
│                                                                ││                            ██│█████                │    ████   │
│                                                                ││                               ││                 ███│████│     │
│                                                                ││                                                │██ ███│        │
│                                                                ││                                           │ █████              │
│                                                                ││                                        ││████ ██               │
│                                                                ││                                    █│█████ ││                  │
│                                                                ││ ⚠ aucun entraînement archivé pour la stratégie "colibri_v1_…   │
│                                                                ││ O 0.67304  H 0.67539  B 0.67304  C 0.67532  ·  110 bougies     │
╰────────────────────────────────────────────────────────────────╯╰────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Positions ouvertes (rapportées par la passerelle)                                                                                │
│  Paire    Sens     Quantité   Prix moyen   P&L latent                                                                            │
│   (aucune donnée)                                                                                                                │
│                                                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
AUDUSD  → bloquée : modèle  ✓ passerelle  ✓ barrières  ✓ kill-switch  ✓ paire armée  ✗ modèle  ✓ historique  ✓ devise USD
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 AUDUSD ARMÉE
tab écran  ·  ? aide  ·  q quitter  ·  c connecter / déconnecter  ·  k kill-switch global  ·  espace armer la paire  ·  ↑↓ sélection
```

<sub>Capture réelle du binaire : rejeu sur un historique synthétique, sans modèle entraîné — d'où « bloquée : modèle » sur la ligne de contrôle.</sub>

## Pourquoi

**Le chiffre qu'on vous montre est celui qui compte.** La validation se fait en
walk-forward : chaque pli s'entraîne sur tout ce qui précède son bloc de test
et n'est évalué que sur ce bloc. Rien n'est jamais testé sur des données vues à
l'entraînement, et l'écran affiche le résultat out-of-sample — avec, pour un
classifieur, l'AUC et son repère : **0,50 = hasard**.

**L'interface ne ment jamais.** Une donnée qu'on n'a pas s'affiche « — », pas
« 0 ». Une passerelle simulée porte un bandeau **REJEU** en permanence. Le
journal ne contient que des exécutions réellement rapportées par un courtier.
Chacune de ces garanties correspond à un test qui échouerait si elle cessait
d'être vraie : la liste complète est dans
[`docs/depannage.md`](docs/depannage.md).

**Un fichier, rien d'autre.** Pas de Python, pas de runtime, pas de
bibliothèque native, pas de base à installer. Le binaire se copie et se lance ;
les données vivent dans votre dossier utilisateur.

## Installation

Prenez l'archive de votre système dans la
[dernière version](https://github.com/BLKMLO/Golden-Waterfall/releases/latest)
— Linux (amd64/arm64), macOS (Intel/Apple Silicon), Windows — et extrayez le
fichier `gw`. Un `SHA256SUMS` accompagne les archives :

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

Ou compilez : il ne faut rien d'autre que Go 1.25.

```bash
go build -o gw ./cmd/gw
```

## Démarrer

`gw --help` commence par les **premiers pas** : les quatre étapes, dans l'ordre, avec la commande et l'écran de chacune.

**1. Lancez `./gw`.** Un `config.yaml` commenté est écrit au premier
démarrage ; `./gw paths` dit où. Tout se règle ensuite depuis l'écran
**6 Paramètres**.

**2. Téléchargez l'historique** — écran **2 Données**, `D` pour tout, `d` pour
la paire sélectionnée. La source est Dukascopy : des bougies M1 en **bid et en
ask**, et c'est le côté ask qui permet de *mesurer* le spread au lieu de
l'inventer. Comptez plusieurs heures pour trente instruments sur quinze ans —
ou réglez une période plus courte aux flèches pour essayer.

**3. Entraînez et validez** — écran **4 Entraînement**, `r`. C'est le seul
écran qui dise si la stratégie vaut quelque chose. Un modèle de **production**
est ensuite entraîné sur tout l'historique : c'est lui qui partira en live, et
les plis disent s'il le mérite.

**4. Tradez** — écran **1 Live** : `c` connecte, `k` arme le kill-switch
global, `espace` arme la paire. Par défaut la passerelle est un **rejeu
simulé** : toute la chaîne fonctionne, aucun argent n'est engagé. Pour un
vrai courtier, **Interactive Brokers** (TWS ou IB Gateway, forex) :
mise en place dans [docs/brokers.md](docs/brokers.md).

## Les six écrans

| | Ce qu'on y fait |
|---|---|
| **1 Live** | Compte, paires suivies, positions, graphique en chandeliers ; ligne de contrôle « pourquoi cette paire ne trade pas » |
| **2 Données** | Inventaire de l'historique local, téléchargement complet ou par période, conversion des anciens fichiers (`m`) |
| **3 Backtest** | Rejeu d'une paire, courbe d'équité, liste des trades défilable (`t`, `pgup`/`pgdn`), détail d'un trade (`entrée`), export CSV (`e`) |
| **4 Entraînement** | Walk-forward, choix des paires (`p`), plis, agrégat out-of-sample, runs archivés |
| **5 Journal** | Journal applicatif et journal des trades exécutés (défilable, détail par `entrée`), filtre texte (`/`), export CSV (`e`) |
| **6 Paramètres** | Compte et courtier, risque, stratégie, historique, interface |

`tab` change d'écran, `?` affiche l'aide complète, `q` quitte — et refuse tant
qu'un entraînement tourne.

La **devise du compte** reste affichée dans l'entête : avec le dimensionnement
au risque, seules les paires dont elle est la base ou la cotation peuvent
trader. Live, Backtest et le choix des paires ne proposent qu'elles par
défaut ; `v` montre toutes les paires. L'interface tient sans rien couper
dès 60×18.

## Les moteurs de décision

Le logiciel s'appelle **Golden Waterfall** ; ses moteurs de décision portent
des noms d'oiseaux, un par génération. Un moteur est un **module
remplaçable** : backtest, live, walk-forward et interface ne connaissent que
le contrat `strategy.Strategy` ([`docs/architecture.md`](docs/architecture.md)).
On choisit le moteur dans l'écran **6 Paramètres** (`strategy.name`) ; chaque
moteur a ses propres modèles et demande son propre entraînement.

**Colibri** (`colibri_v1_2`, par défaut) est un **classifieur** : un gradient
boosting écrit en Go apprend, sur des barrières à ± 1,5 ATR, l'issue nette de
coûts d'un trade, et n'entre que si l'espérance le justifie. Positions
fermées avant chaque week-end. Détail et mesures :
[`docs/colibri.md`](docs/colibri.md), [`docs/gbdt.md`](docs/gbdt.md).

**Troglodyte** est un **suivi de tendance structurel** : un **filtre de
Kalman** estime, à chaque bougie, la pente de la tendance et son
incertitude ; leur rapport `z` décide. Il porte ses positions pendant le
week-end, n'a pas d'AUC (« — » à l'écran) et se juge au P&L out-of-sample.

| | `troglodyte_v1_0` | `troglodyte_v1_1` (v0.7.1) |
|---|---|---|
| Entrée du filtre | log du prix | log du prix **normalisé par sa volatilité** |
| Entrée / sortie | z ≥ 1,5 / \|z\| < 0,5 | seuil **calibré** à l'entraînement, sortie à un tiers |
| Stop | 3 ATR fixe | **suiveur « chandelier »**, 2 à 4 ATR calibré |
| Actualités | non | **filtre déclaré** |

Seuils, grilles et demi-vie sont des **conventions** ; le calibrage choisit
in-sample, le walk-forward juge hors échantillon. Détail :
[`docs/troglodyte.md`](docs/troglodyte.md).

### Le filtre d'actualités

Pour les stratégies qui le **déclarent** (Troglodyte v1_1), aucune entrée
dans les 30 minutes autour d'une annonce à fort impact sur l'une des deux
devises de la paire. **Colibri n'y a jamais accès.** Le calendrier (flux
public de la semaine en cours) est récupéré pendant une séance live ou par
`gw news fetch`, et **archivé** : le backtest ne filtre que les périodes
archivées et compte à part les entrées qu'il n'a pas pu vérifier. Les
sources sont des modules remplaçables. Détail :
[`docs/actualites.md`](docs/actualites.md).

## Ligne de commande

Les mêmes calculs sans interface, pour une tâche planifiée ou un conteneur :

```bash
gw download                        # historique M1 complet (long)
gw download EURUSD --year 2019     # une paire, une année
gw download EURUSD --from 2019 --to 2021
gw train                           # walk-forward sur toutes les paires
gw train EURUSD GBPUSD             # ... ou seulement celles-là
gw train --risk-per-trade 0        # comparer les régimes de taille
gw migrate [--remove]              # convertit les anciens .gwb en Parquet
gw import --symbol EURUSD f.parquet  # verse un historique venu d'ailleurs
gw backtest EURUSD                 # rejeu d'une paire
gw backtest EURUSD --csv           # + trades, équité et métriques en CSV
gw runs                            # entraînements archivés
gw paths                           # où vivent configuration et données
gw config --default                # le modèle de configuration commenté
gw news fetch                      # récupère et archive le calendrier économique
gw news import cal.json            # verse un calendrier historique (format du flux)
```

Variables d'environnement (elles ont le dernier mot sur le fichier, et l'écran
Paramètres le signale) : `GW_CONFIG_DIR`, `GW_DATA_DIR`, `GW_BROKER`,
`GW_MODE`, `GW_STRATEGY`, `GW_STRATEGY_ENABLED`, `GW_LOG_LEVEL`, `GW_THEME`,
`GW_TIMEFRAME`, `GW_SEED`, `GW_BROKER_HOST`, `GW_BROKER_PORT`,
`GW_BROKER_CLIENT_ID`, `GW_BROKER_ACCOUNT`, `GW_NEWS`.

## Documentation

| Document | Contenu |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Arborescence, règles, où ajouter du code |
| [docs/colibri.md](docs/colibri.md) | Colibri : features, cible, décision, révisions, mesures |
| [docs/troglodyte.md](docs/troglodyte.md) | Troglodyte : modèle espace-état, filtre de Kalman, estimation, calibrage, décision |
| [docs/actualites.md](docs/actualites.md) | Filtre d'actualités : sources, archive, règle, honnêteté |
| [docs/gbdt.md](docs/gbdt.md) | Le gradient boosting maison : algorithme et choix |
| [docs/donnees.md](docs/donnees.md) | Dukascopy, stockage Parquet, import, unités de temps |
| [docs/brokers.md](docs/brokers.md) | Contrat de passerelle, rejeu, brancher un courtier |
| [docs/depannage.md](docs/depannage.md) | Garanties, pannes courantes, emplacement des données |
| [LLM.md](LLM.md) | Architecture et invariants — la mémoire de travail du projet |

## Contribuer

```bash
make test   # la suite complète        make race   # avec le détecteur de concurrence
make lint   # format + analyse         make dist   # les cinq binaires
```

Aucun test n'appelle le réseau (le calendrier est éprouvé contre un faux
serveur HTTP). La suite vérifie les propriétés dont dépend
l'honnêteté des résultats, pas seulement que le code s'exécute : stabilité par
préfixe des features ET des décisions, cible identique à l'issue du moteur
d'exécution, fenêtre avant incomplète = pas de label, blocs de walk-forward
disjoints, spread mesuré, refus comptés, entraînement reproductible. Toute
stratégie inscrite au catalogue passe d'office le banc de conformité
(`internal/strategy/strategytest`).

Pour publier : onglet **Actions** → **Release** → **Run workflow** avec le
numéro (`v0.7.1`), ou pousser un tag `v*`.

## Avertissement

Le mode par défaut est `paper` et la passerelle par défaut est un **rejeu
simulé**. Passer `broker.mode` à `live` engage de l'argent réel.

En mode `live` sur un vrai courtier, la connexion demande de **taper le
numéro du compte** ; elle est refusée si ce n'est pas celui de la session.

La passerelle Interactive Brokers est éprouvée contre les messages du
client officiel d'IB et contre un faux TWS, **pas encore contre un vrai
TWS**. La faire tourner d'abord sur un compte papier.

Un expert advisor peut perdre de l'argent, et un bon backtest n'est pas une
promesse. Le seul chiffre à regarder est l'agrégat **out-of-sample** du
walk-forward — jamais le rejeu in-sample, que le modèle de production connaît
déjà par cœur.

## Licence

[MIT](LICENSE) — utilisation, modification et redistribution libres, y compris
commerciales, à condition de conserver la mention de copyright.

Cette licence fournit le logiciel **sans aucune garantie**. Ce n'est pas une
formule de style ici : le programme peut passer des ordres sur un compte réel,
et la responsabilité de ce qu'il y fait reste entièrement celle de qui le
lance.
