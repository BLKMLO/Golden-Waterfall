# LLM.md — Mémoire de travail de Golden Waterfall

> **Source de vérité unique du contexte projet, pour TOUTES les IA.**
> `CLAUDE.md` et `AGENTS.md` ne font que rediriger ici.
>
> Règle de maintenance : après chaque changement notable, mettre ce fichier
> à jour. On y garde ce qui ÉVITE UNE ERREUR à la prochaine séance
> (règles, pièges, décisions encore en vigueur, reste-à-faire). Le récit
> détaillé vit dans `docs/` et dans l'historique git — pas ici.

## Identité — règle de nommage

- **Golden Waterfall** = le LOGICIEL (binaire `gw`, TUI, paquets
  `internal/`). Toute l'identité visible dit « Golden Waterfall ».
- **Colibri** et **Troglodyte** = uniquement des **moteurs de décision**,
  entièrement dans `internal/strategy/colibri/` et
  `internal/strategy/troglodyte/`. Ne jamais appeler le logiciel par le
  nom d'un moteur.
- **Une GÉNÉRATION de moteur = un nom d'oiseau** : Colibri (classifieur
  GBDT) est la première, Troglodyte (suivi de tendance, filtre de
  Kalman, v0.7.0) la deuxième. Les révisions d'une génération sont
  numérotées : `colibri_v1_0`, `v1_1`, `v1_2` (défaut des nouvelles
  installations), `troglodyte_v1_0`, `v1_1`.
- **Colibri n'a PAS droit à internet** (règle du propriétaire) : aucune
  révision Colibri ne déclare `UsesNews` ; `TestColibriNeverUsesTheNews`
  le fait respecter.

## Commandes

```bash
make build      # binaire gw (CGO_ENABLED=0)
make race       # tests + détecteur de concurrence
make lint       # gofmt + go vet
make dist       # les cinq binaires (Linux ×2, macOS ×2, Windows)
./gw            # interface terminal
```

Sous-commandes : `download [--year A | --from A --to B]`,
`train [PAIRES…] [--risk-per-trade X]`, `backtest PAIRE [--csv]`, `runs`,
`migrate [--remove]`, `import --symbol S FICHIER…`,
`news [fetch | import FICHIER…]`, `paths`, `config [--default]`,
`version`. `gw news` n'ouvre pas la base (utilisable TUI ouverte).

Les options sont remises devant les paires avant parsing
(`partitionArgs`) : le paquet `flag` s'arrête au premier argument
positionnel et ignorerait sinon `gw download EURUSD --year 2019` en
silence. Toute nouvelle sous-commande à paires positionnelles doit
passer par là.

Six écrans : **Live**, **Données**, **Backtest**, **Entraînement**,
**Journal**, **Paramètres**.

Configuration : `config.yaml` dans le dossier utilisateur, créé au premier
lancement depuis le modèle **embarqué** (`internal/config/default_config.yaml`).
Priorité : défauts → YAML → variables `GW_*` (l'environnement a le dernier
mot). Nouvelle clé = struct + `Default()` + `Validate()` + modèle YAML +
écran Paramètres (+ `envBindings` si variable), sinon une clé inconnue fait
refuser le démarrage.

Publication : PR fusionnée par **squash** sur `main`, puis onglet
**Actions → Release → Run workflow** avec le numéro (`v0.5.0`), ou tag
`v*`. Conséquence du squash : avant la PR suivante, rebaser la branche de
travail sur `origin/main` (sinon conflits sur un contenu pourtant
identique).

## Règles d'architecture (non négociables)

1. **Flux strict, aller ET retour** :
   `Feed → Strategy → Signal → risk.Manager → OrderRequest → Gateway`,
   retour par `ExecutionReport → live.Engine → journal`. En backtest comme
   en live. Aucune stratégie ne parle au broker ; aucun ordre ne contourne
   le risque ; **aucun trade sans compte rendu réel**.
2. **Câblage uniquement dans `internal/app/app.go` (`app.New`)**.
3. **Bus d'événements** (`core/bus.go`) : ne bloque JAMAIS un producteur.
4. **Config centralisée et fail-fast** : valeur absurde ou clé inconnue =
   refus de démarrer, avec la clé nommée.
5. **Paper → live** = `broker.mode`. Rien d'autre.
6. **L'interface ne ment jamais** (tableau ci-dessous).
7. **Ne jamais modifier une définition de modèle publiée** (features,
   barrières, seuils) : nouvelle **révision**, ou nouvelle **génération**.
