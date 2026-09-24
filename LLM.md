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
- **Colibri** = uniquement le **moteur de décision**, entièrement dans
  `internal/strategy/colibri/` (features, cible, décision, modèles).
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

`train` accepte des PAIRES en positionnel (`gw train EURUSD GBPUSD`) et
`--risk-per-trade X`, qui force le dimensionnement pour CE run — c'est
ainsi qu'on mesure l'effet du réglage sur ses propres données, deux runs
et un `gw runs`.

`migrate [--remove]` convertit les anciens `.gwb` en Parquet ;
`import --symbol S FICHIER…` verse un historique venu d'ailleurs.

`backtest` accepte `--csv` : trades, courbe de valeur et métriques partent
dans `<données>/exports/`. Même sortie que la touche `e` de l'écran
Backtest, et que `e` sur l'onglet trades du Journal.

`download` accepte `--year A` ou `--from A --to B` pour ne prendre qu'une
partie de l'historique. Les options sont remises devant les paires avant
d'être parsées (`partitionArgs`) : le paquet `flag` s'arrête au premier
argument positionnel et ignorerait sinon `gw download EURUSD --year 2019`
en silence.

Six écrans : **Live**, **Données**, **Backtest**, **Entraînement**,
**Journal**, **Paramètres**.

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
8. **Une stratégie est un module remplaçable.** Moteurs (backtest, live),
   walk-forward, TUI et CLI ne connaissent que `strategy.Strategy` et sa
   `Description` ; AUCUN n'importe le paquet d'une stratégie. Ce qu'ils
   supposaient de Colibri, la stratégie le DÉCLARE : `ContextBars`
   (chauffe), `MaxHold` (barrière verticale, 0 = aucune), et le manifeste
   `strategy.ModelManifest` (seul fichier que le catalogue regarde). Les
   règles d'exécution dont une cible a besoin (fin de semaine ISO,
   barrière verticale, spread médian) vivent dans `core/horizon.go`. Le
   catalogue `internal/strategies` est le SEUL paquet qui nomme une
   implémentation ; toute stratégie qui y figure passe d'office le banc
   `strategy/strategytest`.

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
| `Info.SupportsBracket` + `Stats.UnprotectedRefused` | broker, live | Ouvrir une position nue en affichant un stop qui n'existe nulle part |
| `Rejections` par run (`risk.Manager.Fork`) | backtest | Publier un cumul de tous les runs comme s'il décrivait celui-ci |
| `MaxDrawdownPct`/`Sharpe` à NaN dans l'agrégat | backtest | Lire « 0 % de drawdown » là où la vérité est « non mesuré » |
| Refus de dimensionnement (4 motifs) | risk | Risquer, faute de mesure, un montant que personne n'a choisi |
| Brouillon + « prend effet au démarrage » | TUI Paramètres | Croire qu'un réglage modifié s'applique à la séance en cours |
| Réglage forcé par `GW_*`, non modifiable | TUI Paramètres | Éditer une valeur que l'environnement réécrira |
| `Validate()` avant écriture | TUI Paramètres | Transformer un réglage maladroit en démarrage impossible |
| `Table` : colonne retirée annoncée (`+N col.`) | TUI | Prendre une information absente pour une information inexistante |
| Badges d'état jamais supprimés | TUI | Perdre « LIVE — ARGENT RÉEL » en réduisant la fenêtre |
| Période de téléchargement toujours affichée | TUI Données | Croire télécharger tout l'historique |
| `component.Fit` : lignes masquées annoncées | TUI | Perdre l'entête et « q quitter » hors de l'écran, sans un mot |
| `StatRowMax` : cartes écartées comptées (`+N`) | TUI | Confondre « pas affiché » et « sans objet » |
| Filtre du Journal rappelé à l'écran | TUI Journal | Prendre un filtre oublié pour un programme silencieux |
| Cellule VIDE pour une métrique non mesurée | export CSV | Laisser un tableur additionner un zéro inventé |
| `couts_modelises` / `devise_exacte` exportés | export CSV | Additionner des chiffres qui ne sont pas additionnables |
| Export = copie, jamais recalcul | export CSV | Un fichier qui contredit l'écran qui l'a produit |
| `theme.Apply` force réellement le fond | TUI | Une clé de configuration qui n'agit pas |
| `Decision.Capped` + `Stats.SizeCapped` | risk, backtest | Annoncer « 0,5 % par trade » quand le plafond rabote chaque entrée |
| `risk.SizingRefusals` affiché par écran | risk, TUI, CLI | Confondre « refusée faute de devise » et « le modèle s'abstient » |
| `SplitByConversion` au démarrage et dans `gw config` | app, CLI | Découvrir dans un agrégat vide que 21 paires sur 31 ne tradent pas |
| Paire annotée dans le sélecteur | TUI | Choisir une paire qui ne produira rien |
| Côté ask absent écrit `NULL`, jamais `0` | data | Qu'un outil tiers additionne un prix inventé |
| `gw.failures` en métadonnée Parquet | data | Prendre une année trouée pour une année faite |
| `FileHeader.Imported` | data | Déclarer complète une année dont personne n'a compté les jours |
| Colonnes appariées PAR LEUR NOM | data | Lire des prix crédibles et faux dans un fichier tiers |
| Relecture bougie à bougie avant suppression | data (migrate) | Effacer un historique sur une conversion non vérifiée |
| Stabilité par préfixe des DÉCISIONS (`strategytest`) | toute stratégie | Une décision qui dépend des bougies futures alors que chaque feature est causale |
| Cible v1_2 confrontée au moteur de backtest | colibri | Apprendre des gains que l'exécution ne verse pas |
| `costs_modelled`, `purged`, `calibrated` au manifeste | colibri | Croire nette une cible brute, ou calibrée une probabilité brute |
| Modèle rangé PAR PAIRE dans une instance | colibri | Décider EURUSD avec le modèle d'USDJPY |
| `ibCheckMode` (compte « D… » = papier) + devise vérifiée | broker IB | Trader un compte réel en « paper », ou afficher ARGENT RÉEL sur un compte papier |
| Sortie IB = annulation CONFIRMÉE des barrières, puis marché | broker IB | Un stop orphelin qui rouvre une position |
| `checkProtection` (barrière perdue → fermeture) | broker IB | Une position protégée à l'écran et nue chez le courtier |
| `ExecutionReport.Closing` | core, live | Prendre la sortie d'une position d'avant le démarrage pour une entrée |
| `Position.UnrealizedKnown` | core, TUI | Afficher 0 de P&L latent quand le courtier ne le donne pas |
| Messages IB identiques OCTET POUR OCTET au client officiel | broker IB | Un protocole écrit de mémoire |

