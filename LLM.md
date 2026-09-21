# LLM.md — Mémoire de travail de Golden Waterfall

> **Source de vérité unique du contexte projet, pour TOUTES les IA.**
> Règle de maintenance : après chaque changement notable, mettre ce fichier
> à jour (journal de décisions inclus) plutôt que de compter sur la mémoire
> de conversation.
>
> Le détail « pourquoi » vit dans `docs/` ; ce fichier est la carte.

## Identité — règle de nommage (importante)

- **Golden Waterfall** = le LOGICIEL (binaire `gw`, TUI, paquets
  `internal/`). Toute l'identité visible dit « Golden Waterfall ».
- **Colibri** = uniquement le **moteur de décision**
  (`internal/strategy/colibri*.go`, `internal/feature/colibri.go`).
  Ne jamais appeler le logiciel « Colibri ».
- **Chaque GÉNÉRATION de moteur porte un nom d'oiseau.** Colibri est la
  première. La suivante, quand elle changera d'approche, prendra un autre
  nom d'oiseau — pas un numéro de plus. Les révisions à l'intérieur d'une
  génération sont numérotées : `colibri_v1_0`, `colibri_v1_1`.

## Commandes

```bash
make build      # binaire gw (CGO_ENABLED=0)
make race       # tests + détecteur de concurrence
make lint       # gofmt + go vet
make dist       # les cinq binaires (Linux ×2, macOS ×2, Windows)
./gw            # interface terminal
./gw paths      # où vivent config et données
```

Sous-commandes non interactives : `download`, `train`, `backtest PAIRE`,
`runs`, `paths`, `config [--default]`, `version`.

Configuration : `config.yaml` dans le dossier de config de l'utilisateur,
créé au premier lancement depuis le modèle commenté **embarqué dans le
binaire**. Priorité : défauts → YAML → variables `GW_*` (l'environnement a
le dernier mot).

Publication : onglet **Actions** → **Release** → **Run workflow** avec le
numéro de version, ou pousser un tag `v*`.

## Règles d'architecture (non négociables)

1. **Flux strict, aller ET retour** :
   `Feed → Strategy → Signal → risk.Manager → OrderRequest → Gateway`,
   retour par `ExecutionReport → live.Engine → journal`. Imposé à la fois
   en backtest et en live. Aucune stratégie ne parle au broker ; aucun
   ordre ne contourne le risque ; **aucun trade sans compte rendu réel**.
2. **Câblage uniquement dans `internal/app/app.go` (`app.New`)**.
3. **Bus d'événements** (`core/bus.go`) : ne bloque JAMAIS un producteur
   (canaux tamponnés, éviction du plus ancien, pertes comptées).
4. **Config centralisée et fail-fast** : tout par `config.Load()`. Valeur
   absurde ou clé inconnue = refus de démarrer, avec la clé nommée.