8. **Une stratégie est un module remplaçable.** Moteurs, walk-forward, TUI
   et CLI ne connaissent que `strategy.Strategy` et sa `Description`
   (`ContextBars`, `MaxHold`, `HoldsOverWeekend`, `ExitOnReversal`,
   `UsesNews`, `ModelManifest`). Règles d'exécution partagées dans `core/horizon.go`
   et `strategy.ApplyReversal`, appelées par les DEUX moteurs. Le catalogue `internal/strategies`
   est le SEUL paquet qui nomme une implémentation ; toute stratégie qui y
   figure passe le banc `strategy/strategytest`.
9. **Une passerelle DÉCLARE ses capacités** (`Info.Simulated`,
   `Info.SupportsBracket`) ; le moteur refuse une entrée à barrières si la
   passerelle ne les porte pas.

## L'honnêteté, en pratique dans le code

Chaque ligne = un champ réel et un test qui échouerait si elle cessait
d'être vraie.

| Mécanisme | Où | Ce qu'il empêche |
|---|---|---|
| `component.Dash` (« — ») | TUI | Confondre « zéro » et « on ne sait pas » |
| `Info.Simulated` + bandeau REJEU | broker, TUI | Prendre un compte fictif pour un compte réel |
| `Info.SupportsBracket` + `Stats.UnprotectedRefused` | broker, live | Ouvrir une position nue avec un stop affiché nulle part |
| `ExecutionReport` obligatoire | live | Inventer un trade que le broker n'a pas fait |
| `ExecutionReport.Closing` | core, live | Prendre la sortie d'une position d'avant le démarrage pour une entrée |
| `Position.UnrealizedKnown` | core, TUI | Afficher 0 de P&L latent quand le courtier ne le donne pas |
| `ibCheckMode` + devise vérifiée | broker IB | Trader un compte réel en « paper », ou afficher ARGENT RÉEL sur un compte papier |
| Sortie IB = annulation CONFIRMÉE des barrières, puis marché | broker IB | Un stop orphelin qui rouvre une position |
| `checkProtection` | broker IB | Une position protégée à l'écran et nue chez le courtier |
| Messages IB identiques octet pour octet au client officiel | broker IB | Un protocole écrit de mémoire |
| `Stats.CostsModelled` / `CurrencyExact` | backtest | Facturer zéro coût ; additionner des devises non convertibles |
| `Stats.RejectedOrders`, `Rejections` par run (`Fork`) | backtest, risk | Confondre refus et abstention ; publier un cumul comme une mesure |
| `MaxDrawdownPct`/`Sharpe` à NaN dans l'agrégat | backtest | Lire « 0 % » là où la vérité est « non mesuré » |
| `Decision.Reason`, `risk.SizingRefusals`, `Decision.Capped` | risk, TUI, CLI | Un silence inexpliqué ; annoncer « 0,5 % » quand le plafond rabote |
| `SplitByConversion` + paire annotée dans le sélecteur | app, CLI, TUI | Découvrir dans un agrégat vide que 21 paires sur 31 ne tradent pas |
| `SymbolState.Notice` + `ModelLoaded` | live | Confondre « pas de modèle » et « historique périmé » |
| `FileHeader.Failures`/`Imported`, `gw.failures` | data | Prendre une année trouée ou importée pour une année faite |
| Ask absent = `NULL`, colonnes appariées PAR NOM | data | Un prix inventé ; des prix crédibles et faux d'un fichier tiers |
| Relecture bougie à bougie avant suppression | data (migrate) | Effacer un historique sur une conversion non vérifiée |
| `sameColumns` au chargement | strategy | Appliquer un modèle à des colonnes décalées |
| Stabilité par préfixe des DÉCISIONS (`strategytest`) | toute stratégie | Une décision qui dépend du futur |
| Cible v1_2 confrontée au moteur de backtest | colibri | Apprendre des gains que l'exécution ne verse pas |
| Modèle rangé PAR PAIRE | colibri | Décider EURUSD avec le modèle d'USDJPY |
| Modèle refusé hors de sa paire, unité de temps, révision, définition | troglodyte | Des variances H4 d'EURUSD appliquées au M15 d'USDJPY |
| `negligible_eps` / `negligible_zeta` (test du rapport de vraisemblance), `at_upper_bound` | troglodyte | Prendre une variance au bord pour une estimation, ou l'inverse |
| `ScoreOOS` → non calculable | troglodyte | Une AUC inventée pour un moteur qui n'est pas un classifieur |
| Sortie de fin de semaine au temps des TICKS, en retard plutôt que jamais, `WeekendExits`/`WeekendSkipped` | live | Une position que le backtest a fermée, portée en live pendant le week-end |
| `NewsUncovered` (backtest, live), `NewsSummary`, `✗ news` informatif | news, backtest, live, TUI, CLI | Prendre « calendrier absent » pour « aucune annonce » |
| Semaine couverte déclarée explicitement ; import = semaines contenant une annonce | news | Un fichier qui s'arrête le 10 mars présenté comme couvrant le 20 |
| Sorties orientées `ExitLong`/`ExitShort` (`Closes`) | core, risk | Un stop suiveur long qui ferme une position courte |
| Simulation du calibrage confrontée au VRAI moteur, trade par trade | troglodyte | Calibrer une règle que l'exécution ne suit pas |
| Avertissement IN-SAMPLE | backtest (TUI + CLI) | Lire un rejeu comme une performance |
| Brouillon + « prend effet au démarrage » ; `GW_*` non modifiable ; `Validate()` avant écriture | TUI Paramètres | Croire un réglage actif ; éditer une valeur que l'environnement réécrira ; se rendre le démarrage impossible |
| `Table` : `+N col.` ; `Fit` : lignes masquées ; `StatRowMax` : `+N` | TUI | Perdre une information sans le dire |
| Badges d'état jamais supprimés | TUI | Perdre « LIVE — ARGENT RÉEL » en réduisant la fenêtre |
| Listes de trades défilables, position « n/N », « 500 plus récents sur N » | TUI | Des trades invisibles, sans un mot |
| Ligne de contrôle Live (`checks`), « ? » avant la connexion | TUI Live | « Pourquoi rien ne se passe ? » sans réponse ; supposer un modèle chargé |
| Numéro de compte tapé avant une connexion live réelle (`ConnectAs`) | TUI Live, live | Engager de l'argent réel sur une touche |
| Filtre « tradables » par défaut + devise dans l'entête | TUI | Choisir une paire qui ne produira aucun trade |
| « n » (aucun) décoche aussi les paires masquées | sélecteur | Une sélection validée qui contient des paires invisibles |
| `TestBodiesFitWithoutCutting`, `TestLoadedScreensFit` | TUI | Un écran coupé par `Fit` alors qu'il pouvait s'abréger |
| Filtre du Journal rappelé ; période de téléchargement affichée | TUI | Un filtre oublié ; croire télécharger tout l'historique |
| Export = copie, cellule VIDE si non mesuré, drapeaux exportés | export CSV | Un fichier qui contredit l'écran ; un zéro inventé |
| `theme.Apply` force réellement le fond | TUI | Une clé de configuration qui n'agit pas |