## Conventions

- **Commentaires en français**, identifiants en anglais (convention Go).
  Un commentaire dit POURQUOI, pas QUOI.
- **Un paquet = une responsabilité.** Le fichier ne répète pas le paquet
  (`risk/manager.go`, pas `risk/risk_manager.go`).
- **Tests en miroir** : `internal/<paquet>/<fichier>_test.go`.
- **Aucune couleur littérale hors de `internal/tui/theme`.**
- **Un écran MESURE sa mise en page, il ne la devine pas.** Additionner de
  tête bordures, titres et enroulements se trompe d'une à cinq lignes.
  `component.FitBlock(budget, min, max, render)` réduit un bloc jusqu'à ce
  qu'il tienne ; `component.Fit` coupe en DERNIER recours, en disant
  combien de lignes manquent.
- **Largeurs TUI** : `component.PanelContent(width)` donne la largeur
  intérieure d'un panneau ; aucune vue ne la recalcule de tête. `Table`
  reçoit cette largeur et arbitre ses colonnes (`Flex`/`Min` pour
  comprimer, `Priority` pour sacrifier, priorité 0 = jamais retirée).
- **Touches** : ← et → appartiennent aux ÉCRANS, pas au routeur. Un écran
  qui saisit du texte l'annonce par `view.KeyCapturer` ; seul Ctrl+C reste
  global.
- **Aucun `panic` sur une donnée** ; seulement sur une incohérence de code.
- Nouvelle passerelle : `Register()` dans un `init()`, aucun autre fichier
  à modifier. Nouvelle stratégie : son paquet `strategy/<oiseau>/`,
  `Register()` dans un `init()`, et UNE ligne dans
  `internal/strategies/strategies.go`.
- **Une optimisation se mesure.** Un banc d'essai avant, un après, et pour
  une réécriture numérique une comparaison contre une implémentation naïve
  de référence (`indicator_test.go`). Sans ça, ce n'est qu'une croyance.

## Points techniques à ne pas réapprendre

- **Dukascopy** : mois **0-based** dans les URLs ; `.bi5` = LZMA, champs
  dans l'ordre **`offset, open, CLOSE, LOW, HIGH, volume`** (pas OHLC) ;
  404 = marché fermé ; 429 = limite de débit → backoff LONG,
  concurrence 3. Week-ends jamais demandés.
- **Stockage en Parquet** (`data/parquet.go`). Colonnes `time`
  (TIMESTAMP MILLIS UTC), `bid_*`, `ask_*` (NULLABLES), `volume`. Les
  métadonnées portent `gw.failures` — le nombre de jours en échec, dont
  `Complete()` dépend. Groupes de lignes de 32 768 bougies (≈ un mois) :
  une lecture bornée saute les groupes hors période, ce qui remplace le
  seek de l'ancien format. Les prix sont ARRONDIS avant écriture, mais
  seulement si l'aller-retour est exact (`rounder` monte l'échelle par
  décades) : sans arrondi le fichier triple, avec un arrondi au jugé on
  perdrait les cotations d'une source plus fine.
- **L'ancien `.gwb` est en LECTURE SEULE** (`data/gwb.go`). Rien ne peut
  plus l'écrire — un format qu'on ne peut plus produire ne peut plus se
  répandre — et `writeLegacySeries` vit dans les tests, pour que le
  lecteur reste éprouvé contre de vrais octets. `NeedsDownload` regarde
  les DEUX formats : sinon quinze ans d'historique repartiraient en
  téléchargement pour cause de changement d'extension.
- **Lire un Parquet écrit ailleurs** : les colonnes sont appariées par
  NOM avec des alias, l'unité d'horodatage vient du type logique (à
  défaut, de l'ordre de grandeur — les plages s/ms/µs/ns ne se chevauchent
  pour aucune date plausible), et pyarrow déclare TOUTES ses colonnes
  nullables, donc le lecteur ne peut pas compter sur `Int64Reader`.
  `testdata/foreign_EURUSD_m1_2021.parquet` est un vrai fichier pyarrow :
  c'est lui qui a trouvé ces deux pièges.
- **Anti-fuite** : le test de **stabilité par préfixe**
  (`Compute(s)[:k] == Compute(s[:k])`) est le garde-fou central. Ne jamais
  le désactiver.
