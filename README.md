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
◆ Golden Waterfall  1 Live  2 Données  3 Backtest  4 Entraînement  5 Journal  6 Paramètres   REJEU — COMPTE SIMULÉ  replay  kill-switch
───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Compte                                                                                                                              │
│ Équité              Marge               Perte du jour       Ticks               Bougies             Ordres                          │
│ 10 404.73           367.20              0.41 %              3 931               981                 36                              │
│                                         plafond 2.0 %       dernier 13:00:00                        72 exécutés                     │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────╮╭─────────────────────────────────────────────────────────────────────────╮
│ Paires suivies                                           ││ EURUSD · H1                                                             │
│ Paire           Bid     Var. État      Signal            ││                          █│                                             │
│ ▸EURUSD      1.11155  +0.16 % armée     LONG 0.71        ││                          ██        █                                    │
│  GBPUSD      1.28706  +0.06 % arrêtée   neutre 0.52      ││                         █  █   │█  ██        █    ███   █ █             │
│  USDJPY      152.790  -0.22 % arrêtée   —                ││                   ██████ █  █  ██  ███  ████ █ ██ ████  ███             │
│                                                          ││ ███  █         ███   ██ ███ █│████ █████     │███│ █ █   ██   █ █       │
│                                                          ││ O 1.11168  H 1.11168  B 1.11098  C 1.11098  ·  420 bougies              │
╰──────────────────────────────────────────────────────────╯╰─────────────────────────────────────────────────────────────────────────╯
Moteur : 1 paire(s) armée(s)

tab écran  ·  ? aide  ·  q quitter  ·  c connecter  ·  k kill-switch global  ·  espace armer la paire  ·  ↑↓ sélection
```

## Pourquoi

**Le chiffre qu'on vous montre est celui qui compte.** La validation se fait en
walk-forward : chaque pli s'entraîne sur tout ce qui précède son bloc de test
et n'est évalué que sur ce bloc. Rien n'est jamais testé sur des données vues à
l'entraînement, et l'écran affiche l'AUC out-of-sample avec son repère —
**0,50 = hasard**.

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
simulé** : toute la chaîne fonctionne, aucun argent n'est engagé.

## Les six écrans

| | Ce qu'on y fait |
|---|---|
| **1 Live** | Compte, paires suivies, positions, graphique en chandeliers |
| **2 Données** | Inventaire de l'historique local, téléchargement complet ou par période, conversion des anciens fichiers (`m`) |
| **3 Backtest** | Rejeu d'une paire, courbe d'équité, liste des trades, export CSV (`e`) |
| **4 Entraînement** | Walk-forward, choix des paires (`p`), plis, agrégat out-of-sample, runs archivés |
| **5 Journal** | Journal applicatif et journal des trades exécutés, filtre texte (`/`), export CSV (`e`) |
| **6 Paramètres** | Compte et courtier, risque, stratégie, historique, interface |

`tab` change d'écran, `?` affiche l'aide complète, `q` quitte — et refuse tant
qu'un entraînement tourne.

## Le moteur Colibri

Le logiciel s'appelle **Golden Waterfall** ; son moteur de décision s'appelle
**Colibri**. Le moteur est un **module remplaçable** : les moteurs de backtest
et de live, le walk-forward et l'interface ne connaissent que le contrat
`strategy.Strategy`, et la génération suivante se branchera sans les toucher
([`docs/architecture.md`](docs/architecture.md)).

Colibri apprend, sur des bougies étiquetées par **barrières à ± 1,5 ATR**, la
probabilité qu'un trade finisse gagnant, avec un **gradient boosting écrit en
Go** — un fichier unique, un entraînement reproductible au bit près. Depuis
v0.4.1, la révision par défaut **`colibri_v1_2`** apprend l'issue NETTE de coûts
que le moteur d'exécution produirait vraiment, une tête par sens, et n'entre
que si l'espérance nette dépasse 0,10 R. `colibri_v1_0` et `v1_1` restent
disponibles et inchangées. v1_2 n'est pas meilleure partout : sur un des
quatre marchés synthétiques de sa mesure, v1_1 fait mieux — c'est écrit, et
seul un `gw train` sur votre historique tranchera.

Features, cible, décision, mesures et limites :
[`docs/colibri.md`](docs/colibri.md) et [`docs/gbdt.md`](docs/gbdt.md).

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
```

Variables d'environnement (elles ont le dernier mot sur le fichier, et l'écran
Paramètres le signale) : `GW_CONFIG_DIR`, `GW_DATA_DIR`, `GW_BROKER`,
`GW_MODE`, `GW_STRATEGY`, `GW_STRATEGY_ENABLED`, `GW_LOG_LEVEL`, `GW_THEME`,
`GW_TIMEFRAME`, `GW_SEED`, `GW_BROKER_HOST`, `GW_BROKER_PORT`.

## Documentation

| Document | Contenu |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Arborescence, règles, où ajouter du code |
| [docs/colibri.md](docs/colibri.md) | Features, cible, décision, révisions, mesures |
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

Aucun test n'appelle le réseau. La suite vérifie les propriétés dont dépend
l'honnêteté des résultats, pas seulement que le code s'exécute : stabilité par
préfixe des features ET des décisions, cible identique à l'issue du moteur
d'exécution, fenêtre avant incomplète = pas de label, blocs de walk-forward
disjoints, spread mesuré, refus comptés, entraînement reproductible. Toute
stratégie inscrite au catalogue passe d'office le banc de conformité
(`internal/strategy/strategytest`).

Pour publier : onglet **Actions** → **Release** → **Run workflow** avec le
numéro (`v0.4.1`), ou pousser un tag `v*`.

## Avertissement

Le mode par défaut est `paper` et la passerelle par défaut est un **rejeu
simulé**. Passer `broker.mode` à `live` engage de l'argent réel.

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