## Conventions

- **Français** pour commentaires, docs et réponses ; identifiants en
  anglais. Un commentaire dit POURQUOI, pas QUOI.
- **Un paquet = une responsabilité** ; le fichier ne répète pas le paquet.
- **Tests en miroir** : `internal/<paquet>/<fichier>_test.go`.
- **Aucune couleur littérale hors de `internal/tui/theme`.**
- **Un écran MESURE sa mise en page** : `component.FitBlock`,
  `component.Fit` (coupe en dernier recours, en le disant),
  `component.PanelContent(width)` pour la largeur intérieure. Budget de
  `PanelH` : `height − 3` ; un `Table` fenêtré prend `visible + 2` lignes
  (entête, pied « N lignes »).
- **Petits terminaux** : chaque écran a une disposition COMPACTE (lignes
  sans cadre, avertissements sur une ligne qui commence par le plus grave)
  choisie seulement si la complète ne tient pas. Les textes explicatifs
  sont écrits d'un trait, sans retour à la ligne manuel, et ont une
  version courte (`explainPanel`). Deux tests l'exigent dès 60×18, écrans
  vides ET pleins.
- **Aide contextuelle** : une ligne au bas des panneaux de trades dit ce
  que font les touches LÀ. `t` veut dire « aller aux trades / revenir »
  dans Backtest comme dans Journal ; `entrée` ouvre le détail d'un trade ;
  `v` bascule « tradables / toutes » partout où l'on choisit une paire.
- **Listes longues** : `component.Scroll` (curseur, ↑↓, pgup/pgdn,
  début/fin, molette) + `Table` avec ce curseur — jamais `cursor = -1`
  sur une liste qui peut dépasser l'écran.
- **Touches** : ← → et les lettres appartiennent aux ÉCRANS ; `tab`,
  `?`, `q`, `1`–`9` sont globaux. Un écran qui saisit du texte l'annonce
  par `view.KeyCapturer`.
- **Aucun `panic` sur une donnée** ; seulement sur une incohérence de code.
- Nouvelle passerelle : `Register()` dans un `init()`. Nouvelle stratégie :
  `strategy/<oiseau>/` + `Register()` + UNE ligne dans
  `internal/strategies/strategies.go`.
- **Une optimisation se mesure** : banc avant/après, et pour une réécriture
  numérique une comparaison à une implémentation naïve.
- Après chaque modification : conséquences annexes (config, docs, tests,
  `.gitignore`, ce fichier). Appliquer les améliorations évidentes sans
  demander.

## Points techniques à ne pas réapprendre

### Données