5. **Paper → live** = `broker.mode` dans `config.yaml`. Rien d'autre.
6. **L'interface ne ment jamais** (voir plus bas).
7. **Ne jamais modifier une définition de modèle publiée** (features,
   barrières, seuils) : toute évolution crée une nouvelle **révision** de
   stratégie, et tout changement d'approche une nouvelle **génération**
   (nouveau nom d'oiseau).

## L'honnêteté, en pratique dans le code

Chaque ligne correspond à un champ réel et à un test qui échouerait si elle
cessait d'être vraie.

| Mécanisme | Où | Ce qu'il empêche |
|---|---|---|
| `component.Dash` (« — ») | TUI | Confondre « zéro » et « on ne sait pas » |
| `Info.Simulated` + bandeau REJEU | broker, entête TUI | Prendre un compte fictif pour un compte réel |
| `Stats.CostsModelled` | backtest | Facturer zéro coût faute de côté ask |
| `Stats.CurrencyExact` | backtest | Additionner des devises non convertibles |
| `Stats.RejectedOrders` | backtest | Confondre un refus de marge et une abstention |
| `Decision.Reason` + compteurs | risk | Un silence inexpliqué |
| `SymbolState.Notice` + `ModelLoaded` | live | Confondre « pas de modèle » et « historique périmé » |
| `FileHeader.Failures` / `Complete()` | data | Prendre une année trouée pour une année faite |
| Avertissement IN-SAMPLE | backtest (TUI + CLI) | Lire un rejeu comme une performance |
| `ExecutionReport` obligatoire | live | Inventer un trade que le broker n'a pas fait |
| `sameColumns` au chargement | strategy | Appliquer un modèle à des colonnes décalées |

## Conventions

- **Commentaires en français**, identifiants en anglais (convention Go).
  Un commentaire dit POURQUOI, pas QUOI.
- **Un paquet = une responsabilité.** Le fichier ne répète pas le paquet
  (`risk/manager.go`, pas `risk/risk_manager.go`).
- **Tests en miroir** : `internal/<paquet>/<fichier>_test.go`.
- **Aucune couleur littérale hors de `internal/tui/theme`.**
- **Aucun `panic` sur une donnée** ; seulement sur une incohérence de code.
- Nouvelle stratégie / nouvelle passerelle : `Register()` dans un `init()`,
  aucun autre fichier à modifier.
- **Une optimisation se mesure.** Un banc d'essai avant, un après, et pour
  une réécriture numérique une comparaison contre une implémentation naïve
  de référence (`indicator_test.go`). Sans ça, ce n'est qu'une croyance.

## Points techniques à ne pas réapprendre

- **Dukascopy** : mois **0-based** dans les URLs ; `.bi5` = LZMA, champs
  dans l'ordre **`offset, open, CLOSE, LOW, HIGH, volume`** (pas OHLC) ;
  404 = marché fermé ; 429 = limite de débit → backoff LONG,
  concurrence 3. Week-ends jamais demandés.
- **Format `.gwb`** : en-tête 64 o + enregistrements 40 o à taille fixe,
  prix en entiers mis à l'échelle. Écriture via fichier temporaire renommé.
  Lecture d'une tranche de dates par **seek**. L'en-tête porte le nombre de
  jours en échec : `Complete()` décide s'il faut retélécharger.
- **Anti-fuite** : le test de **stabilité par préfixe**
  (`Compute(s)[:k] == Compute(s[:k])`) est le garde-fou central. Ne jamais
  le désactiver.
- **Labeling** : deux barrières dans la même bougie → **la basse**, comme
  l'exécution. Fenêtre avant incomplète → **pas de label**.
- **Le balayage avant du labeling n'a pas de pire cas pathologique** : les
  barrières sont dimensionnées par l'ATR, donc elles se resserrent quand le
  marché se calme. Mesuré : zéro barrière temporelle sur un marché agité
  comme sur un marché plat. Ne pas « optimiser » ce balayage par
  décomposition en blocs — ce serait de la complexité sans gain.
- **Devises** : le P&L d'une paire `XXXYYY` naît en `YYY`.
  `data.ConversionFor` convertit si la devise du compte est la base ou la
  cotation ; sinon `CurrencyExact = false` et on ne convertit PAS.
- **Métriques non finies** : `Stats` a un `MarshalJSON` dédié
  (`backtest/json.go`) — NaN → `null`, ±∞ → `"inf"`/`"-inf"`. Sans lui,
  `run.json` refuse d'être écrit dès qu'un pli n'a aucune perte.
- **Rejeu** : horodate au temps du **marché rejoué**, jamais l'heure réelle.
- **bbolt** verrouille le fichier : une seconde instance de `gw` échoue
  proprement (« déjà ouverte par une autre instance ? »). C'est voulu.
- **`RollingStd` recentre ses accumulateurs** dès la première fenêtre
  complète. Sans ce recentrage, la variance d'une série très décalée (un
  OBV cumulé) se calcule par soustraction de deux grands nombres presque
  égaux et perd tous ses chiffres significatifs.

## Dépendances (volontairement minimales)

`bubbletea`, `lipgloss`, `yaml.v3`, `bbolt`, `ulikunitz/xz` — cinq
directes, **aucune native**. `CGO_ENABLED=0` partout : c'est la garantie
de la promesse « un seul binaire ».

⚠ `go.mod` exige **Go 1.25** (imposé par `bbolt` v1.5 et `x/sys` v0.45).
Une tentative de repli sur 1.24 casse la compatibilité entre les paquets
`charmbracelet/x/*` — ne pas la refaire.

## Préférences utilisateur

- Répondre et documenter en **français**.
- Après chaque modification, réfléchir aux **conséquences annexes**
  (config, docs, tests, `.gitignore`, ce fichier).
- Appliquer les améliorations évidentes **sans demander**.

## État du projet (21 septembre 2026)

**Complet de bout en bout, ~13 000 lignes de code + ~4 300 de tests,
20 paquets, suite verte avec `-race`.**

Validé réellement :

- walk-forward 5 plis × 3 paires sur ~18 000 bougies H4 : **3 s**,
  AUC OOS 0,616, modèle de production écrit ;
- backtest CLI et TUI, courbe d'équité braille, tableau des trades ;
- passerelle `replay` en TUI : connexion, flux, agrégation H4, signaux,
  ordres, exécutions, positions, journal des trades ;
- rendu de la TUI vérifié sous tmux à plusieurs tailles.

**Reste à valider chez l'utilisateur** : premier téléchargement Dukascopy
réel (le bac à sable de dev est limité à 429).

**Reste à faire** :

1. **Brancher Interactive Brokers** — seul élément manquant pour que le
   live soit autre chose qu'un rejeu. Feuille de route détaillée dans
   `docs/brokers.md`.
2. **Icône Windows** dans l'exécutable : cible `make windows`, méthode
   `goversioninfo` → `.syso`, commande déjà écrite en commentaire. Le
   workflow de release la ramasse automatiquement si le `.syso` existe.
3. **Exposition croisée inter-actifs** dans le walk-forward : chaque actif
   a son propre moteur, donc les plafonds s'appliquent par actif. Limite
   documentée, pas masquée.

## Journal de décisions

### Conception

- **Interface TUI, pas web.** Supprime le serveur, le navigateur et tout
  le vendoring front.