- **Labeling symétrique (v1_0, v1_1)** : deux barrières dans la même
  bougie → **la basse**. Juste pour un long, FAUX pour un short : le
  moteur compte ce cas perdant pour le short aussi (stop prioritaire),
  alors que la cible le lui donnait gagnant. Corrigé en v1_2 par une tête
  par sens (`label.Sided`) — jamais par une retouche des révisions
  publiées. Fenêtre avant incomplète → **pas de label**, toutes révisions.
- **Les règles de sortie vivent dans `core/horizon.go`**
  (`HoldDeadline`, `HoldExpired`, `LastBarsOfWeek`, `MedianSpread`) et les
  moteurs ET l'étiquetage v1_2 les appellent. Écrite deux fois, une règle
  de barrière finit par diverger. `HoldExpired` est formulée sur la bougie
  courante et la cadence du flux, jamais sur l'horodatage de la bougie
  suivante : le live ne l'aurait pas. La bougie qui COMMENCE à l'échéance
  ferme la position.
- **Cible et exécution** : en v1_0/v1_1, l'étiquetage accorde cinq jours
  calendaires alors que la clôture de fin de semaine ISO ferme avant — la
  barrière verticale ne se déclenche jamais sur des données forex. En
  v1_2, `label.ExecutionWindow` prend la PREMIÈRE des deux règles du
  moteur, et `TestSidedTargetIsWhatTheEngineDelivers` confronte la cible
  au vrai moteur de backtest sur 60 entrées. ⚠ Le LIVE n'a toujours pas de
  règle de fin de semaine (une bougie n'y est close qu'au premier tick du
  bucket suivant, donc la dernière du vendredi à la réouverture) : la
  divergence subsiste là, et la barrière de cinq jours y reste le seul
  filet.
- **Aucune entrée sur la dernière bougie de la semaine** (backtest,
  depuis v0.4.1) : elle était portée tout le week-end, la clôture de fin
  de semaine ne se rejouant qu'à la semaine suivante.
- **Calibrage v1_2 = rétrécissement vers le taux de base**
  (`gbdt.FitShrinkage`, `A ∈ [0, 1]`, centre FIXE). Deux versions plus
  libres ont été MESURÉES puis écartées : pente libre (1,2 à 2,2 sur une
  marche au hasard — amplifiait le bruit) et ordonnée libre (absorbait la
  tendance de la validation, signe alterné d'un pli à l'autre). Un
  calibrage ne retire que de la confiance ; il n'ajoute jamais d'opinion.
- **La marge 0,10 R de v1_2 est choisie sur SYNTHÉTIQUE** (ablation,
  `GW_ABLATION=1`, 4 marchés × 8 graines) : seule marge au moins aussi
  bonne que v1_1 en P&L total ET moins perdante qu'elle sans signal —
  critère formulé APRÈS la mesure. ⚠ **v1_2 ne domine pas v1_1** : sur le
  marché au signal le plus facile (rappel horaire, AUC 0,586), v1_1 fait
  mieux (PF 1,46 contre 1,36). Tableau complet : `docs/colibri.md`.
- **Correction de fuite ≠ heuristique.** La purge est gardée même sans
  effet mesurable (une fuite ne se garde pas parce qu'elle ne coûte rien
  ici) ; les poids d'unicité, heuristique, ont été écartés parce que la
  mesure ne les soutenait pas.
- **Stops et gaps** : un stop déclenché est un ordre AU MARCHÉ, rempli au
  pire de la barrière et de l'ouverture. Une limite, elle, garde son prix
  exact : un ordre à cours limité ne s'exécute jamais moins bien, et lui
  accorder le gap serait s'attribuer une chance invérifiable.
- **Le balayage avant du labeling n'a pas de pire cas pathologique** : les
  barrières sont dimensionnées par l'ATR, donc elles se resserrent quand le
  marché se calme. Mesuré : zéro barrière temporelle sur un marché agité
  comme sur un marché plat. Ne pas « optimiser » ce balayage par
  décomposition en blocs — ce serait de la complexité sans gain.
- **Dimensionnement au risque, actif par défaut (0,5 %)**. Deux clés
  distinctes : `max_position_size` est une GARDE (aucune entrée ne la
  dépasse, quel que soit le mode), `fixed_position_size` est la taille
  quand le risque par trade vaut 0. Les confondre — ce qu'elles faisaient
  — obligeait à relever le plafond pour activer le risque, ce qui
  décuplait la taille fixe dès qu'on le désactivait.
  ⚠ **0,5 % est une CONVENTION, pas une mesure** : le bac à sable de dev
  n'a pas d'historique réel (Dukascopy y répond 429). `gw train
  --risk-per-trade X` existe pour que l'utilisateur le mesure chez lui.
- **Conséquence lourde du dimensionnement** : sur un compte en USD, 21 des
  31 instruments par défaut ne sont pas convertibles, donc TOUTES leurs
  entrées sont refusées. C'est la règle « refuser plutôt que deviner »,
  et elle est criée à quatre endroits (journal de démarrage, `gw config`,
  sélecteur de paires, écrans Backtest et Entraînement). La lever pour de
  bon demande la TRIANGULATION par une paire tierce — voir reste-à-faire.
- **Devises** : le P&L d'une paire `XXXYYY` naît en `YYY`.
  `data.ConversionFor` convertit si la devise du compte est la base ou la
  cotation ; sinon `CurrencyExact = false` et on ne convertit PAS.