- **Dukascopy** : mois **0-based** dans les URLs ; `.bi5` = LZMA, champs
  **`offset, open, CLOSE, LOW, HIGH, volume`** ; 404 = marché fermé ;
  429 = backoff LONG, concurrence 3. Le bac à sable de dev reçoit 429 :
  aucun historique réel n'y a jamais été téléchargé (25/09/2026 : le
  réseau répondait, mais 4 jours sur 260 en un quart d'heure).
- **Samedi ET dimanche ne sont jamais demandés** (`daysOfYear`) : les
  heures de réouverture du dimanche soir manquent à l'historique (le
  fichier existe : 200, 2 724 octets pour le 7/1/2024). ⚠ La clôture de
  fin de semaine du backtest (`LastBarsOfWeek`, semaine ISO) SUPPOSE
  cette absence : un historique importé avec des bougies du dimanche
  ferait clore la semaine APRÈS le week-end.
- **Parquet** (`data/parquet.go`) : `time` (TIMESTAMP MILLIS UTC),
  `bid_*`, `ask_*` (NULLABLES), `volume` ; métadonnée `gw.failures` ;
  groupes de 32 768 bougies ; prix ARRONDIS seulement si l'aller-retour
  est exact (`rounder`).
- **`.gwb` en LECTURE SEULE** ; `NeedsDownload` regarde les deux formats.
- **Parquet tiers** : colonnes par NOM avec alias, unité d'horodatage par
  le type logique ; pyarrow déclare tout nullable. Témoin :
  `testdata/foreign_EURUSD_m1_2021.parquet`.
- **Devises** : le P&L d'une paire `XXXYYY` naît en `YYY` ;
  `data.ConversionFor` ne convertit que si la devise du compte est base ou
  cotation.
- **`Resample`** : plancher calculé au CHANGEMENT de bucket ; capacité
  selon la durée couverte. Témoin : `resampleNaive`.

### Modèle et cible

- **GBDT maison ≈ LightGBM sans GOSS ni EFB** (`docs/gbdt.md`).
- **Anti-fuite** : test de stabilité par préfixe
  (`Compute(s)[:k] == Compute(s[:k])`). Ne jamais le désactiver.
- **v1_0/v1_1** : deux barrières dans la même bougie → la basse (faux
  pour un short) ; barrière de 5 jours jamais atteinte (fin de semaine
  ISO avant). **v1_2** corrige par une tête par sens (`label.Sided`) et
  `label.ExecutionWindow` ; jamais par retouche des révisions publiées.
- **Calibrage v1_2 = rétrécissement vers le taux de base**
  (`gbdt.FitShrinkage`, centre FIXE). Pente libre et ordonnée libre ont été
  mesurées puis écartées.
- **Marge 0,10 R de v1_2 choisie sur SYNTHÉTIQUE** (`GW_ABLATION=1`).
  ⚠ v1_2 ne domine pas v1_1 partout (rappel horaire : PF 1,46 contre 1,36
  pour v1_1). Tableau : `docs/colibri.md`.
