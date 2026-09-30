# Martinet — scalping de zones de liquidité

Martinet est la **troisième génération** de moteur de décision de Golden
Waterfall (après Colibri, classifieur, et Troglodyte, suivi de tendance). Il
porte le nom du martinet noir, l'oiseau le plus rapide en vol battu : c'est un
**scalpeur**, qui prend des allers courts sur de petites unités de temps.

Code : `internal/strategy/martinet/`. Révision livrée : `martinet_v1_1`
(v0.8.1) ; `martinet_v1_0` (v0.8.0, ATR seul) est retirée.

## L'idée

Au-dessus d'un plus haut récent dorment des ordres : les stops des vendeurs à
découvert et les achats sur cassure. Sous un plus bas, leurs symétriques.
Ces niveaux sont des **zones de liquidité**. Quand une bougie perce la zone,
sert ces ordres, puis **clôture de nouveau en deçà**, la cassure a échoué :
la liquidité a été prise et il ne reste plus personne pour pousser le prix
dans ce sens. Martinet prend alors le sens inverse, pour un aller court.

Le moteur est volontairement **épuré** : des plus hauts, des plus bas, un
ATR pour mettre les distances à l'échelle, et — depuis `martinet_v1_1` — le
**volume en confirmation**. Aucun oscillateur, aucune moyenne mobile.

## Pourquoi le volume s'AJOUTE à l'ATR sans le remplacer

- L'ATR est une **unité de distance** : dépassement maximal de la zone,
  tampon du stop, risque maximal. Le volume ne mesure aucune distance ; il
  ne peut pas tenir ce rôle.
- Le volume dit autre chose : un balayage qui **déclenche** des stops
  produit un afflux d'ordres, donc un pic de volume sur la bougie de
  balayage. Un dépassement sans pic est plus souvent du bruit qu'une
  chasse aux stops.
- En forex, le volume disponible est un **volume de ticks** (Dukascopy) :
  un nombre de mises à jour de prix, pas des lots échangés — une
  approximation du volume réel, dont la validité n'a pas été mesurée ici.
- **FXCM et Interactive Brokers n'en publient pas.** Rendre le volume
  obligatoire rendrait Martinet inutilisable chez eux, comme Colibri. Le
  filtre est donc **calibré** : l'entraînement choisit entre « sans
  filtre » et « avec filtre », et n'essaie le second que si l'historique a
  du volume.

## La règle

À la clôture de chaque bougie, sur les 300 dernières bougies et sur elles
seules :

1. **Zones.** Un plus haut de swing de force `p` : une bougie dont le haut
   dépasse strictement celui des `p` bougies qui la précèdent et égale au
   moins celui des `p` qui la suivent. Il doit être **confirmé** avant la
   bougie de décision, âgé d'au plus **120 bougies**, et **intact** : aucune
   bougie ne l'a dépassé depuis. Symétrique pour les plus bas.
2. **Balayage rejeté (vente).** Le haut de la bougie passe au-dessus d'une
   zone haute intacte, son close revient **sous** la zone, et le dépassement
   reste inférieur à **1 ATR(14)** — au-delà, c'est un marché qui part, pas
   une chasse aux stops. Si plusieurs zones sont balayées, la plus haute
   compte.
3. **Ordre.** Entrée au close. Stop au-delà de la mèche :
   `stop = haut + 0,1 × ATR`. Risque `R = stop − close`. Cible :
   `limite = close − rr × R`.