- **Métriques non finies** : `Stats` a un `MarshalJSON` dédié
  (`backtest/json.go`) — NaN → `null`, ±∞ → `"inf"`/`"-inf"`. Sans lui,
  `run.json` refuse d'être écrit dès qu'un pli n'a aucune perte.
- **Rejeu** : horodate au temps du **marché rejoué**, jamais l'heure réelle.
- **bbolt** verrouille le fichier : une seconde instance de `gw` échoue
  proprement (« déjà ouverte par une autre instance ? »). C'est voulu.
- **`Resample` ne calcule un plancher qu'au CHANGEMENT de bucket.** Le
  test « bucket ≤ t < suivant » est exactement équivalent à
  `Floor(t) == bucket` pour toutes les unités livrées — y compris sur une
  série hors d'ordre — et coûte deux comparaisons au lieu d'une division
  64 bits. La capacité de sortie suit la DURÉE couverte, pas
  `len(series)/4` : cette hypothèse ne vaut que pour M1→M5 et réservait
  8,9 Mo pour produire 1 560 bougies en H4. Témoin de non-régression :
  `resampleNaive` dans `data/bench_test.go`.
- **`ui.theme` vaut « auto », « dark » ou « light ».** Les couleurs sont
  adaptatives ; ce qui change entre clair et sombre n'est pas la palette
  mais la réponse à « le fond est-il sombre ? ». `theme.Apply` la FIXE via
  `lipgloss.SetHasDarkBackground` ; `theme.ByName` reste pur, un
  accesseur à effet de bord finissant toujours par surprendre. « auto »
  ne touche à rien : la détection a raison presque partout, et forcer
  sans raison rend l'interface illisible chez quelqu'un d'autre.
- **`RollingStd` recentre ses accumulateurs** dès la première fenêtre
  complète. Sans ce recentrage, la variance d'une série très décalée (un
  OBV cumulé) se calcule par soustraction de deux grands nombres presque
  égaux et perd tous ses chiffres significatifs.

## Dépendances (volontairement minimales)

`bubbletea`, `lipgloss`, `yaml.v3`, `bbolt`, `ulikunitz/xz`,
**`parquet-go/parquet-go`** — six directes en production, **aucune
native**.

Parquet est la dépendance la plus lourde du projet : elle amène neuf
modules transitifs et fait passer le binaire de **8,8 à 15,2 Mo**. C'est
le prix d'un format de données que d'autres outils savent lire, et il a
été payé volontairement — un format maison enferme l'utilisateur dans le
logiciel qui l'a écrit. Le binaire reste unique, statique et sans `cgo`,
et les cinq cibles compilent toujours. `muesli/termenv`, déjà
transitive via lipgloss, est devenue directe pour les SEULS tests du
thème : forcer un profil de couleur est la seule façon de vérifier que
`ui.theme` change vraiment ce qui sort à l'écran, et un test qui se
contenterait de lire un drapeau ne vaudrait rien. `CGO_ENABLED=0` partout : c'est la garantie
de la promesse « un seul binaire ».

CI : format, `go vet`, tests `-race`, **govulncheck** (vulnérabilités
réellement atteignables) et compilation croisée des quatre cibles.
Dependabot surveille modules et actions chaque semaine, les paquets
`charmbracelet/x/*` étant GROUPÉS — les mettre à jour séparément produit
des états qui ne compilent pas.

Licence **MIT** (`LICENSE`), choisie par le propriétaire du projet.

⚠ `go.mod` exige **Go 1.25** (imposé par `bbolt` v1.5 et `x/sys` v0.45).
Une tentative de repli sur 1.24 casse la compatibilité entre les paquets
`charmbracelet/x/*` — ne pas la refaire.

## Préférences utilisateur

- Répondre et documenter en **français**.
- Après chaque modification, réfléchir aux **conséquences annexes**
  (config, docs, tests, `.gitignore`, ce fichier).
- Appliquer les améliorations évidentes **sans demander**.

## État du projet (23 septembre 2026, v0.4.1)

**Complet de bout en bout, ~18 500 lignes de code + ~8 700 de tests,
24 paquets, suite verte avec `-race`.**