- Purge gardée même sans effet mesurable ; poids d'unicité écartés (mesure).
- Balayage avant du labeling : pas de pire cas (barrières à l'ATR) — ne
  pas le décomposer en blocs.
- `RollingStd` recentre ses accumulateurs (sinon perte de précision sur un
  OBV cumulé).
- `Stats` a un `MarshalJSON` (NaN → `null`, ±∞ → `"inf"`) : sinon
  `run.json` refuse d'être écrit.

### Troglodyte (`docs/troglodyte.md`)

- **Fenêtre FIXE de 500 bougies refiltrée à chaque décision** : la pente
  a une mémoire longue quand σ²_ζ est petit ; filtrée depuis le début de
  la série, la décision dépendrait du début du bloc (backtest) ou du
  tampon (live). Ne pas « optimiser » en filtre incrémental.
- **Initialisation diffuse EXACTE** sur y₁, y₂ (pas de « grand κ »).
- **Vraisemblance concentrée en σ²_η**, pas σ²_ε : un taux de change n'a
  presque pas de bruit d'observation ; rapportées à σ²_ε, les variances
  butaient sur les bornes (trouvé par un essai du binaire, pas par les
  tests). Variance « nulle » = test du rapport de vraisemblance,
  2ΔlnL < 2,71 (½χ²₀ + ½χ²₁, Self et Liang 1987).
- Seuils 1,5 / 0,5, stop 3 ATR, fenêtre 500 : **CONVENTIONS**, jamais
  mesurées. Pas de limite (`TakeProfit` = 0 permis par le banc).
- Filtre confronté à un conditionnement gaussien DENSE, vraisemblance à
  une densité de Cholesky ; estimation vérifiée sur séries simulées.
- **v1_1** (v0.7.1) : prix NORMALISÉ par sa volatilité (EWMA des r²,
  demi-vie 30, σ_{t−1}, calculée sur la fenêtre seule) ; stop suiveur
  CHANDELIER (22 bougies, k ATR) via sorties orientées ; s_in ∈ {1 ; 1,5 ;
  2 ; 2,5} et k ∈ {2 ; 3 ; 4} CALIBRÉS in-sample (t de Student de la
  moyenne des trades, n ≥ 20, repli 1,5/3 signalé), s_out = s_in/3 ;
  `UsesNews`. Tout cela : conventions. `decide` est LA règle (OnBar ET
  calibrage). Un modèle calibré hors grille est refusé au chargement.
- **v1_0 vérifiée identique octet pour octet** après la refonte du paquet
  (modèle + 3 000 décisions, et walk-forward synthétique : mêmes 38
  trades). Refaire cette capture avant toute retouche du paquet.

### Exécution et risque

- **Règles de sortie dans `core/horizon.go`** (`HoldDeadline`,
  `HoldExpired`, `LastBarsOfWeek`, `MedianSpread`), appelées par les
  moteurs ET l'étiquetage v1_2. `HoldExpired` raisonne sur la bougie
  courante et la cadence, jamais sur la bougie suivante.
- **Fin de semaine = déclarée** (`HoldsOverWeekend`, v0.7.0). Backtest :
  semaine ISO des données, **aucune entrée sur la dernière bougie**. Live :
  la dernière bougie du vendredi n'y est close qu'à la réouverture, d'où
  une règle HORAIRE (`core.WeeklyClose` = vendredi 17 h New York,
  `time/tzdata` embarqué ; sortie dans les 5 min avant, `WeekendGuard`,
  CONVENTION ; en retard au premier tick suivant sinon ; aucune entrée
  sur `LastBarBeforeWeekend`). Heure des TICKS, jamais de la machine.
  Métaux/indices : même heure, non vérifiée (IB : forex seulement).
- **`Exit` et retournement** (v0.7.0) : le backtest ferme au close de la
  bougie de décision (motifs `signal`, `reversal`). Avant v0.7.0 il
  IGNORAIT `Exit` alors que le live l'exécutait — sans effet, Colibri
  n'en émet pas. Colibri vérifié identique AU BIT PRÈS après ce
  changement (walk-forward complet, binaire de `main` contre la branche).
- **Stop = ordre au marché** (gap : pire de la barrière et de
  l'ouverture) ; **limite = prix exact**.
- **Dimensionnement au risque actif, 0,5 %** — CONVENTION, pas mesure.
  `max_position_size` est une GARDE, `fixed_position_size` la taille
  quand le risque vaut 0. Sur un compte USD, **21 des 31 instruments** ne
  sont pas convertibles → toutes leurs entrées refusées (dit à quatre
  endroits). Remède : triangulation (reste-à-faire).
- **Un seul `risk.Manager`** partagé (backtest, walk-forward parallèle,
  live) : ses compteurs sont sous `sync.Mutex`, et `Fork()` donne des
  compteurs par run.
- **Live multi-paires dans UNE instance** : moteur à état par symbole,
  risque et équité partagés, aucune corrélation modélisée
  (`docs/architecture.md` § 5 ter).
- **Rejeu** : horodate au temps du marché rejoué, jamais l'heure réelle.
- **bbolt** verrouille le fichier : une seconde instance échoue, c'est
  voulu.

### Actualités (`internal/news`, `docs/actualites.md`)

- Flux `nfs.faireconomy.media/ff_calendar_thisweek.json` : semaine EN
  COURS seulement (précédente/suivante = 404, constaté le 25/09/2026) →
  ARCHIVE `<données>/news/AAAA-MM-JJ.json` (dimanche 0 h New York), une
  récupération remplace sa semaine. Seuls ces noms de fichiers sont lus.
- Règle : entrée bloquée si annonce d'impact ≥ `min_impact` sur base ou
  cotation dans [t − after, t + before], t = close de la bougie de
  décision. Fériés jamais filtrants. Hors semaine couverte → NON filtrée,
  COMPTÉE (`NewsUncovered`). Sorties jamais filtrées.
- `news` n'importe PAS `config` (sinon cycle via `data`) : `app.NewsOptions`
  traduit. Source inconnue = refus de démarrer (dans `app.New`).
- Récupération : live (si la stratégie déclare, toutes les
  `refresh_minutes`) et `gw news fetch`. Tests : faux serveur HTTP, jamais
  le réseau ; extrait réel du flux dans `testdata/`.

### Interactive Brokers (`internal/broker/ib_*.go`, `interactive_brokers.go`)

- Écrit contre le **client officiel IB, API 10.30 (Python)**, archive
  `twsapi_macunix.1030.01.zip` sur interactivebrokers.github.io.
  `testdata/ib/gen_golden.py` régénère les références ; toute retouche d'un
  message doit garder `TestRequestsMatchOfficialClientByteForByte` vert.
- Pas de dépendance : `scmhub/ibapi` exige Go 1.26 et protobuf.
- Versions : le client annonce `v100..187` (max du client 10.30 — en
  annoncer plus ferait parler un dialecte non relu) ; refus sous 163.
  Au-dessus de 201 TWS passerait en protobuf : ne pas monter le maximum
  sans le relire.
- Flottants envoyés au format `str()` de Python (`0.0`, valeur non
  renseignée `1.7976931348623157e+308`) ; champs `handle_empty` vides.
- **Compte papier = identifiant commençant par « D »** (seule preuve
  côté API).
- Bracket : parent `MKT DAY` `transmit=0`, limite `GTC` `transmit=0`,
  stop `GTC` `transmit=1` (le dernier transmet tout) ; enfants liés par
  `parentId`. Prix au demi-pip (0,00005 ; 0,005 pour JPY).
- Les rappels (ticks, comptes rendus) partent d'une goroutine de
  livraison dédiée, JAMAIS de la goroutine de lecture : le moteur peut
  appeler `PlaceOrder`, qui attend une confirmation que seule la lecture
  apporte (sinon interblocage).
- TWS annonce parfois l'annulation de la barrière jumelle AVANT
  l'exécution qui l'a provoquée : d'où le délai de grâce de
  `checkProtection` et `markClosing` dès la première exécution d'un
  enfant.
- Délais figés dans la struct à la construction (les minuteries ne
  relisent pas les variables du paquet — course de données en test).
- Forex IDEALPRO seulement ; pas de reconnexion automatique ; valeur
  liquidative rafraîchie par IB toutes les 3 min.

### Interface

- `ui.theme` : « auto », « dark », « light ». `theme.Apply` FIXE
  `lipgloss.SetHasDarkBackground` ; `theme.ByName` reste pur.
- L'aide est une fenêtre MODALE : tant qu'elle couvre l'écran, les touches
  lui appartiennent.

## Dépendances (volontairement minimales)

`bubbletea`, `lipgloss`, `yaml.v3`, `bbolt`, `ulikunitz/xz`,
`parquet-go/parquet-go` — six directes, **aucune native**, `CGO_ENABLED=0`
partout (promesse « un seul binaire »). Parquet fait passer le binaire de
8,8 à 15,2 Mo : prix assumé d'un format lisible par d'autres outils.
v0.7.0 : 15 536 312 → 16 093 368 octets (`-s -w`, mesurés côte à côte),
base des fuseaux `time/tzdata` et Troglodyte compris ; v0.7.1 :
16 212 152 octets (`net/http` était déjà là pour Dukascopy).
`muesli/termenv` est directe pour les SEULS tests du thème.

⚠ `go.mod` exige **Go 1.25** (`bbolt` v1.5, `x/sys` v0.45). Un repli sur
1.24 casse les paquets `charmbracelet/x/*` — ne pas le refaire.

CI : format, `go vet`, tests `-race`, **govulncheck**, compilation croisée.
Dependabot hebdomadaire, `charmbracelet/x/*` GROUPÉS.

Licence **MIT**, choisie par le propriétaire du projet.

## État du projet (25 septembre 2026, v0.7.1)

26 paquets, suite verte avec `-race`. `wc -l` des fichiers `.go` :
24 320 lignes hors tests, 11 364 de tests.

Couverture mesurée le 25 septembre 2026 (`go test -cover`) :
`cmd/gw` 23 %, `config` 57 %, `tui/view` 61 %, `core` 67 %, `tui` 69 %,
`data` 70 %, `tui/component` 74 %, `training` 74 %, `news` 76 %,
`app` 77 %, `indicator` 77 %, `storage` 79 %, `live` 80 %, `broker` 82 %,
`ml/gbdt` 83 %, `backtest` 86 %, `strategy/colibri` 87 %, `export` 88 %,
`strategy/troglodyte` 89 %, `risk` 90 %, `label` 97 %, `tui/theme` 100 %.

Validé réellement : walk-forward et backtest de bout en bout sur un
historique importé depuis pyarrow ; Parquet écrit relu par pyarrow ; rendu
TUI contrôlé de 60×18 à 200×60 ; sélecteur de paires sous tmux ; binaire
connecté à un **faux TWS** sous tmux (équité, position, ticks, refus
« compte papier en mode live ») ; en v0.6.0, binaire sous tmux à 60×18
et 132×34 sur un rejeu d'historique SYNTHÉTIQUE (liste de contrôle,
filtre tradables, dispositions compactes) ; en v0.7.0, `gw train` et
`gw backtest` avec `troglodyte_v1_0` sur trois ans de M1 SYNTHÉTIQUE
(aucune clôture de week-end, sorties sur signal, AUC « — »), et Colibri
v1_2 identique au bit près entre `main` et la branche ; en v0.7.1, même
essai avec `troglodyte_v1_1` (avertissement « hors calendrier » affiché),
`gw news fetch` contre le VRAI flux (une semaine archivée), TUI sous tmux
(`✗ news` informatif, section Actualités de Paramètres), Colibri et
Troglodyte v1_0 à nouveau identiques au bit près.

**Jamais validé** : un téléchargement Dukascopy réel, une mesure sur
données réelles (ni Colibri ni Troglodyte), une séance contre un vrai
TWS, la règle de fin de semaine live sur un vrai flux, le filtre
d'actualités sur une période archivée (aucun historique de calendrier).

## Reste à faire, par ordre de valeur

1. **Éprouver Interactive Brokers contre un VRAI TWS papier** : entrée,
   stop, limite, sortie sur signal, redémarrage ; comparer le journal au
   relevé IB. À faire avant tout `broker.mode: live`.
2. **Mesurer sur historique réel** `colibri_v1_2` contre `v1_1`, et
   `troglodyte_v1_0` contre les deux (`gw train`, `gw runs`). Toutes les
   mesures sont SYNTHÉTIQUES ; les seuils de Troglodyte sont des
   conventions. Un démenti donne une nouvelle révision, jamais une
   retouche.
3. **Triangulation des devises** (21/31 instruments non dimensionnables
   sur un compte USD). Le taux manquant est déjà sur le disque (GBPUSD
   pour un P&L en GBP) : série de taux alignée dans le temps au backtest,
   cotation supplémentaire en live, `Conversion` qui prend un instant.
   Change des résultats publiés : à traiter comme un changement de moteur.
4. **Mesurer `risk_per_trade_pct`** (`gw train --risk-per-trade 0` puis
   `0.5`, `gw runs`).
5. **Troglodyte, suite** : mesurer v1_1 contre v1_0 sur historique réel ;
   archiver un calendrier historique (`gw news import`) pour que le
   filtre compte dans une mesure ; variances variables dans le modèle
   lui-même ; avec un contrat multi-jambes, une génération d'arbitrage
   statistique qui réutiliserait le filtre de Kalman.
6. **IB, suite** : reconnexion automatique, métaux et indices (contrat
   à définir, pas à deviner), P&L latent via `reqAccountUpdates`.
7. **Exposition croisée** dans le walk-forward (drawdown et Sharpe
   agrégés à NaN faute de courbe commune).
8. **Import CSV** (le lecteur de colonnes par alias existe).
9. **Icône Windows** : il manque `build/icon.ico` (256×256).

## Décisions en vigueur (et pourquoi)

Chaque ligne est une décision qu'une séance future pourrait être tentée
de défaire.

- **TUI, pas web** ; **GBDT écrit en Go** (reproductible au bit près) ;
  **backtest sur mesure** (hypothèses explicites) ; **bbolt plutôt que
  SQLite** (pas de `cgo`).
- **Parquet plutôt qu'un format maison** : un format que seul ce
  programme lit enferme l'utilisateur. Payé ×5 en lecture, ×7 en écriture,
  ×1,7 en taille de binaire ; fichier 9,2 Mo contre 14,9.
- **`OnBar`, pas `on_tick`** : l'agrégation tick→bougie est faite une fois
  par le moteur live ; backtest et live entrent par la même porte.
- **Modèle de production entraîné sur tout l'historique** : c'est lui qui
  trade ; les plis disent s'il le mérite (d'où l'avertissement IN-SAMPLE).
- **Dimensionnement au risque actif par défaut** (0,5 %) : à taille
  fixe, la perte au stop suit l'ATR sans que personne l'ait décidé. Ce qui
  manque (équité, stop, conversion) REFUSE l'entrée.
- **Taille fixe par défaut 10 000** (mini-lot) : à 1 unité, les P&L
  étaient illisibles.
- **Écran Paramètres = brouillon** appliqué au prochain démarrage : un
  réglage à chaud donnerait un programme à deux configurations.
- **CSV francophone** (`;`, `,`, BOM) : pandas demande
  `sep=";", decimal=","` — écrit dans `docs/donnees.md`.
- **Ce qui est exporté est ce qui est affiché**, filtre compris.
- **Un import est marqué comme tel** : `Complete()` répond non.
- **IB : protocole écrit à la main contre le client officiel**, plutôt
  qu'une dépendance (Go 1.26 + protobuf) ; **forex seulement** plutôt
  qu'une correspondance approximative des métaux et indices ; **sortie =
  annuler puis confirmer puis vendre**, quitte à ne pas sortir.
- **Premiers pas en tête de `gw --help`** (avant la liste des commandes) ;
  le README n'y renvoie qu'en une ligne. Pas d'assistant de premier
  lancement dans la TUI : décision du propriétaire du projet.
- **README abrégé, détail dans `docs/`** (`architecture`, `brokers`,
  `actualites`, `colibri`, `depannage`, `donnees`, `gbdt`, `troglodyte`).
- **Deuxième génération = tendance structurelle** (Kalman), pas
  cointégration (deux jambes : contrat à refaire ; croisées non
  dimensionnables ; identité ln EURGBP = ln EURUSD − ln GBPUSD), ni
  Markowitz (des poids : sa place est dans le RISQUE, reste-à-faire
  « exposition croisée »), ni microstructure (aucune donnée ; l'exécution
  est à la passerelle). Ni les quatre à la fois : rien ne serait
  attribuable.
- **Troglodyte porte le week-end et sort sur retournement** : déclaré
  dans sa `Description`, jamais codé dans les moteurs ; Colibri garde
  l'ancien comportement (sa cible v1_2 l'a appris).