4. **Symétrique** sous une zone basse (achat).
5. **Filtre de volume** (si le calibrage l'a retenu) : le volume de la
   bougie de balayage doit atteindre **1,5 × la médiane** des 20 bougies
   précédentes. Un volume inconnu (NaN) dans la bougie ou dans la
   référence fait s'abstenir — jamais passer.

Filtres, tous des **conventions** de métier, jamais mesurées :

| Filtre | Valeur | Pourquoi |
|---|---|---|
| Séance | 7 h – 20 h UTC (heure de début de bougie) | De l'ouverture de Londres à l'après-midi de New York ; hors séance, les zones balayées ne se retournent pas |
| Risque maximal | 2 ATR | Une bougie de balayage démesurée donnerait un « scalp » au risque d'un swing |
| Spread maximal | 0,25 R (si le côté ask est connu) | Un scalp qui paie plus du quart de son risque en spread est perdu d'avance |
| Deux côtés balayés | abstention | Une bougie qui chasse les deux côtés ne dit rien |
| Barrière verticale | 2 heures | Un scalp qui n'a touché ni stop ni cible a raté son idée |
| Week-end | jamais porté | Pas de sens pour un scalp ; aucune entrée sur la dernière bougie de la semaine |
| Actualités | filtre déclaré | Autour d'une annonce, le spread s'écarte et un balayage n'en est plus un |

Martinet ne travaille qu'en **M1, M5 ou M15** (`Description.Timeframes`) :
l'entraînement, le backtest et la connexion live refusent toute autre unité
en le disant. Il faut régler **les deux** unités de temps —
`training.timeframe` et `broker.timeframe` — sur la même valeur.

## Entraîner = calibrer

Il n'y a rien à estimer. Entraîner Martinet sur une paire, c'est choisir,
**sur le seul jeu d'entraînement**, trois réglages parmi une grille de
douze points :

| | Valeurs essayées | Repli |
|---|---|---|
| Cible `rr` (multiples de R) | 1 ; 1,5 ; 2 | 1,5 |
| Force des pivots `p` | 3 ; 5 | 3 |
| Filtre de volume (× médiane) | sans ; 1,5 | sans |

Soit **12 points** (6 en `martinet_v1_0`). Sur un historique sans volume
mesuré, les 6 points avec filtre ne sont pas essayés et sont archivés avec
la raison (« volume non mesuré dans l'historique »). Le repli est toujours
sans filtre : il vaut partout.

Un modèle qui a retenu le filtre est **refusé** là où le volume manque :
à la chauffe sur un historique sans volume, et, en live, la paire est
marquée « ✗ volume » avec sa cause si la passerelle n'en publie pas
(Interactive Brokers). Remède : réentraîner sur l'historique de cette
source — le calibrage ne proposera alors que des réglages sans filtre.

Pour chaque point, tous les balayages du jeu sont **rejoués comme le moteur
de backtest les exécuterait** (entrée au close, stop d'abord quand stop et
cible tombent dans la même bougie, stop rempli au pire du niveau et de
l'ouverture en cas de gap, cible à son prix exact, barrière verticale,
clôture de fin de semaine, liquidation finale). Le résultat de chaque trade
est exprimé en R, net d'un spread médian par aller-retour. Le critère est le
**t de Student de la moyenne des trades** :

```
score = moyenne(R) / écart-type(R) × √n        n ≥ 30 trades
```

Le point au meilleur score est retenu ; à égalité, le premier de la grille.
Si aucun point n'a 30 trades, le repli est gardé **et signalé**
(`calibration.fallback` dans le manifeste, `calibrage_repli` dans le
rapport). Toute la grille est archivée dans `metadata.json`.

Ce choix est fait **in-sample** : ce n'est pas une mesure de performance. Le
walk-forward, qui calibre chaque pli sur son passé et le juge sur son
futur, dit seul s'il tient. La grille est volontairement petite : plus on
essaie de réglages, plus le meilleur doit sa place au hasard.

Ce que la simulation du calibrage ignore, et que le moteur applique : le
filtre d'actualités et les refus du gestionnaire de risque. Un test
(`TestCalibrationSimulationMatchesTheBacktestEngine`) confronte la
simulation au vrai moteur de backtest, trade par trade (entrée, sortie, prix,
motif) : 165, 99, 167 et, avec le filtre de volume, 44 trades identiques
sur quatre réglages, sur une série M5 synthétique.

Un modèle est rangé **par paire** et refusé au chargement hors de sa paire,
de son unité de temps, de sa révision ou de sa définition, ou si son
réglage n'est pas un point de la grille.

## Scalping et taille des positions

Avec le dimensionnement au risque (0,5 % de l'équité par défaut), la taille
vaut `risque en devise / distance au stop`. Un stop de quelques pips donne
donc une taille élevée, souvent au-delà du plafond `max_position_size`
(100 000 unités par défaut) : l'entrée est alors **ramenée au plafond**, et
le risque réellement pris est inférieur à celui demandé. C'est compté et
affiché (« N entrée(s) ramenée(s) au plafond ») par `gw train`,
`gw backtest` et les écrans. Lors de l'essai du binaire sur un historique
synthétique M5, 104 entrées out-of-sample sur 149 l'ont été. Relever le
plafond est une décision de risque, pas un réglage du moteur.

Exemple (formule du dimensionnement) : équité 10 000 USD, risque 0,5 % →
50 USD ; stop à 3 pips sur EURUSD (0,0003) → 50 / 0,0003 ≈ 166 667 unités,
ramenées à 100 000.

## Ce qu'il ne fait pas

- **Pas d'AUC** : ce n'est pas un classifieur. `ScoreOOS` répond « non
  calculable » et l'écran affiche « — ». On le juge au P&L et au profit
  factor out-of-sample du walk-forward.
- **Pas de confiance** : une règle n'a pas de probabilité. Le signal porte
  une confiance de 0 (contrat), et l'écran Live n'affiche aucun chiffre à
  côté de LONG / SHORT plutôt que « 0.00 ».
- **Pas de carnet d'ordres** : les zones sont lues dans les bougies, pas
  dans la profondeur de marché, que le programme n'a pas.

## Performance

Mesurée le 29 septembre 2026 dans le bac à sable de développement (Xeon
2,1 GHz), `internal/strategy/martinet/bench_test.go` :

| Mesure | Valeur |
|---|---|
| Une décision (fenêtre de 300 bougies M1, en séance) | 3,5 µs, aucune allocation |
| Calibrage complet (6 points, v1_0) sur 20 000 bougies M5 | 0,15 s |
| Calibrage complet (12 points, v1_1, historique avec volume) sur 20 000 bougies M5 | 0,39 s (mesuré le 29/09/2026) |

## Première mesure sur historique réel (v0.8.2, 30 septembre 2026)

**Source** : FXCM (`candledata.fxcorporate.com`, M1 bid et ask, sans
volume), téléchargée par `gw download EURUSD GBPUSD USDJPY --from 2022
--to 2025 --source fxcm` : 4 176 619 bougies, avec les semaines que FXCM
ne publie pas (1 en 2023, 4 en 2024, 4 en 2025, comptées dans les
en-têtes). **Protocole** : `gw train EURUSD GBPUSD USDJPY --tf M15` puis
`--tf M5`, configuration par défaut (capital 10 000 USD, levier 30,
0,5 % de l'équité risquée par trade, plafond 100 000 unités, graine 42),
5 plis — blocs de test consécutifs du 02/01/2024 au 31/12/2025. Aucun
calendrier d'actualités archivé pour la période : le filtre n'a rien
filtré (toutes les entrées « hors calendrier »). Agrégat OUT-OF-SAMPLE
tel qu'écrit dans `run.json` :

| | trades | gagnants | P&L net (USD) | coûts (USD) | avant coûts (USD) | profit factor | SQN |
|---|---|---|---|---|---|---|---|
| M15 | 2 814 | 986 (35,0 %) | −8 695,93 | 7 340,62 | −1 355,31 | 0,88 | −2,98 |
| M5 | 6 815 | 2 291 (33,6 %) | −18 646,28 | 20 216,55 | +1 570,28 | 0,87 | −4,94 |

Formules : profit factor = somme des gains ÷ somme des pertes (nettes de
coûts) ; « avant coûts » = P&L net + coûts (spread médian mesuré dans
l'historique, facturé moitié à l'entrée, moitié à la sortie) ;
SQN = √n × moyenne des P&L ÷ écart-type des P&L (`backtest/stats.go`).

Par paire, M15 : EURUSD 1 039 trades, PF 0,99, −258,15 ; GBPUSD 946,
PF 0,87, −3 498,74 ; USDJPY 829, PF 0,78, −4 939,03. M5 : EURUSD 2 494,
PF 0,88, −5 714,65 ; GBPUSD 2 127, PF 0,84, −8 410,56 ; USDJPY 2 194,
PF 0,90, −4 521,06. Profit factor par pli, M15 : 0,81 · 0,99 · 0,94 ·
0,72 · 0,96 ; M5 : 0,84 · 0,92 · 0,82 · 0,83 · 0,97 — aucun pli au-dessus
de 1. Sorties, M15 : 1 742 stops, 792 limites, 279 horizons, 1 fin de
semaine. Entrées ramenées au plafond : 1 425 sur 2 814 (M15), 5 215 sur
6 815 (M5).

Ce que ces chiffres permettent de dire :

- **`martinet_v1_1` perd, nette de coûts, sur ces trois paires en
  2024-2025**, en M15 comme en M5 — et ce n'est pas le hasard d'un petit
  échantillon : SQN −2,98 sur 2 814 trades, −4,94 sur 6 815.
- **Avant coûts, la règle est à peu près neutre** (−1 355 et +1 570 USD).
  Elle n'a pas d'avantage que le spread dévorerait ; elle n'en a pas du
  tout, et le spread la fait perdre. Toute révision devra d'abord montrer
  un avantage AVANT coûts, puis qu'il survit au spread.
- Le **filtre de volume** de v1_1 n'a pas pu être essayé : FXCM ne publie
  pas de volume, les points filtrés de la grille sont archivés « non
  essayés ». Il reste à mesurer sur Dukascopy.

Les décisions de ce walk-forward sont identiques, pli par pli (trades,
gagnants, perdants, motifs de sortie), à celles du binaire d'avant
v0.8.2 ; seul le P&L change légèrement, le dimensionnement lisant
désormais l'équité corrigée du backtest (M15 : −8 639,09 → −8 695,93).

Toutes les valeurs de la définition (fenêtre, âge des zones, dépassement,
tampon du stop, risque et spread maximaux, séance, barrière verticale)
restent des conventions. Une mesure qui les démentirait donne une
**nouvelle révision** (`martinet_v1_2`), qui remplacerait celle-ci —
jamais une retouche en place.