Couverture par paquet (la plus basse d'abord, mesurée en v0.4.1) :
`cmd/gw` 26 %, `tui/view` 48 %, `config` 58 %, `core` 67 %, `tui` 68 %,
`data` 70 %, `broker` 75 %, `training` 75 %, `indicator` 77 %,
`storage` 79 %, `tui/component` 79 %, `app` 79 %, `live` 80 %,
`ml/gbdt` 83 %, `backtest` 84 %, `strategy/colibri` 87 %, `export` 88 %,
`risk` 90 %, `label` 97 %, `tui/theme` 100 %. Sans instruction propre
(couverts par les tests des autres) : `feature`, `strategy`,
`strategytest` ; `strategies` n'a que le banc de conformité.

Validé réellement :

- walk-forward et backtest de bout en bout sur un historique **importé
  depuis un fichier pyarrow** : import → 2 ans de M1 → 3 plis → modèle de
  production → backtest → export CSV. Le spread mesuré à la sortie
  (0,000080) prouve que le côté ask a survécu à l'aller-retour ;
- fichier Parquet écrit par le programme **relu par pyarrow** : types,
  colonnes nullables et métadonnées `gw.*` conformes ;
- rendu de la TUI CONTRÔLÉ par test en largeur ET en hauteur, de 60×18 à
  200×60 ;
- sélecteur de paires exercé sous tmux : filtre « JPY » → 7 paires, « a »
  les coche, annotation « non dimensionnable en USD » visible au moment
  du choix.

**Reste à valider chez l'utilisateur** : premier téléchargement Dukascopy
réel (le bac à sable de dev est limité à 429), et surtout la MESURE de
`risk_per_trade_pct` sur des données réelles.

**Reste à faire**, par ordre de valeur :

0. **Mesurer `colibri_v1_2` sur historique réel** contre `v1_1` (deux
   `gw train` sur les mêmes paires, `gw runs` compare). Toutes les mesures
   de v0.4.1 sont SYNTHÉTIQUES : la marge 0,10 R, le rejet des poids
   d'unicité et celui du calibrage libre en dépendent. Un résultat réel
   qui les contredit donne une révision `colibri_v1_3`, jamais une
   retouche de v1_2.
1. **Triangulation des devises.** C'est devenu la limite la plus coûteuse
   du programme : sur un compte en dollars, 21 des 31 instruments par
   défaut ne sont pas convertibles, leur P&L reste en devise de cotation
   (`CurrencyExact = false`) et, depuis que le dimensionnement au risque
   est actif, TOUTES leurs entrées sont refusées. Le taux manquant est
   pourtant **déjà sur le disque** : convertir un P&L en GBP vers l'USD
   demande GBPUSD, que l'historique contient. Ce qu'il faut : une source
   de taux alignée dans le temps (une deuxième série chargée par le
   moteur de backtest, une cotation supplémentaire côté passerelle en
   live), et `Conversion` qui prenne un instant. Attention : cela change
   des résultats déjà publiés — à traiter comme un changement de moteur,
   pas comme un correctif.
2. **Mesurer `risk_per_trade_pct`.** Le réglage est actif à 0,5 %, valeur
   de CONVENTION. `gw train --risk-per-trade 0` puis `0.5` sur le même
   historique, et `gw runs` compare. Tant que ce n'est pas fait, le
   chiffre par défaut n'est adossé à aucune mesure — et le dire est la
   moitié du travail.
3. **Éprouver Interactive Brokers contre un VRAI TWS papier** (v0.5.0).
   La passerelle est écrite et testée contre le client officiel et un faux
   TWS, jamais contre un vrai : entrée, stop, limite, sortie sur signal,
   redémarrage, puis comparer le journal au relevé IB.
4. **Icône Windows** : mécanique en place, il manque le fichier
   `build/icon.ico` (256×256) — décision de design, pas de code.
5. **Exposition croisée inter-actifs** dans le walk-forward : les
   plafonds s'appliquent par actif, et l'agrégat renvoie NaN pour
   drawdown et Sharpe faute de courbe de valeur commune.
6. **60×18 ne suffit pas à l'écran Live.** Entre le plancher déclaré et
   80×24, le corps est coupé et `Fit` le dit. Relever le plancher, ou
   abréger les panneaux explicatifs sous 70 colonnes — pas supprimer
   l'avertissement.
7. **Import CSV.** L'import lit le Parquet ; beaucoup d'outils exportent
   en CSV. Le lecteur de colonnes par alias est déjà écrit, il ne
   manquerait que le décodage.

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
- **Dimensionnement au risque OPTIONNEL** (`risk_per_trade_pct`, 0 par
  défaut). À taille fixe, la perte au stop suit l'ATR : elle double quand
  la volatilité double, sans que personne ne l'ait décidé. Le réglage
  inverse la dépendance — c'est la taille qui bouge, la perte qui reste
  constante. Laissé à 0 parce qu'une taille variable change le SYSTÈME
  (drawdown, profit factor, SQN) : cela se mesure, cela ne se décide pas à
  la place de l'utilisateur. Ce qui manque (équité, stop, conversion)
  REFUSE l'entrée au lieu de replier sur la taille maximale.
- **Une passerelle déclare ses capacités** (`Info.SupportsBracket`)
  plutôt que le moteur ne les suppose. Le contraire ouvrait des positions
  nues avec un stop affiché à l'écran et nulle part ailleurs.
- **Licence MIT.** Sans fichier `LICENSE`, un dépôt public reste « tous
  droits réservés » sans que rien ne le dise.
- **L'écran Paramètres travaille sur un BROUILLON.** Le risque, le moteur
  live et le moteur de backtest reçoivent leur configuration au démarrage
  et ne la relisent pas. Appliquer un réglage à chaud donnerait un
  programme dont la moitié des composants obéit à une configuration et
  l'autre moitié à une autre. L'écran écrit `config.yaml` et répète que
  cela prend effet au prochain démarrage.
- **`ui.theme` agit ou disparaît.** La clé existait, l'écran Paramètres
  l'exposait, et elle ne faisait RIEN : `Light()` renvoyait `Dark()` et
  personne n'appelait `SetHasDarkBackground`. C'est le piège que la règle
  « une variable exportée doit agir » interdit. Elle agit désormais, et
  une troisième valeur — « auto », devenue le défaut — dit explicitement
  « laisse la détection décider » au lieu de le faire en douce sous le nom
  « dark ».
- **L'export CSV ne recalcule RIEN.** Un export qui refait un cumul peut
  présenter un chiffre différent de l'écran qui l'a produit. Il recopie,
  champ pour champ, ce que le programme a déjà mesuré — et emporte avec
  les chiffres les drapeaux qui disent s'ils sont additionnables.
- **Convention CSV francophone, assumée** : séparateur `;`, décimale `,`,
  marque d'ordre des octets. C'est ce qu'un tableur francophone ouvre d'un
  double-clic. Le prix est que pandas demande `sep=";", decimal=","` ;
  c'est écrit dans `docs/donnees.md` et en tête du paquet. Le choix
  inverse aurait transformé « 1.25 » en date chez l'utilisateur du
  projet.
- **Ce qui est exporté est ce qui est AFFICHÉ**, filtre compris. Un
  fichier dont le contenu ne correspond pas à l'écran qui l'a produit est
  un piège, et la ligne d'état dit combien de trades sont partis.
- **Un écran mesure sa mise en page.** `FitBlock` rend un panneau une
  seconde fois plutôt que de faire confiance à une soustraction de
  constantes. Mesuré : ≈ 0,6 ms par écran, un millième de la cadence de
  rafraîchissement. C'est dérisoire devant un affichage faux.
- **L'aide est une fenêtre MODALE et défilable.** Tant qu'elle couvre
  l'écran, les touches lui appartiennent — sinon les flèches faisaient
  défiler un tableau invisible derrière elle.
- **Parquet plutôt qu'un format maison.** `.gwb` était plus compact à
  écrire et se relisait par seek — et n'était lisible que par ce
  programme. Impossible d'y verser un historique téléchargé ailleurs,
  impossible de l'ouvrir dans un notebook. Un format de données qui
  enferme son utilisateur dans le logiciel qui l'a écrit est l'inverse de
  ce que ce projet promet. Mesuré : fichier 9,2 Mo contre 14,9, lecture
  139 ms contre 26, écriture 485 ms contre 68 — la lecture se produit une
  fois par entraînement et l'écriture derrière un téléchargement réseau,
  tandis que la taille reste sur le disque pour toujours.
- **Le côté ask absent s'écrit NULL, pas zéro.** Zéro serait un prix, et
  l'outil tiers qui ouvre le fichier l'additionnerait. C'est la règle
  « — plutôt que zéro » de l'interface, appliquée au fichier.
- **Les `.gwb` restent lus, rien ne les écrit plus.** Casser la lecture
  d'un format qu'on abandonne reviendrait à demander de retélécharger
  quinze ans d'historique parce qu'on a changé d'avis. `gw migrate` les
  convertit, et ne supprime qu'APRÈS relecture bougie à bougie.
- **Un import est marqué comme tel.** Personne n'a compté les jours
  manquants d'un fichier venu d'ailleurs : `Complete()` répond non plutôt
  que de déclarer faite une année dont on ne sait rien.
- **`max_position_size` et `fixed_position_size` sont deux clés.** Une
  seule valeur servait de plafond dans un mode et de taille exacte dans
  l'autre : activer le dimensionnement au risque obligeait à relever ce
  nombre, ce qui décuplait la taille fixe dès qu'on le désactivait. Deux
  sens pour une valeur, c'est un piège, pas une économie.
- **Une taille rabotée par le plafond est COMPTÉE.** Sans ce compteur, un
  plafond trop bas neutralise le dimensionnement au risque à chaque
  entrée et l'écran continue d'annoncer « 0,5 % par trade » en toute
  bonne foi.
- **Le sélecteur de paires annote ce qu'il sait.** Une paire sans
  historique local, ou non dimensionnable dans la devise du compte, ne
  produira rien. Le dire AU MOMENT DU CHOIX, et non dans un agrégat vide
  deux heures plus tard, est tout l'intérêt d'avoir remplacé le champ de
  texte.
- **Entraîner un sous-ensemble de paires.** Un walk-forward sur trente et
  une paires dure des heures ; pouvoir n'en reprendre qu'une après avoir
  changé un réglage est la différence entre essayer et renoncer.
- **README abrégé, détail dans `docs/`.** Les tableaux de garanties et de
  pannes vivent dans `docs/depannage.md` : un lecteur qui découvre le
  projet n'en a pas besoin avant d'avoir lancé le binaire.

- **Stratégie modulaire (v0.4.1).** Colibri vivait dans trois paquets
  partagés (`feature`, `label`, `strategy`) et les moteurs importaient ses
  constantes : `label.MaxHoldDays` dans le backtest et le live,
  `feature.ContextBars` dans le walk-forward, la TUI et la CLI, et le
  catalogue cherchait `model.json`. Remplacer le moteur aurait demandé de
  réécrire les moteurs. Désormais tout Colibri est dans
  `strategy/colibri/`, la stratégie DÉCLARE contexte et horizon, et le
  catalogue `internal/strategies` est le seul endroit qui la nomme. La
  refonte a été vérifiée par une empreinte : modèles, signaux et AUC de
  v1_0 et v1_1 identiques AU BIT PRÈS avant et après.
- **Un banc de conformité plutôt qu'une liste de consignes.** Ce qu'une
  stratégie doit à ses moteurs (silence sans modèle, manifeste, barrières
  du bon côté, stabilité par préfixe des décisions) est un test que le
  catalogue fait tourner sur chacune. Une consigne s'oublie ; un test
  qui échoue, non.
- **`colibri_v1_2`, par défaut pour les nouvelles installations.** Une
  config existante qui nomme `colibri_v1_1` le garde : ses modèles ne se
  chargeraient pas sous un autre nom. Ce que v1_2 change et pourquoi :
  `docs/colibri.md`, section « Ce que l'analyse de v1.1 a trouvé ».
- **Mesurer avant de garder un ingrédient.** v1_2 a été construite par
  ablation (`GW_ABLATION=1`), et deux ingrédients écrits, testés et
  théoriquement fondés n'y sont pas entrés : les poids d'unicité (égaux
  ou moins bons sur les quatre marchés) et le calibrage libre (pente :
  amplifiait le bruit ; ordonnée : pariait sur la tendance récente). Les
  poids restent disponibles, inactifs, documentés comme tels.

### Bugs corrigés (et pourquoi ils comptaient)

- **Le short apprenait des gains fictifs** (v1_0, v1_1 — non corrigé EN
  PLACE, puisque publiées) : la cible symétrique comptait « deux
  barrières dans la même bougie » comme un short gagnant, que le moteur
  compte perdant. Corrigé par la cible par côté de v1_2.
- **Entrée possible sur la dernière bougie de la semaine** (backtest) :
  la position traversait le week-end, précisément ce que la clôture de fin
  de semaine interdit. Change les résultats de backtest de toutes les
  stratégies, dans le sens prudent.
- **Un modèle par paire écrasé par le suivant** (live, `colibri_v1_0`) :
  une seule instance de stratégie chauffait toutes les paires et ne
  gardait qu'UN modèle — le dernier chargé décidait pour toutes. Test de
  régression : `TestPerSymbolModelsDoNotOverwriteEachOther`.

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
- **Compteurs de rejet publiés comme des mesures alors qu'ils
  cumulaient** : un seul `risk.Manager` sert tout le programme, donc
  `Stats.Rejections` portait le cumul depuis le démarrage, et
  `AggregateStats` additionnait ensuite ces cumuls chevauchants pli par
  pli. Le nombre de `run.json` ne mesurait rien et changeait d'une
  exécution à l'autre selon l'ordonnancement des plis parallèles.
  `Fork()` donne des compteurs propres à chaque run.
- **Stops remplis au prix exact malgré un gap** : la seule hypothèse du
  backtest qui jouait en faveur du résultat. Un stop déclenché est un
  ordre au marché, servi à l'ouverture quand la bougie ouvre au-delà.
- **Barrière verticale absente de l'exécution** : l'étiquetage pose trois
  barrières, les moteurs n'en tenaient que deux. Une position pouvait
  courir au-delà de l'horizon sur lequel le modèle a été entraîné — sans
  conséquence en backtest (le week-end plafonne déjà), mais bien réelle en
  live.
- **Drawdown et Sharpe agrégés affichés à zéro** : ils n'étaient jamais
  calculés faute de courbe de valeur commune. Zéro se lit « aucun
  drawdown » ; la vérité était « non mesuré ». Désormais NaN, donc `null`
  dans `run.json` et « — » à l'écran.
- **Tableaux enroulés sur deux lignes par enregistrement** : les largeurs
  de colonnes étaient fixes et dépassaient le panneau dès 100 colonnes,
  sur l'écran Live. La cause profonde était un écart de deux colonnes
  entre la largeur qu'un panneau OCCUPE et celle qu'il offre à son
  contenu, que chaque vue recalculait de tête. Un tableau enroulé n'est
  plus un tableau.
- **Badges d'état supprimés en premier** quand la largeur manquait — dont
  « LIVE — ARGENT RÉEL » et l'état du kill-switch. Ce sont les dernières
  informations qu'on peut se permettre de perdre.
- **Barre de raccourcis rognée sur les touches globales** : `? aide` et
  `q quitter` étaient en fin de liste, donc coupées les premières.
- **Cartes de statistiques abandonnées en silence** : le panneau Compte
  perdait « Bougies » et « Ordres » à 70 colonnes.
- **`PanelH` comptait les lignes AVANT enroulement**, donc dépassait sa
  hauteur promise et désalignait le panneau voisin.
- **Cellules décalées d'une colonne** après retrait d'une colonne : le
  prix s'affichait sous l'entête « État ». Attrapé par le test écrit dans
  la foulée du correctif.
- **Chemin d'historique débordant la largeur du terminal** : écrit hors
  de tout panneau, sans troncature. Un débordement décale la mise en page
  de TOUS les écrans, Bubbletea composant des chaînes sans les découper.
  Attrapé par le premier test de rendu des écrans.
- **Compteurs de rejet du risque écrits sans verrou** : un SEUL
  `risk.Manager` est câblé dans `app.New` et partagé par le backtest, le
  walk-forward (plis parallèles) et le moteur live (une goroutine de rejeu
  par symbole). `counts[motif]++` sur une map non protégée : course
  confirmée au détecteur, et risque de `fatal error: concurrent map
  writes` — une panique du runtime, irrattrapable, au milieu d'un
  entraînement ou d'une séance. Corrigé par un `sync.Mutex`. Test de
  régression : `TestConcurrentEvaluateIsSafe`.
- **Cinq écrans sur six débordaient la HAUTEUR du terminal.** En 80×24 —
  la taille la plus banale qui soit — l'écran Live rendait 29 lignes. Un
  corps trop haut ne perd pas ses dernières lignes : il pousse l'entête et
  la barre de raccourcis hors de l'écran, donc « LIVE — ARGENT RÉEL » et
  « q quitter ». L'audit de v0.2 n'avait contrôlé que la LARGEUR ; le
  contrôle de hauteur, écrit en premier, a montré le défaut d'un coup.
  Trois causes : `Fill` complétait sans jamais couper, chaque écran
  devinait la hauteur de ses panneaux par une soustraction de constantes,
  et un panneau de statistiques s'étalait sur trois rangées sans se
  demander s'il restait de la place. Corrigé par `Fit`, `FitBlock` et
  `StatRowMax`. Test de régression :
  `TestScreensNeverExceedTheTerminal`.
- **L'aide occupait cinquante-neuf lignes quelle que soit la fenêtre** :
  sur vingt-quatre lignes, les trois quarts partaient dehors — dont la
  ligne qui explique comment la refermer. Une aide illisible dont rien ne
  dit qu'elle continue est pire que pas d'aide.
- **`Resample` réservait 8,9 Mo pour produire 1 560 bougies.** La capacité
  initiale valait `len(series)/4`, hypothèse vraie pour M1→M5 seulement.
  La mise à zéro de cette tranche pesait un tiers du temps, le plancher
  recalculé par bougie un autre tiers. Mesuré : M1→H4 de 21,0 à 9,0 ms,
  M1→D1 de 26,4 à 8,5 ms. Ce n'est pas un bug d'exactitude — d'où le
  témoin naïf ajouté en même temps que l'optimisation.
- **`ui.theme` ne forçait rien** (voir plus haut) : une clé exposée dans
  l'écran Paramètres, réglable, documentée, et sans le moindre effet.
- **`gw backtest EURUSD --csv` refusé** : `runBacktest` parsait ses
  options sans `partitionArgs`, et le paquet `flag` s'arrête au premier
  argument positionnel. L'usage documenté dans le README ne marchait
  pas. Trouvé par un essai de bout en bout sur le vrai binaire, pas par
  les tests — d'où le test ajouté dans la foulée.
- **Une année en `.gwb` aurait été retéléchargée** après la bascule :
  `NeedsDownload` ne cherchait que le Parquet. Des heures de réseau, et
  un 429 au bout de trois minutes, pour des données déjà sur le disque.
- **Prix relus à cinq décimales près, puis réécrits en flottant brut** :
  le fichier triplait (24,9 Mo au lieu de 9,2). Un compresseur ne sait
  rien faire d'un bruit de bas de mantisse. Corrigé par un arrondi — mais
  un arrondi VÉRIFIÉ, sans quoi un import plus fin que
  `Instruments` y perdrait des cotations réelles.
- **Recentrage de `RollingStd` jamais établi sur une série courte** : bug
  introduit pendant l'optimisation et attrapé par la comparaison avec
  l'implémentation naïve. C'est précisément à ça qu'elle sert.

### Performance (mesurée, pas supposée)

Bancs d'essai : `internal/indicator/bench_test.go`,
`internal/ml/gbdt/bench_test.go`, `internal/label/bench_test.go`,
`internal/data/bench_test.go`, `internal/tui/bench_test.go`.

| Changement | Avant | Après |
|---|---|---|
| `RollingMax` fenêtre 50, 400 k points (file monotone) | 62,5 ms | 12,7 ms |
| `RollingMin` fenêtre 20, 400 k points | 35,7 ms | 12,7 ms |
| `RollingMean` fenêtre 20 (somme simple + resync) | 2,57 ms | 1,89 ms |
| Entraînement GBDT, mémoire allouée | 3 855 Mo | **72,6 Mo** |
| Entraînement GBDT, allocations | 226 000 | 44 000 |
| Entraînement GBDT, temps (4 cœurs) | 4,41 s | 3,84 s |
| Walk-forward de bout en bout, échantillon CPU | 1 980 ms | 1 130 ms |
| `Resample` M1→H4, 372 k bougies | 21,0 ms / 8,93 Mo | **9,0 ms / 0,16 Mo** |
| `Resample` M1→M5, 372 k bougies | 23,5 ms / 8,93 Mo | 16,1 ms / 7,14 Mo |
| `Resample` M1→D1, 372 k bougies | 26,4 ms / 8,93 Mo | **8,5 ms / 0,03 Mo** |
| Stockage d'une année de M1 (taille) | 14,9 Mo (`.gwb`) | **9,2 Mo** (Parquet) |
| Lecture d'une année | 26 ms | 139 ms |
| Lecture d'un mois | seek | 50 ms |
| Écriture d'une année | 68 ms | 485 ms |
| Binaire (`-s -w`, CGO désactivé) | 8,8 Mo | 15,2 Mo |

Ce qui a produit ces gains : file monotone pour les extrema glissants,
capacité de sortie du ré-échantillonnage calée sur la durée couverte et
non sur le nombre de bougies d'entrée, plancher de bucket recalculé au
changement de bucket seulement, parcours par indice pour ne plus copier
une `core.Bar` de cent octets par tour,
recyclage des histogrammes du GBDT, partition en place (plus d'allocation
par coupure), seuil en deçà duquel la parallélisation des histogrammes
coûte plus qu'elle ne rapporte, pré-dimensionnement du chargement de
l'historique depuis les en-têtes, et mise en cache des lectures de la TUI
(trades, graphique ré-échantillonné).

Le stockage est le seul poste où le projet a sciemment payé de la vitesse
et de la taille de binaire : lecture ×5, écriture ×7, binaire ×1,7. Ce
qu'il achète est l'interopérabilité, et la lecture columnaire a tout de
même été optimisée (chemin rapide `ReadDoubles` pour les colonnes sans
valeur absente, groupes de lignes sautés hors période).

Ce qui a été mesuré puis **écarté** : la décomposition en blocs du
balayage du labeling — le pire cas redouté n'est pas atteignable, les
barrières suivant l'ATR (voir plus haut).