- **Pas d'AUC pour Troglodyte** : `ScoreOOS` répond « non calculable » ;
  `PositiveRate` et `Rounds` sont `omitempty` dans `run.json`.
- **Actualités = filtre des MOTEURS, déclaré par la stratégie**, pas une
  feature : une stratégie ne va jamais sur internet, backtest et live
  appliquent la même `Gate.Check`. `news.enabled: true` par défaut ; sans
  archive, rien n'est filtré et c'est dit. Calendrier absent en live →
  entrée TRANSMISE (échec ouvert, compté, journalisé), pas bloquée.
- **Calibrage petit et in-sample** (12 points) : le walk-forward juge ;
  plus de points = plus de chance d'un gagnant de hasard.
- **Stop suiveur par sortie de la stratégie** (chandelier), pas par
  déplacement d'ordre : le contrat ne sait pas modifier un stop posé.

## Leçons (bugs corrigés dont la cause peut revenir)

- Un **cumul publié comme une mesure** (rejets d'un `risk.Manager`
  partagé) → compteurs par run (`Fork`).
- **Moyenne glissante mal faite** (`moy = (moy + x) / 2`) → somme et
  compteur.
- **Vérifier les NOMS des colonnes** d'un modèle, pas leur nombre.
- **Deux fills du même sens ne font pas un aller-retour** ; une **sortie
  sans entrée connue** non plus (`Closing`).
