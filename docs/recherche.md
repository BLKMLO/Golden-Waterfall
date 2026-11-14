# Journal de recherche — comment un moteur est cherché, et ce qui a été écarté

Ce document consigne la recherche qui a conduit à Sterne (v0.9.0,
14 novembre 2026) : les hypothèses, le protocole, **tout** ce qui a été
essayé — y compris ce qui n'a rien donné. Un résultat de backtest ne vaut
que si l'on sait combien d'essais l'ont précédé.

## Point de départ

Mesures refaites le 14/11/2026 sur l'historique FXCM complet des sept
paires en dollar (EURUSD, GBPUSD, AUDUSD, NZDUSD, USDJPY, USDCHF, USDCAD ;
2012 → 2026, 36 517 485 bougies M1), configuration par défaut, 5 plis,
blocs de test du 05/06/2019 au 06/11/2026 :

| Moteur | Unité | trades | P&L net (USD) | coûts (USD) | profit factor | SQN |
|---|---|---|---|---|---|---|
| `martinet_v1_1` | M15 | 24 560 | −88 400,05 | 59 417,78 | 0,85 | −11,60 |
| `troglodyte_v1_1` | H1 | 1 205 | −1 643,32 | 537,60 | 0,93 | −0,76 |
| `colibri_v1_2` | — | non mesurable : exige un volume que FXCM ne publie pas | | | | |

Martinet perd dans les cinq plis et sur les sept paires (PF de 0,82 à
0,89). Troglodyte ne gagne que sur USDJPY (433 trades, PF 1,52) et perd
sur les six autres. Aucun moteur n'était rentable.

Le diagnostic : les deux règles n'ont pas d'avantage **avant** coûts ;
aucun réglage de paramètres ne crée ce qui n'existe pas. Il fallait une
hypothèse qui ait une raison économique documentée d'exister.

## Protocole

Trois périodes, fixées avant de regarder :

| Période | Rôle |
|---|---|
| 2012 → 2019 | **Découverte** : explorer, choisir |
| 2020 → 2022 | **Validation** : confirmer ou écarter ce que la découverte suggère |
| 2023 → 2026 | **Test final** : regardé UNE fois, avec la définition figée |

Règles : prix **exécutables** (achat à l'ask, vente au bid) dès la
première mesure ; hypothèses tirées de la littérature, pas d'un balayage
de paramètres ; toute variante essayée est consignée ci-dessous ; la
définition de `sterne_v1_0` (fenêtres, durée, stop, seuils) a été figée
et testée **avant** le run sur l'historique complet, et n'a pas été
retouchée après.

L'exploration a été faite hors du binaire (scripts Python sur les Parquet
de `gw download`), la mesure finale par `gw train`, seule à faire foi.

## Ce qui a été essayé

1. **Profil horaire du rendement** (EURUSD, GBPUSD, USDJPY ; heure de
   New York). Constat sur 2012-2019 : EURUSD baisse de 4 h à 8 h
   (−0,61, −0,42, −0,77, −0,97 pb par heure ; t de −1,9 à −4,0), le même
   signe en 2020-2022. Un rendement à 16 h (heure de bascule de valeur,
   spread médian de 1,3 à 2,7 pb) a été écarté comme artefact probable du
   prix milieu.
2. **Profil du dollar par demi-heure, puis par quart d'heure**, sur les
   horloges de Londres et de Tokyo : hausse du dollar de 11 h 30 à 13 h
   (Londres), soit avant le fixing de la BCE, +1,19 pb sur 2012-2019
   (t de 4,69 et 5,86 sur deux des trois demi-heures) et +0,91 pb sur
   2020-2022 ; baisse
   après le fixing de Tokyo. C'est le profil décrit par Krohn, Mueller et
   Whelan (2024).
