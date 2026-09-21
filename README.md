<div align="center">

# 🌊 Golden Waterfall

**Un expert advisor de trading algorithmique qui tient entièrement dans votre terminal.**

Télécharger l'historique, entraîner un modèle, le valider honnêtement, puis le
laisser trader — cinq écrans, un seul binaire, aucun navigateur, aucun
serveur, aucune dépendance à installer.

[Télécharger](https://github.com/BLKMLO/Golden-Waterfall/releases/latest) ·
[Démarrer](#démarrer) ·
[Le moteur Colibri](#le-moteur-colibri) ·
[Ce qu'il ne fera jamais](#ce-quil-ne-fera-jamais)

</div>

---

```
◆ Golden Waterfall   1 Live   2 Données   3 Backtest   4 Entraînement   5 Journal      REJEU — COMPTE SIMULÉ  replay  kill-switch  colibri_v1_1
─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
╭───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Compte                                                                                                                                    │
│ Équité              Marge               Perte du jour       Ticks               Bougies             Ordres                                 │
│ 10 404.73           367.20              0.41 %              3 931               981                 36                                     │
│                                         plafond 2.0 %       dernier 13:00:00                        72 exécutés                            │
╰───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────╮╭───────────────────────────────────────────────────────────────────────────────╮
│ Paires suivies                                           ││ EURUSD · H1                                                                   │
│ Paire           Bid     Var. État      Signal            ││                          █│                                                   │
│ ▸EURUSD      1.11155  +0.16 % armée     LONG 0.71        ││                          ██        █                                          │
│  GBPUSD      1.28706  +0.06 % arrêtée   neutre 0.52      ││                         █  █   │█  ██        █    ███   █ █                   │
│  USDJPY      152.790  -0.22 % arrêtée   —                ││                   ██████ █  █  ██  ███  ████ █ ██ ████  ███                   │
│                                                          ││ ███  █         ███   ██ ███ █│████ █████     │███│ █ █   ██   █ █             │
│                                                          ││ O 1.11168  H 1.11168  B 1.11098  C 1.11098  ·  420 bougies                    │
╰──────────────────────────────────────────────────────────╯╰───────────────────────────────────────────────────────────────────────────────╯
╭───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Positions ouvertes (rapportées par la passerelle)                                                                                         │
│ Paire    Sens     Quantité   Prix moyen   P&L latent                                                                                      │
│  EURUSD   LONG     10000.00      1.10980       +17.50                                                                                     │
╰───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
moteur : 1 paire(s) armée(s)

c connecter / déconnecter  ·  k kill-switch global  ·  espace armer la paire  ·  ↑↓ sélection  ·  u unité de temps  ·  ? aide  ·  q quitter
```

## Pourquoi Golden Waterfall

**Le chiffre qu'on vous montre est celui qui compte.** La validation se fait en
walk-forward : la seconde moitié de l'historique est découpée en blocs de test
consécutifs, chaque pli s'entraîne sur tout ce qui le précède et n'est évalué
que sur son bloc. Rien n'est jamais testé sur des données vues à
l'entraînement, et l'écran affiche l'AUC out-of-sample avec son repère —
**0,50 = hasard**.

**L'interface ne ment jamais.** Une donnée qu'on n'a pas s'affiche « — », pas
« 0 ». Une passerelle simulée porte un bandeau **REJEU** en permanence. Un
ordre refusé par manque de marge est compté, jamais confondu avec une
abstention du modèle. Sans côté ask dans les données, aucun coût n'est
modélisé — et c'est écrit, plutôt qu'un zéro qui ressemblerait à une mesure.

**Aucun trade n'est inventé.** Le journal ne contient que des exécutions
réellement rapportées par une passerelle. Quand le courtier ne fournit pas de
P&L, le programme ne le calcule pas à sa place : frais, swap et conversion lui
échappent, et un chiffre approximatif serait pire que pas de chiffre.

**Quatre verrous avant le premier ordre.** Broker connecté, kill-switch global
armé, paire armée, aucun ordre en vol sur cette paire. Les quatre sont visibles
à l'écran : un silence s'explique toujours.

**Un fichier, rien d'autre.** Pas de Python, pas de runtime, pas de
bibliothèque native, pas de base de données à installer. Le binaire se copie et
se lance. Les données vivent dans votre dossier utilisateur, jamais à côté de
lui.

## Installation

Téléchargez l'archive correspondant à votre système depuis la
[dernière version](https://github.com/BLKMLO/Golden-Waterfall/releases/latest),
extrayez le fichier `gw` qu'elle contient, et placez-le où vous voulez.

| Votre système | Fichier à prendre |
|---|---|
| Linux | `golden-waterfall-…-linux-amd64.tar.gz` |
| Linux (ARM, Raspberry Pi) | `golden-waterfall-…-linux-arm64.tar.gz` |
| macOS Intel | `golden-waterfall-…-macos-amd64.tar.gz` |
| macOS Apple Silicon | `golden-waterfall-…-macos-arm64.tar.gz` |
| Windows | `golden-waterfall-…-windows-amd64.zip` |

Un fichier `SHA256SUMS` accompagne les archives si vous voulez vérifier ce que
vous avez téléchargé :

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

Ou compilez depuis les sources — il ne faut rien d'autre que Go 1.25 :

```bash
go build -o gw ./cmd/gw
```

## Démarrer

**1. Lancez le programme.**

```bash
./gw
```

Au premier lancement, un `config.yaml` commenté est écrit dans votre dossier de
configuration et le dossier de données est créé. `./gw paths` dit exactement
où. Vous pouvez y modifier les paires suivies, les limites de risque et le
capital simulé.

**2. Téléchargez l'historique.** Écran **2 Données**, touche `D`.

La source est Dukascopy : des bougies M1 gratuites et profondes, en **bid et en
ask**. Le côté ask n'est pas un luxe — c'est lui qui permet de *mesurer* le
spread au lieu de l'inventer, et donc de savoir ce qu'un aller-retour coûte
réellement.

```
╭─────────────────────────────────────────────────────────────────────────────╮
│ Téléchargement en cours                                                     │
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━──────────────────────  62 %      │
│ EURUSD 2018 · 2018-08-14 · 162 300 bougies · 9 jours sans donnée · 0 échec  │
╰─────────────────────────────────────────────────────────────────────────────╯
```

Comptez plusieurs heures et quelques gigaoctets pour trente instruments sur
quinze ans. Une année déjà complète n'est jamais retéléchargée ; une année
trouée, si.

**3. Entraînez et validez.** Écran **4 Entraînement**, touche `r`.

C'est l'écran le plus important du programme : c'est le seul qui dise si la
stratégie vaut quelque chose.

```
╭─────────────────────────────────────────────────────────────────────────────╮
│ Agrégat OUT-OF-SAMPLE                                                       │
│ AUC out-of-sample  P&L net OOS USD  Trades OOS   Taux de gain  Profit factor │
│ 0.616              +9 540.63        1 045        61.0 %        1.60          │
│ 0,50 = hasard                                                                │
╰─────────────────────────────────────────────────────────────────────────────╯
```

Un modèle de **production** est ensuite entraîné sur tout l'historique : c'est
lui qui partira en live, et les plis disent s'il le mérite.

**4. Tradez.** Écran **1 Live** : `c` connecte la passerelle, `k` arme le
kill-switch global, `espace` arme la paire sélectionnée.

Par défaut la passerelle est un **rejeu simulé** de vos données historiques :
toute la chaîne fonctionne, aucun argent n'est engagé, et l'interface le
rappelle en permanence.

## Les cinq écrans

| | Ce qu'on y fait | Touches |
|---|---|---|
| **1 Live** | Compte, paires suivies, positions, graphique en chandeliers | `c` connecter · `k` kill-switch · `espace` armer · `u` unité de temps |
| **2 Données** | Inventaire de l'historique local, téléchargement | `d` une paire · `D` tout · `x` interrompre |
| **3 Backtest** | Rejeu d'une paire, courbe d'équité, liste des trades | `r` lancer · `u` unité de temps |
| **4 Entraînement** | Walk-forward, plis, agrégat out-of-sample, runs archivés | `r` lancer · `o` historique · `+`/`-` plis |
| **5 Journal** | Journal applicatif et journal des trades exécutés | `t` basculer · `f` niveau · `s` figer |

`tab` change d'écran, `?` affiche l'aide complète, `q` quitte — et refuse de le
faire tant qu'un entraînement tourne.

## Le moteur Colibri

Le logiciel s'appelle **Golden Waterfall**. Son moteur de décision s'appelle
**Colibri**. Chaque génération de moteur portera un nom d'oiseau ; Colibri est
la première.

Le principe tient en une phrase : un classifieur binaire apprend, sur des
bougies étiquetées par **triple barrière**, la probabilité que la barrière
haute soit touchée avant la basse. Probabilité élevée → long, faible → short,
entre les deux → abstention. Le label étant symétrique, un seul modèle sert les
deux sens.

| Révision | Modèles | Seuils |
|---|---|---|
| `colibri_v1_0` | un **par actif** | 0,55 / 0,45 |
| `colibri_v1_1` *(défaut)* | **un seul**, mutualisé sur tous les actifs | 0,60 / 0,40 |

Le classifieur est un **gradient boosting sur arbres écrit en Go**, pas une
bibliothèque liée : c'est ce qui permet au programme de tenir dans un fichier
unique, et l'entraînement y est reproductible au bit près à graine égale.

Détails — les 34 features, le labeling, les garde-fous anti-fuite :
[`docs/colibri.md`](docs/colibri.md) et [`docs/gbdt.md`](docs/gbdt.md).

## Ligne de commande

Les mêmes calculs, sans interface — pour une tâche planifiée ou un conteneur :

```bash
gw download              # historique M1 (long : plusieurs heures)
gw download EURUSD       # ou juste une paire
gw train                 # walk-forward complet + modèle de production
gw train -folds 8 -tf H1 # avec d'autres réglages
gw backtest EURUSD       # rejeu d'une paire
gw runs                  # entraînements archivés
gw paths                 # où vivent configuration et données
gw config --default      # le modèle de configuration commenté
```

Réglages par variables d'environnement, pour un conteneur : `GW_CONFIG_DIR`,
`GW_DATA_DIR`, `GW_BROKER`, `GW_MODE`, `GW_STRATEGY`, `GW_STRATEGY_ENABLED`,
`GW_LOG_LEVEL`, `GW_THEME`, `GW_TIMEFRAME`, `GW_SEED`.

## Ce qu'il ne fera jamais

| Ce qu'un programme pourrait faire | Ce que celui-ci fait |
|---|---|
| Afficher `0` pour une donnée qu'il n'a pas | affiche `—` |
| Estimer l'équité quand le courtier ne répond pas | affiche des tirets et signale l'anomalie |
| Facturer zéro coût faute de côté ask | dit qu'aucun coût n'est modélisé, et que le résultat est donc optimiste |
| Compter un ordre refusé comme une abstention du modèle | compte les refus à part et les affiche |
| Additionner des P&L nés dans des devises différentes | signale que la somme mélange des devises |
| Présenter un rejeu comme une performance | marque le backtest **IN-SAMPLE** en permanence |
| Inventer un P&L que le courtier n'a pas rapporté | journalise le trade avec un P&L nul et le dit |
| Se déclarer connecté par optimisme | échoue explicitement |
| Fabriquer un aller-retour à partir de deux entrées du même sens | ne journalise rien et signale l'incohérence |
| Appliquer un modèle dont les colonnes ont bougé | refuse le modèle en nommant la colonne fautive |

Ce ne sont pas des intentions : chacune de ces lignes correspond à un champ
dans le code et à un test qui échouerait si elle cessait d'être vraie.

## Où vivent vos données

Jamais à côté du binaire — celui-ci est unique et déplaçable.

| | Configuration | Données |
|---|---|---|
| Linux | `~/.config/golden-waterfall` | `~/.local/share/golden-waterfall` |
| macOS | `~/Library/Application Support/GoldenWaterfall` | idem |
| Windows | `%AppData%\GoldenWaterfall` | `%LocalAppData%\GoldenWaterfall` |

Le dossier de données contient l'historique, les modèles entraînés, la base des
trades et les journaux. `GW_CONFIG_DIR` et `GW_DATA_DIR` forcent ces
emplacements, pour une installation portable ou un conteneur.

## Quand ça se passe mal

| Ce qui arrive | Ce que fait Golden Waterfall |
|---|---|
| Dukascopy répond 429 (limite de débit) | attend longuement, honore `Retry-After`, et ne monte jamais la concurrence |
| Un jour n'a pas de donnée (week-end, férié) | le compte comme normal, pas comme un échec |
| Une année se télécharge à moitié | l'écrit, la marque incomplète, et la refait à la prochaine demande |
| Un fichier d'historique est tronqué | refuse de le lire et nomme le fichier et le remède |
| Un fichier au nom inattendu traîne dans le dossier | l'ignore, plutôt que de compter ses bougies deux fois |
| La configuration contient une valeur absurde | refuse de démarrer, en nommant la clé fautive |
| La configuration contient une faute de frappe | refuse de démarrer : une clé inconnue est une limite jamais appliquée |
| Le jeu d'entraînement est trop petit | refuse d'entraîner, plutôt que de livrer un modèle décoratif |
| Une seule classe dans les labels | refuse d'entraîner, plutôt qu'un modèle constant déguisé |
| Le courtier ne répond pas sur les positions | s'abstient de décider, plutôt que de trader sur une image périmée |
| La perte journalière maximale est atteinte | bloque toute nouvelle entrée ; les sorties restent toujours possibles |
| Une deuxième instance est lancée | échoue proprement : la base est déjà ouverte |

## Contribuer

```bash
make test     # la suite complète
make race     # avec le détecteur de concurrence
make lint     # format + analyse statique
make dist     # les cinq binaires
```

Aucun test n'appelle le réseau : tout tourne hors ligne.

La suite vérifie les propriétés dont dépend l'honnêteté des résultats, pas
seulement que le code s'exécute : **stabilité par préfixe** des features (une
feature qui regarderait vers l'avant casse le test), fenêtre avant incomplète =
pas de label, blocs de walk-forward disjoints, spread mesuré, refus de marge
comptés, repère d'équité du jour jamais écrasé, entraînement reproductible, et
équivalence des indicateurs optimisés avec une implémentation naïve de
référence.

[`LLM.md`](LLM.md) décrit l'architecture et les invariants à respecter, et
[`docs/`](docs/) le détail de chaque partie :

| Document | Contenu |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Arborescence, règles, où ajouter du code |
| [docs/colibri.md](docs/colibri.md) | Features, labeling, seuils, révisions |
| [docs/gbdt.md](docs/gbdt.md) | Le gradient boosting maison : algorithme et choix |
| [docs/donnees.md](docs/donnees.md) | Dukascopy, format `.gwb`, unités de temps |
| [docs/brokers.md](docs/brokers.md) | Contrat de passerelle, rejeu, brancher un courtier |

Pour publier une version : onglet **Actions** → **Release** → **Run workflow**,
en saisissant le numéro (`v0.1.0`). Le workflow vérifie le code, compile les
cinq binaires et publie. Pousser un tag `v*` produit le même résultat.

## Avertissement

Le mode par défaut est `paper` et la passerelle par défaut est un **rejeu
simulé**. Passer `broker.mode` à `live` engage de l'argent réel.

Un expert advisor peut perdre de l'argent, et un bon backtest n'est pas une
promesse. Le seul chiffre à regarder est l'agrégat **out-of-sample** du
walk-forward — jamais le rejeu in-sample, que le modèle de production connaît
déjà par cœur.
