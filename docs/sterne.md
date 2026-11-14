# Sterne — saisonnalité du dollar autour des fixings

Sterne est la **quatrième génération** de moteur de décision de Golden
Waterfall (après Colibri, classifieur ; Troglodyte, suivi de tendance ;
Martinet, scalping). Elle porte le nom de la sterne arctique, l'oiseau qui
suit le soleil autour du globe : le moteur suit l'horloge des places de
change, de Tokyo à Londres.

Code : `internal/strategy/sterne/`. Révision livrée : `sterne_v1_0`
(v0.9.0).

## L'idée, et ses sources

Le dollar s'apprécie en moyenne dans les heures qui **précèdent** un fixing
de change et se déprécie dans celles qui **suivent** — un profil en « W »
sur vingt-quatre heures, autour des fixings de Tokyo (9 h 55, heure de
Tokyo), de Francfort (BCE, 14 h 15) et de Londres (WM/Reuters, 16 h).

Sources :

- Krohn, Mueller et Whelan, « Foreign Exchange Fixings and Returns around
  the Clock », *Journal of Finance*, 2024, vol. 79, n° 1, p. 541-578
  (DOI 10.1111/jofi.13306 ;
  [notice RePEc](https://econpapers.repec.org/RePEc:bla:jfinan:v:79:y:2024:i:1:p:541-578),
  [document de travail de la Banque du Canada](https://www.bankofcanada.ca/2021/10/staff-working-paper-2021-48/)).
  Les auteurs l'attribuent à une demande de dollars aux fixings, que les
  teneurs de marché couvrent avant.
- Breedon et Ranaldo, « Intraday Patterns in FX Returns and Order Flow »,
  *Journal of Money, Credit and Banking*, 2013, vol. 45, n° 5, p. 953-965
  ([notice RePEc](https://econpapers.repec.org/RePEc:wly:jmoncb:v:45:y:2013:i:5:p:953-965)) :
  une devise tend à faiblir pendant les heures d'ouverture de sa place.

Ces articles n'ont été consultés que par leurs résumés et notices ; les
ordres de grandeur ci-dessous sont ceux **mesurés ici**, pas les leurs.

## La règle

À la clôture de chaque bougie M15, sur l'horodatage de la bougie et les
100 dernières bougies, elles seules :

1. **Fenêtre.** Si la bougie finit à l'heure d'entrée d'une fenêtre retenue
   par le modèle, sur l'horloge de **sa place** (heure d'été comprise),
   une entrée est décidée.

   | Fenêtre | Place | Entrée (heure locale) | Sens sur le dollar |
   |---|---|---|---|
   | `tokyo_avant` | Tokyo | 8 h 00 | achat |
   | `tokyo_apres` | Tokyo | 10 h 00 | vente |
   | `francfort_avant` | Francfort | 12 h 15 | achat |
   | `francfort_apres` | Francfort | 14 h 15 | vente |
   | `londres_avant` | Londres | 14 h 00 | achat |
   | `londres_apres` | Londres | 16 h 00 | vente |

2. **Sens sur la paire.** Acheter le dollar, c'est acheter `USDJPY` et
   vendre `EURUSD`.
3. **Stop.** `stop = entrée ∓ 4 × ATR(14)`, l'ATR de Wilder étant calculé
   sur les 100 dernières bougies. C'est une protection et l'unité du
   dimensionnement au risque, pas la sortie normale.
4. **Sortie.** À l'heure : barrière verticale de **2 heures**. Aucune
   cible.

La grille des six fenêtres (deux heures avant, deux heures après chacun
des trois fixings) est fixée **a priori** par la littérature, non choisie
sur les données. Durée de portage, stop à 4 ATR, seuils de sélection :
des **conventions**, jamais optimisées.

Ce que Sterne refuse, en le disant : une paire sans dollar (`EURGBP`),
une unité de temps autre que **M15** (la durée d'une bougie entre dans la
définition des fenêtres) — régler `training.timeframe` **et**
`broker.timeframe`. Aucun portage de week-end, pas de filtre d'actualités
(non déclaré : les fenêtres sont mesurées telles quelles), pas de volume.

Deux fenêtres peuvent se chevaucher (`francfort_apres` et `londres_avant`).
Une position ouverte n'est pas doublée : la première entrée court jusqu'à
son heure, la seconde est ignorée, comme pour tout moteur.

## Entraîner = retenir ou écarter chaque fenêtre

L'effet n'a pas la même force sur toutes les paires ni à tous les fixings,
et son ordre de grandeur est celui du spread. Entraîner Sterne sur une
paire, c'est rejouer **chaque fenêtre seule** sur le jeu d'entraînement,
comme le moteur de backtest l'exécuterait, et la retenir si :

```
score = moyenne(R) / écart-type(R) × √n  ≥ 2,0        et  n ≥ 250 trades
R = (sens × (sortie − entrée) − spread médian) / (4 × ATR)
```

2,0 est le seuil unilatéral d'environ 2,3 % sous l'hypothèse nulle ; 250
trades font environ une année de jours ouvrés. Tout ce qui a été essayé
est archivé dans `metadata.json` (trades, score, moyenne en R, taux de
gain, retenue ou non).

Un modèle **sans fenêtre retenue est valide** : il ne trade pas, et
l'entraînement le dit (`fenetres_retenues` = 0). C'est le garde-fou du
moteur : si l'effet disparaît de l'historique d'une paire, Sterne cesse
de la trader au réentraînement suivant.

Ce choix est fait **in-sample** : ce n'est pas une mesure de performance.
Un test (`TestCalibrationSimulationMatchesTheBacktestEngine`) confronte la
simulation du calibrage au vrai moteur de backtest, trade par trade : 93,
92 et 93 trades identiques sur trois fenêtres, sur une série M15
synthétique.

## Mesure sur historique réel (v0.9.0, 14 novembre 2026)

**Source** : FXCM (`candledata.fxcorporate.com`, M1 bid et ask, sans
volume), `gw download … --from 2012 --to 2026 --source fxcm` : 36 517 485
bougies M1 pour les sept paires en dollar, 168 semaines que FXCM ne
publie pas (comptées dans les en-têtes, jamais comblées).

**Protocole** : `gw train EURUSD GBPUSD AUDUSD NZDUSD USDJPY USDCHF USDCAD
--tf M15`, `strategy.name: sterne_v1_0`, configuration par défaut (capital
10 000 USD, levier 30, 0,5 % de l'équité risquée par trade, plafond
100 000 unités, commission 0, graine 42), 5 plis — blocs de test
consécutifs du 05/06/2019 au 06/11/2026, chaque pli calibré sur tout ce
qui le précède depuis 2012. Chaque paire est rejouée seule, sur son propre
compte, dans chaque pli ; l'agrégat somme les P&L et recalcule les ratios
sur l'ensemble des trades. Run `20261114-181838`, tel qu'écrit dans
`run.json` :

| | trades | gagnants | P&L net (USD) | coûts (USD) | avant coûts (USD) | profit factor | SQN |
|---|---|---|---|---|---|---|---|
| **Agrégat** | 11 362 | 5 711 (50,3 %) | **+5 135,82** | 7 489,20 | +12 625,03 | **1,05** | 1,86 |
| Pli 1 (05/06/2019 → 28/11/2020) | 1 919 | 971 | +667,32 | 1 157,95 | +1 825,27 | 1,04 | 0,63 |
| Pli 2 (28/11/2020 → 25/05/2022) | 1 929 | 1 006 | +2 262,53 | 1 071,17 | +3 333,70 | 1,12 | 2,00 |
| Pli 3 (25/05/2022 → 18/11/2023) | 2 212 | 1 109 | +1 550,68 | 1 513,32 | +3 064,00 | 1,07 | 1,23 |
| Pli 4 (18/11/2023 → 13/05/2025) | 2 727 | 1 376 | +2 074,31 | 2 015,48 | +4 089,79 | 1,09 | 1,57 |
| Pli 5 (13/05/2025 → 06/11/2026) | 2 575 | 1 249 | **−1 419,01** | 1 731,28 | +312,27 | 0,95 | −1,03 |

Par paire : EURUSD 3 922 trades, PF 1,09, +3 371,77 ; AUDUSD 1 425,
PF 1,16, +2 631,60 ; USDJPY 1 807, PF 1,01, +254,09 ; USDCAD 825, PF 1,02,
+142,36 ; GBPUSD 1 807, PF 0,96, −630,45 ; USDCHF 1 576, PF 0,95,
−633,53 ; NZDUSD : aucune fenêtre retenue dans aucun pli, aucun trade.
Sorties : 10 761 à l'heure, 597 stops, 4 liquidations finales. Aucune
entrée refusée, aucune ramenée au plafond.

Formules : profit factor = somme des gains ÷ somme des pertes (nettes de
coûts) ; « avant coûts » = P&L net + coûts (spread médian mesuré dans
l'historique, facturé moitié à l'entrée, moitié à la sortie) ;
SQN = √n × moyenne des P&L ÷ écart-type des P&L (`backtest/stats.go`) ;
couverture des coûts = avant coûts ÷ coûts = 12 625,03 ÷ 7 489,20 = 1,69.

Fenêtres retenues par le modèle de production (calibré sur tout
l'historique) : EURUSD `tokyo_apres`, `francfort_avant`,
`francfort_apres` ; AUDUSD `tokyo_apres` ; USDJPY `tokyo_apres` ; USDCAD
`tokyo_avant` ; USDCHF `tokyo_avant`, `francfort_apres` ; GBPUSD et
NZDUSD : aucune.

### Sensibilité à la commission

Même protocole, EURUSD GBPUSD AUDUSD seulement (la commission se règle en
unités de prix, la même pour toutes les paires : elle n'a pas le même
sens sur une paire en yens) :

| `costs.commission_per_unit` | trades | P&L net (USD) | coûts (USD) | profit factor |
|---|---|---|---|---|
| 0 (défaut) | 7 154 | +5 372,91 | 4 322,91 | 1,08 |
| 0,00002 par unité et par côté | 7 154 | **−1 338,11** | 10 811,74 | 0,98 |

0,00002 par unité et par côté fait 2 USD par côté pour 100 000 unités
d'une paire cotée en dollar, soit environ 0,18 point de base du notionnel
à 1,10.

### Ce que ces chiffres permettent de dire

- **Sterne est le premier moteur de Golden Waterfall dont le walk-forward
  est positif net du spread** sur historique réel : PF 1,05 sur 11 362
  trades, quatre plis sur cinq au-dessus de 1.
- **L'avantage est mince et n'est pas établi statistiquement** : SQN 1,86
  (le seuil usuel de 5 % bilatéral est 1,96). Le dernier pli
  (05/2025 → 11/2026) perd 1 419 USD.
- **Il ne survit pas à une commission** : avec 0,00002 par unité et par
  côté, le résultat devient négatif. La couverture des coûts de 1,69 dit
  la même chose : au-delà de coûts 1,69 fois supérieurs au spread médian
  de l'historique FXCM, plus rien ne reste. Le spread des bougies FXCM
  (médiane de l'ordre de 0,2 pip sur EURUSD) est celui d'un flux
  institutionnel brut ; le coût réel d'un compte de détail (spread majoré
  ou commission) n'a pas été vérifié ici et doit l'être **avant** tout
  usage réel. `gw train` affiche désormais cette couverture.
- **Le backtest facture le spread MÉDIAN de la paire**, pas celui de
  l'heure d'entrée. Les fenêtres de Tokyo s'ouvrent à des heures où le
  spread est un peu plus large que la médiane : le coût réel de ces
  fenêtres est sous-estimé d'autant.
- **Aucun swap n'est modélisé**, et Sterne n'en paie pas : aucune position
  ne passe 17 h à New York (la fenêtre la plus tardive ferme à 18 h,
  heure de Londres).
- Le P&L agrégé additionne des comptes séparés (une paire, un pli, un
  compte) : il n'y a ni drawdown ni Sharpe de portefeuille, et les
  fenêtres de plusieurs paires ouvertes à la même heure portent le même
  pari sur le dollar. `max_open_positions` borne ce cumul en live.

Comment la règle a été trouvée, ce qui a été essayé et écarté, et pourquoi
la mesure ci-dessus compte comme hors échantillon :
[`docs/recherche.md`](recherche.md).

## Performance

Walk-forward complet des sept paires (2012 → 2026, 5 plis, modèle de
production compris) : 19 s dans le bac à sable de développement (4 cœurs),
mesuré le 14/11/2026.

Toutes les valeurs de la définition restent des conventions. Une mesure
qui les démentirait donne une **nouvelle révision** (`sterne_v1_1`), qui
remplacerait celle-ci — jamais une retouche en place.
