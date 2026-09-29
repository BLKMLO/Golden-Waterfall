# Martinet — scalping de zones de liquidité

Martinet est la **troisième génération** de moteur de décision de Golden
Waterfall (après Colibri, classifieur, et Troglodyte, suivi de tendance). Il
porte le nom du martinet noir, l'oiseau le plus rapide en vol battu : c'est un
**scalpeur**, qui prend des allers courts sur de petites unités de temps.

Code : `internal/strategy/martinet/`. Révision livrée : `martinet_v1_0`.

## L'idée

Au-dessus d'un plus haut récent dorment des ordres : les stops des vendeurs à
découvert et les achats sur cassure. Sous un plus bas, leurs symétriques.
Ces niveaux sont des **zones de liquidité**. Quand une bougie perce la zone,
sert ces ordres, puis **clôture de nouveau en deçà**, la cassure a échoué :
la liquidité a été prise et il ne reste plus personne pour pousser le prix
dans ce sens. Martinet prend alors le sens inverse, pour un aller court.

Le moteur est volontairement **épuré** : des plus hauts, des plus bas, et un
ATR pour mettre les distances à l'échelle. Aucun oscillateur, aucune
moyenne mobile, aucun volume (il fonctionne donc sur FXCM et sur Interactive
Brokers, qui n'en publient pas).

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
**sur le seul jeu d'entraînement**, deux réglages parmi une grille de six
points :

| | Valeurs essayées | Repli |
|---|---|---|
| Cible `rr` (multiples de R) | 1 ; 1,5 ; 2 | 1,5 |
| Force des pivots `p` | 3 ; 5 | 3 |

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
motif) : 165, 99 et 167 trades identiques sur trois réglages, sur une série
M5 synthétique.

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
| Calibrage complet (6 points) sur 20 000 bougies M5 | 0,15 s |

## Jamais mesuré

Martinet n'a **jamais été mesuré sur un historique réel** : tous les chiffres
ci-dessus viennent de séries synthétiques. Toutes les valeurs de la
définition (fenêtre, âge des zones, dépassement, tampon du stop, risque et
spread maximaux, séance, barrière verticale) sont des conventions. Une
mesure qui les démentirait donnerait une **nouvelle révision**
(`martinet_v1_1`), qui remplacerait celle-ci — jamais une retouche en place.