- **Course de données** sur une map partagée ou un bus fermé pendant un
  envoi → verrous ; `-race` obligatoire.
- **Hauteur d'écran** : cinq écrans sur six débordaient en 80×24 ; une
  soustraction de constantes se trompe → mesurer (`FitBlock`). Encore en
  v0.5.0 : l'ancien budget du tableau de Backtest coupait son pied.
- **Une option ignorée en silence** (`flag` qui s'arrête au premier
  positionnel) → `partitionArgs`, et un essai sur le VRAI binaire : c'est
  ainsi qu'elle a été trouvée, pas par les tests.
- **Une clé qui n'agit pas** (`ui.theme`) est un piège ; elle agit ou
  disparaît.
- **Un champ de trop dans un message IB** (version 163) : trouvé par la
  comparaison octet pour octet, pas par la relecture.
- **Une paramétrisation mal conditionnée** (Troglodyte, variances
  rapportées à σ²_ε) : tests unitaires verts, trouvée par le premier
  `gw train` sur le binaire. Toujours faire tourner le binaire.
- **Un fichier étranger lu comme une semaine d'archive** (test d'import
  posé dans `news/`) → noms de fichiers vérifiés.

## Performance (mesurée)

Bancs : `internal/{indicator,ml/gbdt,label,data,tui,strategy/troglodyte}/bench_test.go`.

Troglodyte (Xeon 2,1 GHz du bac à sable) : 21 à 24 µs par décision
(fenêtre de 500, une allocation de 4 Ko) ; 40 ms pour estimer les
variances sur 10 000 bougies. v1_1 : 27 µs par décision, 0,31 s pour un
entraînement complet (calibrage compris) sur 10 000 bougies.

| Changement | Avant | Après |
|---|---|---|
| `RollingMax` fenêtre 50, 400 k points (file monotone) | 62,5 ms | 12,7 ms |
| Entraînement GBDT, mémoire allouée | 3 855 Mo | 72,6 Mo |
| Walk-forward de bout en bout, échantillon CPU | 1 980 ms | 1 130 ms |
| `Resample` M1→H4, 372 k bougies | 21,0 ms / 8,93 Mo | 9,0 ms / 0,16 Mo |
| Stockage d'une année de M1 | 14,9 Mo (`.gwb`) | 9,2 Mo (Parquet) |
| Lecture / écriture d'une année | 26 / 68 ms | 139 / 485 ms |

Mesuré puis **écarté** : décomposition en blocs du balayage du labeling.