3. **Six fenêtres de deux heures** (avant et après chaque fixing) × sept
   paires = 42 cases, prix exécutables. Sur 2012-2019, t net ≥ 2,5 pour
   `tokyo_apres` EURUSD (3,62) et USDJPY (2,98), `francfort_avant` EURUSD
   (5,14), USDJPY (2,53), USDCHF (2,60), `londres_apres` EURUSD (2,90).
   Sur 2020-2022 : `tokyo_apres` positive nette sur les sept paires
   (de +0,45 à +1,50 pb par trade), `francfort_avant` EURUSD +0,83 pb ;
   `londres_apres` EURUSD négative (−0,62 pb) — non confirmée.
4. **Émulation du walk-forward** (sélection des cases à t ≥ seuil sur le
   passé, jugement l'année suivante, 2017 → 2022) :

   | Seuil | trades | net moyen (pb) | t | Sharpe annualisé |
   |---|---|---|---|---|
   | 2,0 | 9 582 | +0,321 | 2,34 | 0,73 |
   | 2,5 | 8 287 | +0,305 | 2,14 | 0,70 |
   | 3,0 | 4 144 | +0,364 | 1,86 | 0,72 |

   Les trois seuils donnent la même chose : 2,0 a été gardé, sans autre
   raison que d'être le seuil conventionnel.
5. **Fenêtres plus longues** (matinée européenne de 7 h à 13 h 15, quatre
   et six heures après Tokyo, cinq heures après Londres) : aucune
   n'améliore le net moyen de façon cohérente entre découverte et
   validation. **Écartées** — la grille de deux heures est restée.
6. **Vérification par le binaire sur 2012-2022** (sept paires, tests de
   mi-2017 à 2022) : 7 970 trades, PF 1,10, +7 500,11 USD nets. Conforme
   à l'émulation.
7. **Test final**, une seule fois, sur 2012-2026 : tableau de
   [`docs/sterne.md`](sterne.md). Les plis 4 et 5 (18/11/2023 →
   06/11/2026) sont entièrement dans la période jamais regardée : +2 074,31
   puis −1 419,01 USD, soit +655,30 USD sur 5 302 trades.
8. **Sensibilité à la commission**, après le test final et sans rien
   modifier : négatif à 0,00002 par unité et par côté (`docs/sterne.md`).

Jamais essayé, donc à ne pas supposer : un stop autre que 4 ATR, une durée
autre que deux heures (hors point 5), un filtre par jour de la semaine ou
par jour « gotobi », les paires sans dollar, un ordre à cours limité.

## Ce que la recherche établit, et ce qu'elle n'établit pas

- L'effet des fixings **existe dans cet historique** avant coûts
  (+12 625 USD avant coûts sur le test, contre −1 355 pour Martinet en
  v0.8.3) et il est décrit indépendamment par la littérature.
- Il est **du même ordre que les coûts** : environ un point de base par
  trade avant coûts, 0,3 net au spread FXCM. Sa rentabilité réelle dépend
  entièrement du coût d'exécution du compte.
- La période la plus récente est la plus faible. Rien ne permet de dire
  si c'est du bruit ou une érosion de l'effet depuis sa publication.
- Quarante-deux cases et une poignée de variantes ont été regardées : le
  SQN de 1,86 du test doit être lu avec ce nombre d'essais en tête.

## Pistes, par ordre de valeur attendue

1. **Mesurer le coût réel** d'un compte (spread à l'heure d'entrée,
   commission) et le régler dans `costs.commission_per_unit` avant tout
   usage : c'est lui qui décide.
2. **Facturer le spread de la bougie d'entrée** plutôt que la médiane de
   la paire, dans le moteur de backtest : la mesure deviendrait exacte
   pour un moteur horaire. Change des résultats publiés.
3. **Exécution à cours limité** (entrer au bid pour un achat) : diviserait
   le coût, mais demande un modèle de remplissage que le backtest n'a pas.
4. **Sélection consciente des coûts** : passer la commission au calibrage
   pour qu'une fenêtre ne soit retenue que si elle la couvre.
5. **Dimensionner par pari** : les fenêtres simultanées de plusieurs
   paires sont le même pari sur le dollar (reste-à-faire « exposition
   croisée »).
