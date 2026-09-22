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
- **La barrière VERTICALE vit dans `label`** (`Horizon`, `Deadline`,
  `Expired`) et les DEUX moteurs l'appellent. Écrite deux fois, une règle
  de barrière finit par diverger — c'est exactement ce que le projet
  cherche à empêcher. `Expired` est formulée sur la bougie courante et la
  cadence du flux, jamais sur l'horodatage de la bougie suivante : le live
  ne l'aurait pas.
- **Divergence connue entre cible et exécution** : l'étiquetage accorde
  cinq jours calendaires, la clôture de fin de semaine ISO plafonne la
  détention à moins de cinq jours (lundi → vendredi). La barrière
  verticale ne se déclenche donc jamais sur des données forex réelles ;
  elle protège le live, qui n'a pas de règle de week-end. Aligner
  vraiment les deux suppose de modéliser le week-end DANS l'étiquetage,
  donc une nouvelle révision (`colibri_v1_1`) — jamais une retouche du
  modèle publié.
- **Stops et gaps** : un stop déclenché est un ordre AU MARCHÉ, rempli au
  pire de la barrière et de l'ouverture. Une limite, elle, garde son prix
  exact : un ordre à cours limité ne s'exécute jamais moins bien, et lui
  accorder le gap serait s'attribuer une chance invérifiable.
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

`bubbletea`, `lipgloss`, `yaml.v3`, `bbolt`, `ulikunitz/xz` — cinq
directes en production, **aucune native**. `muesli/termenv`, déjà
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

## État du projet (22 septembre 2026)

**Complet de bout en bout, ~15 200 lignes de code + ~6 700 de tests,
21 paquets, suite verte avec `-race`.**

Couverture par paquet (la plus basse d'abord) : `cmd/gw` 18 %,
`tui/view` 43 %, `config` 57 %, `data` 59 %, `core` 62 %, `tui` 68 %,
`training` 75 %, `indicator` 77 %, `broker` 78 %, `app` 78 %,
`storage` 79 %, `tui/component` 79 %, `feature` 79 %, `live` 80 %,
`strategy` 80 %, `ml/gbdt` 81 %, `backtest` 85 %, `export` 88 %,
`risk` 88 %, `label` 95 %, `tui/theme` 100 %. Les chiffres bas ne sont pas
tous des trous : dans `data` et `config`, le non-couvert est surtout la
branche réseau Dukascopy et les erreurs d'E/S ; tous les invariants
annoncés, eux, ont un test qui échoue s'ils cessent d'être vrais.

Validé réellement :

- walk-forward 5 plis × 3 paires sur ~18 000 bougies H4 : **3 s**,
  AUC OOS 0,616, modèle de production écrit ;
- backtest CLI et TUI, courbe d'équité braille, tableau des trades ;
- passerelle `replay` en TUI : connexion, flux, agrégation H4, signaux,
  ordres, exécutions, positions, journal des trades ;
- rendu de la TUI CONTRÔLÉ par test en LARGEUR **et en HAUTEUR** : chaque
  écran est dessiné de 60×18 à 200×60 et ne dépasse ni la largeur ni la
  hauteur demandées. C'est le contrôle de hauteur, ajouté en v0.3, qui a
  révélé que cinq écrans sur six débordaient en 80×24 ;
- `ui.theme` vérifié sur le vrai binaire sous tmux : `GW_THEME=dark` et
  `GW_THEME=light` produisent bien deux jeux de couleurs différents
  (SGR 179 contre 136 pour un titre).

**Reste à valider chez l'utilisateur** : premier téléchargement Dukascopy
réel (le bac à sable de dev est limité à 429).

**Reste à faire** :

1. **Brancher Interactive Brokers** — seul élément manquant pour que le
   live soit autre chose qu'un rejeu. Feuille de route détaillée dans
   `docs/brokers.md`. Tant que `placeOrder` ne soumet pas un vrai bracket
   OCA, `Info.SupportsBracket` reste à `false` et le moteur REFUSE les
   entrées : basculer ce drapeau sans le code revient à mentir au moteur.
   ⚠ Ne pas écrire ce protocole « à l'aveugle » : du code non éprouvé
   contre un vrai TWS, sur le chemin qui envoie des ordres réels, est
   précisément ce que les règles du projet interdisent.
2. **Icône Windows** : toute la mécanique est en place et automatique —
   `make windows` et le workflow de release détectent `build/icon.ico`,
   génèrent le `.syso` et le lient, ou DISENT ce qui manque. Il ne reste
   qu'à déposer l'icône elle-même (256×256), qui est une décision de
   design, pas de code.
3. **Exposition croisée inter-actifs** dans le walk-forward : chaque actif
   a son propre moteur, donc les plafonds s'appliquent par actif. Limite
   documentée, pas masquée — et plus masquée non plus dans les chiffres :
   l'agrégat renvoie NaN pour le drawdown et le Sharpe, qui exigeraient
   une courbe de valeur commune inexistante. Les calculer pour de bon
   suppose de trancher comment le capital se partage entre actifs : c'est
   une décision de conception, pas un calcul.
4. **Mesurer `risk_per_trade_pct`** : le dimensionnement au risque existe
   et est testé, mais il est à 0 (désactivé) par défaut. Avant de
   l'activer, un walk-forward avant/après — il déplace drawdown, profit
   factor et SQN.
5. **60×18 ne suffit pas à l'écran Live.** C'est le plancher en deçà
   duquel le programme refuse de dessiner, mais entre 60×18 et 80×24 le
   corps est coupé et `Fit` annonce les lignes masquées. Deux sorties
   possibles : relever le plancher déclaré, ou donner aux panneaux
   explicatifs une version courte sous 70 colonnes. Ne pas le « régler »
   en supprimant l'avertissement.

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
- **README abrégé, détail dans `docs/`.** Les tableaux de garanties et de
  pannes vivent dans `docs/depannage.md` : un lecteur qui découvre le
  projet n'en a pas besoin avant d'avoir lancé le binaire.

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

Ce qui a été mesuré puis **écarté** : la décomposition en blocs du
balayage du labeling — le pire cas redouté n'est pas atteignable, les
barrières suivant l'ATR (voir plus haut).
