<div align="center">

# 🌊 Golden Waterfall

**Un expert advisor de trading algorithmique qui tient entièrement dans votre terminal.**

Télécharger l'historique, entraîner un modèle, le valider honnêtement, puis le
laisser trader — deux interfaces, un seul binaire, aucun navigateur, aucun
serveur, aucune dépendance à installer.

[Télécharger](https://github.com/BLKMLO/Golden-Waterfall/releases/latest) ·
[Démarrer](#démarrer) ·
[Documentation](#documentation)

</div>

---

```
◆ Live    1 Live      2 Journal      3 Paramètres             REJEU — COMPTE SIMULÉ  compte USD  replay  kill-switch  martinet_v1_0
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Compte                                                                                                                           │
│ Équité        Marge         Perte du jour Ticks         Bougies       Ordres                                                     │
│ 10091.18      0.00          -0.91 %       7 215         1 442         3                                                          │
│                             plafond 2.0 % dernier 00:1…               6 exécutés                                                 │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────╮╭────────────────────────────────────────────────────────────────╮
│ Paires suivies                                                 ││ EURUSD · H1                                                    │
│  Paire           Bid     Var. État      Signal                 ││           █│  │                  │                             │
│ ▸EURUSD      1.10840  +0.04 % armée     neutre                 ││ █││       ██││││              │ │││                            │
│  GBPUSD            —        — arrêtée   pas de modèle          ││ ██│      │███████  ██         ██████                           │
│                                                                ││ │███     ████  │█││██         █│██││                           │
│                                                                ││   │█│ ████ │    ██│██     ██ ██                                │
│                                                                ││    ██████│ │    │██ █     ████                                 │
│                                                                ││    ████││        █│ █  │  █ ██                                 │
│                                                                ││         │           █│ │ │█ █                                  │
│                                                                ││                      ███│██                                    │
│                                                                ││                      █████                                     │
│                                                                ││                       │ │                                      │
│                                                                ││ O 1.10792  H 1.10792  B 1.10747  C 1.10769  ·  36 bougies      │
╰────────────────────────────────────────────────────────────────╯╰────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Positions ouvertes (rapportées par la passerelle)                                                                                │
│  Paire    Sens     Quantité   Prix moyen   P&L latent                                                                            │
│   (aucune donnée)                                                                                                                │
│                                                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
Préreq.  → prêt  ✓ moteur  ✓ M5  ⚠ entr. 1/2  ⚠ hist. 1/2  ✓ dim.  ✓ news  p détail
EURUSD  → peut trader  ✓ pass.  ✓ SL  ✓ k-s  ✓ armée  ✓ modèle  ✓ hist.  ✓ USD  ✗ news
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

tab écran  ·  ? aide  ·  q quitter  ·  c connecter / déconnecter  ·  k kill-switch global  ·  espace armer la paire  ·  ↑↓ sélection
```

<sub>Capture réelle du binaire (`gw`, v0.8.0, donc `martinet_v1_0`, remplacée depuis par `v1_1`) : Martinet en M5 sur un rejeu d'historique SYNTHÉTIQUE, EURUSD armée — trois ordres à barrières, six exécutions, aucune position restée ouverte. GBPUSD, sans modèle ni historique, reste muette : la ligne Prérequis le dit (« ⚠ entraînement 1/2 »).</sub>

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

Deux interfaces, pour deux activités qui ne se font pas au même moment :

| | Écrans | Pour |
|---|---|---|
| **`gw backtrain`** | 1 Données · 2 Entraînement · 3 Backtest · 4 Paramètres | Préparer un moteur. Aucun ordre n'y part ; il n'ouvre pas la base du journal et peut donc rester ouvert **pendant** une séance. |
| **`gw`** | 1 Live · 2 Journal · 3 Paramètres | Trader, en papier ou en réel. L'écran Live dit si les **prérequis** sont faits (moteur, unité de temps, entraînement, historique, dimensionnement, calendrier) et où réparer ce qui manque — touche `p`. |

**1. Lancez `./gw backtrain`.** Un `config.yaml` commenté est écrit au
premier démarrage ; `./gw paths` dit où. Tout se règle ensuite depuis
l'écran **Paramètres**, présent dans les deux interfaces.

**2. Téléchargez l'historique** — écran **1 Données**, `D` pour tout, `d` pour
la paire sélectionnée. Des bougies M1 en **bid et en ask** : c'est le côté ask
qui permet de *mesurer* le spread au lieu de l'inventer. Deux sources, au
choix dans **Paramètres** (`history.source`) : **Dukascopy** (défaut, depuis
2003, tout le catalogue, volume de ticks — plusieurs heures pour trente
instruments sur quinze ans) ou **FXCM** (depuis 2012, 25 paires forex, rapide,
mais sans volume : Colibri ne peut pas s'en servir, Troglodyte oui). Un
historique venu d'ailleurs s'importe en Parquet ou en CSV (`gw import`).
Détail : [docs/donnees.md](docs/donnees.md).

**3. Entraînez et validez** — écran **2 Entraînement**, `r`. C'est le seul
écran qui dise si la stratégie vaut quelque chose. Un modèle de **production**
est ensuite entraîné sur tout l'historique : c'est lui qui partira en live, et
les plis disent s'il le mérite. L'écran **3 Backtest** rejoue une paire avec
ce modèle, pour comprendre ce qu'il fait.

**4. Tradez** — lancez `./gw`, écran **1 Live** : la ligne **Prérequis** doit
dire « → prêt » (`p` en détaille chaque point) ; `c` connecte, `k` arme le
kill-switch global, `espace` arme la paire. Par défaut la passerelle est un **rejeu
simulé** : toute la chaîne fonctionne, aucun argent n'est engagé. Pour un
vrai courtier, **Interactive Brokers** (TWS ou IB Gateway, forex) :
mise en place dans [docs/brokers.md](docs/brokers.md).

## Les écrans

| Interface | Écran | Ce qu'on y fait |
|---|---|---|
| `gw backtrain` | **1 Données** | Inventaire de l'historique local, téléchargement complet ou par période, conversion des anciens fichiers (`m`) |
| | **2 Entraînement** | Walk-forward, choix des paires (`p`), plis, agrégat out-of-sample, runs archivés |
| | **3 Backtest** | Rejeu d'une paire, courbe d'équité, liste des trades défilable (`t`, `pgup`/`pgdn`), détail d'un trade (`entrée`), export CSV (`e`) |
| `gw` | **1 Live** | Prérequis de la séance (`p`), compte, paires suivies, positions, graphique en chandeliers ; ligne de contrôle « pourquoi cette paire ne trade pas » |
| | **2 Journal** | Journal applicatif et journal des trades exécutés (défilable, détail par `entrée`), filtre texte (`/`), export CSV (`e`) |
| les deux | **Paramètres** | Compte et courtier, risque, stratégie, historique, interface |

`tab` change d'écran, `?` affiche l'aide complète, `q` quitte — et refuse tant
qu'un entraînement tourne. L'entête dit toujours quelle interface est ouverte
(« · Live » ou « · Backtrain ») ; celle de l'atelier porte le badge
**ATELIER — aucun ordre**.

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
On choisit le moteur dans l'écran **Paramètres** (`strategy.name`) ; chaque
moteur a ses propres modèles et demande son propre entraînement. Seule la
**dernière révision** de chaque moteur est livrée (depuis v0.8.0) : une
configuration restée sur une révision retirée passe d'office à celle qui
la remplace (à réentraîner).

**Configuration réparée d'office** (v0.8.2) : après une mise à jour, un
réglage que la nouvelle version refuse — clé disparue, valeur hors
bornes, révision retirée — est remis sur une valeur valide au lieu de
bloquer le démarrage. `config.yaml` est corrigé en gardant ses
commentaires, l'ancien est sauvegardé à côté (`config.yaml.<date>.bak`),
et chaque réparation est annoncée dans le terminal, la barre d'état et le
journal. Seule une erreur de syntaxe YAML empêche de démarrer.

**Colibri** (`colibri_v1_2`, par défaut) est un **classifieur** : un gradient
boosting écrit en Go apprend, sur des barrières à ± 1,5 ATR, l'issue nette de
coûts d'un trade, et n'entre que si l'espérance le justifie. Positions
fermées avant chaque week-end. Détail et mesures :
[`docs/colibri.md`](docs/colibri.md), [`docs/gbdt.md`](docs/gbdt.md).

**Troglodyte** (`troglodyte_v1_1`) est un **suivi de tendance structurel** :
un **filtre de Kalman** estime, à chaque bougie, la pente de la tendance du
prix normalisé par sa volatilité, et son incertitude ; leur rapport `z`
décide, avec un stop suiveur « chandelier ». Seuil et stop sont calibrés à
l'entraînement. Il porte ses positions pendant le week-end, n'a pas d'AUC
(« — » à l'écran) et se juge au P&L out-of-sample. Détail :
[`docs/troglodyte.md`](docs/troglodyte.md).

**Martinet** (`martinet_v1_1`, v0.8.1) est un **scalpeur de zones de
liquidité**, épuré — des plus hauts, des plus bas, un ATR pour les
distances et le volume en confirmation.
Quand une bougie perce un plus haut (plus bas) de swing intact, sert les
stops qui y dormaient, puis clôture de nouveau en deçà, la cassure a échoué :
Martinet prend le sens inverse, stop au-delà de la mèche, cible à 1, 1,5 ou
2 R choisie à l'entraînement ; l'entraînement décide aussi s'il exige un
pic de volume (≥ 1,5 × la médiane) sur la bougie de balayage — seulement
si l'historique a du volume, si bien que FXCM et Interactive Brokers
restent utilisables. Séance 7 h – 20 h UTC, spread ≤ 0,25 R,
barrière de deux heures, filtre d'actualités. **M1, M5 ou M15 seulement.**
Détail : [`docs/martinet.md`](docs/martinet.md).

| | Colibri | Troglodyte | Martinet |
|---|---|---|---|
| Nature | classifieur GBDT | tendance (Kalman) | règle de balayage |
| Unités de temps | toutes | toutes | M1, M5, M15 |
| Volume exigé | oui | non | si le calibrage retient le filtre |
| Week-end | fermé | porté | fermé |
| Actualités | jamais | filtre | filtre |
| Juge | AUC et P&L OOS | P&L OOS | P&L OOS |

Seuils, grilles et fenêtres sont des **conventions** ; le calibrage choisit
in-sample, le walk-forward juge hors échantillon.

### Le filtre d'actualités

Pour les stratégies qui le **déclarent** (Troglodyte, Martinet), aucune entrée
dans les 30 minutes autour d'une annonce à fort impact sur l'une des deux
devises de la paire. **Colibri n'y a jamais accès.** Le calendrier (flux
public de la semaine en cours) est récupéré pendant une séance live ou par
`gw news fetch`, et **archivé** : le backtest ne filtre que les périodes
archivées et compte à part les entrées qu'il n'a pas pu vérifier. Les
sources sont des modules remplaçables. Chaque walk-forward d'une stratégie
qui déclare le filtre rejoue aussi ses plis **sans** lui, avec le même
modèle : l'écart « avec − sans » se lit dans `gw train` et l'écran
Entraînement, mesuré sur la seule période que le calendrier couvre.
Détail : [`docs/actualites.md`](docs/actualites.md).

## Ligne de commande

Les mêmes calculs sans interface, pour une tâche planifiée ou un conteneur.
Comme `gw backtrain`, ces commandes n'ouvrent pas la base du journal : elles
tournent pendant une séance `gw`.

```bash
gw                                 # interface de trading (Live, Journal, Paramètres)
gw backtrain                       # interface d'atelier (Données, Entraînement, Backtest, Paramètres)
gw download                        # historique M1 complet (long)
gw download EURUSD --year 2019     # une paire, une année
gw download EURUSD --from 2019 --to 2021
gw download EURUSD --source fxcm   # une autre source, pour ce téléchargement
gw train                           # walk-forward sur toutes les paires
gw train EURUSD GBPUSD             # ... ou seulement celles-là
gw train --risk-per-trade 0        # comparer les régimes de taille
gw migrate [--remove]              # convertit les anciens .gwb en Parquet
gw import --symbol EURUSD f.parquet  # verse un historique venu d'ailleurs
gw import --symbol EURUSD --tz Europe/Athens f.csv  # ... ou un CSV (MetaTrader, HistData…)
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
`GW_BROKER_CLIENT_ID`, `GW_BROKER_ACCOUNT`, `GW_NEWS`, `GW_HISTORY_SOURCE`.

## Documentation

| Document | Contenu |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Arborescence, règles, où ajouter du code |
| [docs/colibri.md](docs/colibri.md) | Colibri : features, cible, décision, révisions, mesures |
| [docs/troglodyte.md](docs/troglodyte.md) | Troglodyte : modèle espace-état, filtre de Kalman, estimation, calibrage, décision |
| [docs/martinet.md](docs/martinet.md) | Martinet : zones de liquidité, balayage rejeté, filtres, calibrage |
| [docs/actualites.md](docs/actualites.md) | Filtre d'actualités : sources, archive, règle, honnêteté |
| [docs/gbdt.md](docs/gbdt.md) | Le gradient boosting maison : algorithme et choix |
| [docs/donnees.md](docs/donnees.md) | Sources (Dukascopy, FXCM, en brancher une), stockage Parquet, import Parquet/CSV, unités de temps |
| [docs/brokers.md](docs/brokers.md) | Contrat de passerelle, rejeu, brancher un courtier |
| [docs/depannage.md](docs/depannage.md) | Garanties, pannes courantes, emplacement des données |
| [LLM.md](LLM.md) | Architecture et invariants — la mémoire de travail du projet |

## Contribuer

```bash
make test   # la suite complète        make race   # avec le détecteur de concurrence
make lint   # format + analyse         make dist   # les cinq binaires
```

Aucun test n'appelle le réseau (le calendrier et les sources d'historique sont
éprouvés contre de faux serveurs HTTP). La suite vérifie les propriétés dont dépend
l'honnêteté des résultats, pas seulement que le code s'exécute : stabilité par
préfixe des features ET des décisions, cible identique à l'issue du moteur
d'exécution, fenêtre avant incomplète = pas de label, blocs de walk-forward
disjoints, spread mesuré, refus comptés, entraînement reproductible. Toute
stratégie inscrite au catalogue passe d'office le banc de conformité
(`internal/strategy/strategytest`).

Pour publier : onglet **Actions** → **Release** → **Run workflow** avec le
numéro (`v0.8.0`), ou pousser un tag `v*`.

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