- **GBDT écrit en Go** plutôt qu'une bibliothèque liée : `cgo` et une
  bibliothèque native ruineraient la promesse du binaire unique. Bonus
  inattendu : reproductibilité au bit près, vérifiée par un test.
- **Moteur de backtest sur mesure** (~400 lignes) : chaque hypothèse
  d'exécution devient explicite au lieu d'être un réglage obscur.
- **bbolt plutôt que SQLite** : trois collections lues par clé n'ont pas
  besoin de SQL, et SQLite en Go coûterait `cgo`.
- **Format `.gwb` plutôt qu'un format colonne générique** : zéro
  dépendance, sans perte, deux fois plus compact, lecture par seek.
- **`OnBar` plutôt que `on_tick`** : l'agrégation tick→bougie est faite
  UNE fois par le moteur live. Backtest et live entrent par la même porte.
- **Environnement > YAML** : une variable exportée doit agir, sinon elle
  piège.
- **Données dans le dossier utilisateur**, jamais à côté du binaire.
- **Modèle de production entraîné sur tout l'historique**, écrit à la fin
  du walk-forward : c'est lui qui trade, et les plis disent s'il le mérite.
- **Taille de position par défaut : 10 000 unités** (mini-lot). À 1 unité,
  tous les P&L affichés étaient des poussières illisibles.
- **Avertissement IN-SAMPLE permanent** sur l'écran Backtest.

### Bugs corrigés (et pourquoi ils comptaient)

- **Course de données dans le bus** : `Publish` parcourait la tranche
  d'abonnés pendant qu'un `Close` la réécrivait, et pouvait envoyer sur un
  canal fermé — panique, donc arrêt brutal. Corrigé par une tranche
  immuable et un verrou tenu pendant l'envoi non bloquant. Test de
  régression : `TestBusCloseDuringPublishIsSafe`.
- **AUC out-of-sample d'un pli mal moyennée** : `moy = (moy + auc) / 2`
  pondère 1/4, 1/4, 1/2 sur trois actifs — le dernier évalué décidait de la
  moitié du chiffre. Remplacé par somme et compteur.
- **Modèle rechargé sans vérifier les noms de colonnes** : seul le nombre
  était contrôlé. Un modèle aux colonnes permutées aurait produit des
  probabilités crédibles et fausses, sans rien pour le trahir.
- **Deux fills du même sens journalisés comme un aller-retour** : cela
  fabriquait un trade inexistant avec un P&L calculé entre deux entrées.
  Désormais signalé, et rien n'est journalisé.
- **Année téléchargée à moitié jamais complétée** : elle était
  indiscernable d'une année entière et la relance la sautait. L'en-tête
  porte maintenant le nombre de jours en échec.
- **Métriques GBDT calculées sur tous les arbres** au lieu de s'arrêter à
  `BestIteration` — elles décrivaient un modèle qui ne prédira jamais.
- **Compteurs de rejet du risque écrits sans verrou** : un SEUL
  `risk.Manager` est câblé dans `app.New` et partagé par le backtest, le
  walk-forward (plis parallèles) et le moteur live (une goroutine de rejeu
  par symbole). `counts[motif]++` sur une map non protégée : course
  confirmée au détecteur, et risque de `fatal error: concurrent map
  writes` — une panique du runtime, irrattrapable, au milieu d'un
  entraînement ou d'une séance. Corrigé par un `sync.Mutex`. Test de
  régression : `TestConcurrentEvaluateIsSafe`.
- **Recentrage de `RollingStd` jamais établi sur une série courte** : bug
  introduit pendant l'optimisation et attrapé par la comparaison avec
  l'implémentation naïve. C'est précisément à ça qu'elle sert.

### Performance (mesurée, pas supposée)

Bancs d'essai : `internal/indicator/bench_test.go`, `internal/ml/gbdt/bench_test.go`.

| Changement | Avant | Après |
|---|---|---|
| `RollingMax` fenêtre 50, 400 k points (file monotone) | 62,5 ms | 12,7 ms |
| `RollingMin` fenêtre 20, 400 k points | 35,7 ms | 12,7 ms |
| `RollingMean` fenêtre 20 (somme simple + resync) | 2,57 ms | 1,89 ms |
| Entraînement GBDT, mémoire allouée | 3 855 Mo | **72,6 Mo** |
| Entraînement GBDT, allocations | 226 000 | 44 000 |
| Entraînement GBDT, temps (4 cœurs) | 4,41 s | 3,84 s |
| Walk-forward de bout en bout, échantillon CPU | 1 980 ms | 1 130 ms |

Ce qui a produit ces gains : file monotone pour les extrema glissants,
recyclage des histogrammes du GBDT, partition en place (plus d'allocation
par coupure), seuil en deçà duquel la parallélisation des histogrammes
coûte plus qu'elle ne rapporte, pré-dimensionnement du chargement de
l'historique depuis les en-têtes, et mise en cache des lectures de la TUI
(trades, graphique ré-échantillonné).

Ce qui a été mesuré puis **écarté** : la décomposition en blocs du
balayage du labeling — le pire cas redouté n'est pas atteignable, les
barrières suivant l'ATR (voir plus haut).
