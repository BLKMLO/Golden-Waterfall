# Colibri — le moteur de décision

> Colibri est le MOTEUR DE DÉCISION. Le logiciel s'appelle Golden
> Waterfall. Ne jamais confondre les deux.

Colibri est la **première génération** de moteur. Tout ce qui lui est
propre vit dans `internal/strategy/colibri/` ; le reste du programme ne
connaît que le contrat `strategy.Strategy` (voir
[`architecture.md`](architecture.md), règle 2 bis). La génération suivante
portera un autre nom d'oiseau et se branchera à côté, sans toucher aux
moteurs.

## Le principe en une phrase

Un classifieur GBDT apprend, sur des bougies étiquetées par barrières à
± 1,5 × ATR(14), la probabilité qu'un trade ouvert au close finisse
gagnant ; la stratégie entre quand cette probabilité le justifie, avec
stop et limite posés aux barrières mêmes de l'étiquetage.

## Les trois révisions

Toute évolution de la DÉFINITION (features, cible, barrières, règle de
décision) crée une **nouvelle révision**, jamais une modification en
place : un modèle archivé doit toujours vouloir dire la même chose.
v0.4.1 l'a vérifié au passage : après déplacement de tout le code de
Colibri, `colibri_v1_0` et `v1_1` produisent des modèles, des signaux et
des AUC **identiques au bit près** à ceux d'avant.

| | `colibri_v1_0` | `colibri_v1_1` | **`colibri_v1_2`** (défaut) |
|---|---|---|---|
| Modèles | un **par actif** | un, mutualisé | un, mutualisé |
| Features | 34 (jeu v1) | 34 + `symbol` | 33 (jeu v2) + `symbol` |
| Cible | P(haute avant basse) | idem | P(trade gagnant NET), **une tête par sens** |
| Fin de trade dans la cible | 5 jours calendaires | idem | **règle du moteur** : fin de semaine ISO ou 5 jours |
| Coût dans la cible | non | non | **oui** (spread médian glissant) |
| Jeu d'entraînement | tel quel | tel quel | **purgé** avant la validation |
| Probabilités | brutes | brutes | **calibrées** (rétrécies vers le taux de base) |
| Décision | p ≥ 0,55 / ≤ 0,45 | p ≥ 0,60 / ≤ 0,40 | **espérance nette ≥ 0,10 R** |
| Barrières | ± 1,5 × ATR(14) | idem | idem |
| Fichiers | `model.json` | `model.json` | `model_long.json`, `model_short.json` |

Toutes écrivent `metadata.json`, le **manifeste** par lequel le catalogue
reconnaît un modèle, et qui fige tout ce dont l'inférence a besoin.

## Le cycle complet

```
     bougies OHLCV (bid + ask)
            │
      ┌─────┴──────────────┐
      ▼                    ▼
  features             cible                features : STRICTEMENT causales (≤ t)
  (≤ t)          (issue du trade selon      cible    : regarde l'avenir — c'est
                  les règles du moteur)                ce qu'on apprend
      └─────┬──────────────┘
            ▼
   lignes où features complètes ET cible définie
            │  (+ purge des lignes qui débordent sur la validation)
            ▼
       GBDT × 2 têtes ──────► model_long.json, model_short.json, metadata.json
            │                     (+ gains/pertes moyens et calibrage mesurés)
            ▼
   P(long gagnant), P(short gagnant) à chaque bougie, calibrées
            │
   espérance nette de chaque sens, en R
            │
   ≥ 0,10 R ? → ENTER_LONG / ENTER_SHORT, sinon abstention
            ▼
   TP/SL à ± 1,5 × ATR(14) — les barrières mêmes de la cible
```

## Ce que l'analyse de v1.1 a trouvé, et ce que v1.2 en fait

### 1. Le short apprenait des gains que personne ne lui verse

La cible symétrique vaut 1 si la barrière haute est touchée d'abord, 0
sinon, et **les deux dans la même bougie comptent 0**. Pour un long, c'est
juste : le moteur suppose le stop touché d'abord. Mais la même stratégie
lit « 0 » comme « la basse d'abord », donc comme un short GAGNANT — alors
que le moteur, pour ce short, suppose aussi le stop touché d'abord, et le
compte **perdant**. La cible donnait au short exactement les cas que
l'exécution lui retire.

**v1.2 : une tête par sens.** Chacune apprend l'issue de SON trade, stop
prioritaire dans les deux sens, stop en gap rempli à l'ouverture, limite à
son prix exact — les règles de `backtest/engine.go`, rejouées bougie par
bougie par `label.Sided`.

### 2. La cible ne voyait pas les coûts

Un trade qui touche sa limite mais rend moins que le spread est une perte
dans les statistiques du backtest (`Trade.IsWin` est net). La cible
symétrique l'appelait gain.

**v1.2 : cible NETTE.** Le coût d'un aller-retour est estimé par la
médiane glissante et causale du spread sur 100 bougies (`spreadCost`),
la même estimation servant à l'entraînement et à la décision. Sans côté
ask, le coût est inconnu : la cible est brute, et le manifeste le dit
(`costs_modelled: false`).

### 3. La cible et l'exécution ne finissaient pas le trade au même moment

La cible accordait cinq jours calendaires ; le moteur liquide à la fin de
la semaine ISO, soit moins de cinq jours après n'importe quelle entrée de
semaine. La barrière de cinq jours ne se déclenchait donc jamais sur des
données forex — et la cible étiquetait des trades que l'exécution avait
déjà fermés le vendredi.

**v1.2 : même fenêtre que le moteur** (`label.ExecutionWindow`) : la
première bougie qui soit la dernière de sa semaine ISO, ou dont la barrière
verticale a expiré — deux règles prises dans `core/horizon.go`, là où le
moteur les prend. Un test confronte la cible au moteur de backtest sur 60
entrées tirées au hasard : même issue, même bougie de sortie, à chaque
fois.

Au passage, un défaut du moteur lui-même : il pouvait OUVRIR une position
sur la dernière bougie de la semaine, qui traversait alors tout le
week-end. Corrigé pour toutes les stratégies.

### 4. Des seuils de probabilité fixes ignoraient le coût du moment

`p ≥ 0,60` dit la même chose que le spread vaille un centième ou un
dixième de barrière. Or l'espérance d'un trade en dépend directement.

**v1.2 : décision à l'espérance.** Pour chaque sens *s* :

```
E_s = p_s · W_s − (1 − p_s) · L_s − c / (k · ATR_t)
```

- `p_s` : probabilité calibrée que le trade finisse gagnant net ;
- `W_s`, `L_s` : issue brute MOYENNE, en R, des trades gagnants et
  perdants de l'entraînement — **mesurées**, pas supposées à ± 1 R (les
  sorties au temps et les stops en gap le démentent) ;
- `c` : coût aller-retour estimé à la bougie, `k · ATR_t` : la barrière ;
- entrée dans le sens de plus grande espérance si elle atteint **0,10 R**.

À coût nul et `W = L = 1`, la règle se lit `p ≥ 0,55` ; chaque dixième de
barrière de spread relève le seuil de 0,05.

### 5. Le jeu d'entraînement fuyait dans sa propre validation

La validation de l'arrêt anticipé est la queue (15 %) de chaque symbole.
Mais le label des dernières lignes d'entraînement regarde plusieurs jours
en avant — dans la période de validation. L'arrêt anticipé choisissait
donc son nombre d'arbres sur une perte flattée par ce recouvrement.

**v1.2 : purge.** Toute ligne d'entraînement dont le trade se termine
après la première bougie de validation est retirée (le nombre est écrit
au manifeste, `purged`).

### 6. Des probabilités trop confiantes

Un GBDT arrêté tôt est à peu près calibré sur ce qu'il a appris, pas sur
ce qu'il n'a pas vu. **v1.2 rétrécit** les probabilités de chaque tête
vers son taux de base d'entraînement, `p = σ(c + A · (s − c))`, avec
`A ∈ [0, 1]` ajusté sur la validation (`gbdt.FitShrinkage`) : à `A = 0`,
le modèle ne sait rien et ne décide plus rien.

Deux versions plus libres ont été essayées et écartées sur mesure :

- une **pente libre** : la validation ayant servi à choisir le nombre
  d'arbres, elle est biaisée en faveur du modèle ; sur une marche au
  hasard, la pente y valait 1,2 à 2,2 et AMPLIFIAIT le bruit en signaux ;
- une **ordonnée libre** (Platt complet) : ajustée sur la période la plus
  récente, elle absorbait la tendance de ces quelques mois et alternait
  de signe d'un pli à l'autre (+0,27, −0,24, +0,19, −0,32 en log-odds),
  soit un pari directionnel plus grand que tout le pouvoir de
  discrimination du modèle.

Le calibrage ne peut donc que retirer de la confiance, jamais ajouter une
opinion.

### 7. Des features exprimées dans la mauvaise unité

Le jeu v1 mesure les écarts de prix en fraction du prix (`close/sma − 1`,
`macd/close`). « 0,5 % sous la moyenne » ne dit pas la même chose sur
EURCHF et sur GBPJPY ; « 1,2 ATR sous la moyenne » si — et c'est l'unité
même des barrières. Pour un modèle mutualisé, c'est la différence entre
apprendre le marché et apprendre l'instrument. Un arbre est insensible à
une transformation monotone d'UNE colonne, pas à une normalisation qui
varie d'une ligne à l'autre : le changement n'est pas cosmétique.

**Jeu v2** (33 colonnes causales) :

| Famille | Features |
|---|---|
| Retours, en unités de volatilité | `ret_z_{1,3,5,10,20}` = ln(Cₜ/Cₜ₋ₙ) / (σ₂₀ · √n) |
| Position | `range_pos_20`, `gap_atr` |
| Tendance, en ATR | `sma_dev_atr_{10,20,50}`, `sma_slope_atr_20`, `ema_dev_atr_{12,26}`, `macd_atr`, `macd_signal_atr`, `macd_hist_atr`, `adx_14` |
| Momentum | `rsi_{7,14}`, `stoch_k_14`, `stoch_d_14` |
| Volatilité (le régime) | `atr_norm_14`, `realized_vol_20`, `bb_width_20`, `vol_ratio_10_50` |
| Volume (ticks) | `vol_rel_20`, `vol_spike_20`, `obv_z_20` |
| Coût | `spread_atr` (optionnelle : NaN sans côté ask) |
| Calendrier | `dow`, `hour_utc`, `session`, `hours_to_week_close` |

Retirées : `roc_5`, `roc_10` (roc = e^ret − 1, fonction monotone de
`ret_log` : pour un arbre, deux copies de la même colonne) et
`days_to_friday` (fonction de `dow`). `hours_to_week_close` la remplace :
la cible v1.2 est bornée par la clôture de fin de semaine, et le temps
restant décide directement des sorties au temps.

### Ce qui a été mesuré puis écarté

**La pondération par l'unicité des labels** (`label.AverageUniqueness`,
López de Prado) : des labels qui se chevauchent racontent le même épisode,
et la théorie veut qu'on les pondère par leur unicité moyenne. Le
mécanisme est écrit et testé (poids d'échantillon dans le GBDT,
renormalisés à une moyenne de 1). Mais sur l'ablation, il fait égal ou
moins bien que sans, sur les quatre marchés : il n'est donc **pas
activé** en v1.2 ; une révision future pourra le re-mesurer sur des
données réelles.

**Les hyperparamètres du GBDT** ne sont pas retouchés : sans historique
réel dans l'environnement de développement, les régler reviendrait à les
ajuster sur des marchés synthétiques dont le signal est connu d'avance.

## La mesure (ablation)

`internal/strategy/colibri/ablation_test.go`, reproductible :

```bash
GW_ABLATION=1 go test ./internal/strategy/colibri -run Ablation -v
```

Walk-forward complet (4 plis, 3 paires, 4 ans, H4, spread 1e-4 du prix),
sommé sur **8 graines** par marché. Quatre marchés SYNTHÉTIQUES : une
ancre sinusoïdale lente et un rappel vers elle de force 0,05, 0,02 ou 0
(marche au hasard) par bougie H4, plus un marché écrit en H1 avec un
rappel de 0,02 par heure puis ré-échantillonné en H4 (fort retour à la
moyenne, d'une autre texture). Chaque case : trades · P&L net · profit
factor (gains bruts / pertes brutes, sur la somme des huit graines).

| Variante | Rappel fort | Rappel faible | Marche au hasard | Rappel horaire |
|---|---|---|---|---|
| `colibri_v1_1` | 958 · +144,57 · 1,14 | 61 · −21,09 · 0,76 | 138 · −76,48 · 0,63 | 4 848 · +2 180,41 · **1,46** |
| **`colibri_v1_2`** (0,10 R) | 1 352 · **+331,31** · **1,24** | 156 · **−6,80** · **0,97** | 279 · **−64,87** · **0,83** | 5 279 · +1 979,44 · 1,36 |
| marge 0 R | 7 714 · +719,11 · 1,08 | 3 640 · −309,46 · 0,93 | 3 978 · −336,33 · 0,93 | 12 136 · +3 381,07 · 1,26 |
| marge 0,05 R | 3 306 · +657,68 · 1,19 | 767 · −42,71 · 0,95 | 888 · −116,69 · 0,89 | 8 372 · +3 051,59 · 1,35 |
| marge 0,15 R | 570 · +186,08 · 1,33 | 29 · −2,77 · 0,93 | 74 · −10,64 · 0,89 | 3 075 · +1 035,41 · 1,32 |
| marge 0,20 R | 242 · +84,97 · 1,37 | 1 · +2,70 · ∞ | 23 · +15,87 · 1,89 | 1 683 · +729,64 · 1,43 |
| features v1 au lieu de v2 | 1 354 · +289,13 · 1,20 | 379 · −61,77 · 0,88 | 216 · −44,59 · 0,84 | 5 077 · +1 960,20 · 1,38 |
| avec poids d'unicité | 1 467 · +362,44 · 1,24 | 255 · −37,43 · 0,88 | 332 · −95,85 · 0,79 | 5 040 · +1 754,94 · 1,33 |
| sans purge | 1 401 · +285,46 · 1,19 | 219 · +12,64 · 1,05 | 183 · −37,68 · 0,85 | 5 466 · +2 045,80 · 1,36 |
| sans calibrage | 1 607 · +319,98 · 1,19 | 288 · −41,79 · 0,89 | 322 · −55,27 · 0,87 | 5 750 · +2 149,25 · 1,36 |

P&L en dollars de compte, taille fixe de 1 000 unités : c'est le SIGNE et
le rapport entre variantes qui comptent. AUC OOS moyenne : 0,53 sur le
rappel fort, 0,50 sur le rappel faible et la marche au hasard, 0,586 pour
v1_1 et 0,574 pour toutes les variantes v1_2 sur le rappel horaire.

Ce qu'on peut en conclure, et ce qu'on ne peut pas :

- **Sur la marche au hasard, tout gain est de la chance** : l'espérance
  vraie y est négative (le spread). S'y mesurent le nombre de trades pris
  sur du bruit et l'ampleur de la perte. Même lecture, en partie, pour le
  rappel faible (AUC 0,50 : rien n'y est appris).
- **v1_2 ne domine pas v1_1 partout.** Elle fait mieux sur trois marchés
  sur quatre, en profit factor comme en P&L. Elle fait **moins bien sur le
  rappel horaire** (PF 1,36 contre 1,46, P&L −9 %), le marché au signal le
  plus facile — AUC 0,586, un niveau que le forex réel offre rarement. Là,
  la cible symétrique de v1_1, qui apprend une seule direction avec deux
  fois plus d'exemples par modèle, discrimine mieux (0,586 contre 0,574) ;
  aucune marge ne rattrape cet écart de PF.
- **La marge est indispensable** : sans elle, des milliers de trades et
  des pertes dès que le signal manque. **0,10 R est la seule marge** qui
  fasse au moins aussi bien que v1_1 en P&L total sur les quatre marchés
  (2 239 contre 2 227) tout en perdant moins qu'elle sur les deux marchés
  sans signal exploitable. ⚠ Ce critère a été formulé APRÈS la mesure :
  c'est un choix raisonné, pas une preuve. À coût nul et gains/pertes de
  ± 1 R, 0,10 R revient à p ≥ 0,55 — le seuil de v1_0.
- **Poids d'unicité** : égaux ou moins bons partout → écartés.
- **Calibrage** : meilleur sur les deux premiers marchés, égal sur le
  quatrième, un peu moins bon sur la marche au hasard → gardé.
- **Purge** : effet mitigé, gardée sans condition — c'est une correction
  de FUITE, et une fuite ne se garde pas parce qu'elle ne coûte rien sur
  un marché donné.
- **Features v2** : meilleures sur les deux premiers marchés, à peu près
  égales ailleurs. Trois paires synthétiques de même volatilité n'ont pas
  l'hétérogénéité qui justifie la normalisation : la raison principale de
  v2 reste théorique, sans preuve empirique à ce jour.
- **Le calibrage à ordonnée libre** (première version de v1_2) mesurait
  mieux sur les trois premiers marchés. Mais l'ancre sinusoïdale lente du
  générateur rend la tendance récente prédictive PAR CONSTRUCTION, et
  l'ordonnée l'exploitait : la garder reviendrait à ajuster la stratégie
  au générateur de test.

Pourquoi `colibri_v1_2` est malgré tout la révision par défaut : elle
apprend ce que l'exécution délivre (v1_1 donne au short des gains
fictifs et ignore les coûts), et elle perd moins que v1_1 sur les
marchés sans signal exploitable — le cas le plus proche d'une paire de
devises réelle. Une configuration existante qui nomme `colibri_v1_1` la
garde ; et `gw train` puis `gw runs` sur l'historique réel diront
laquelle des deux mérite d'être gardée.

⚠ **Ce ne sont pas des performances de marché.** Un marché synthétique a
un signal connu, un spread constant et des paires de volatilité
identique : la mesure dit si une révision se comporte comme sa conception
le prévoit — s'abstenir quand il n'y a rien, trader quand il y a quelque
chose — pas ce qu'elle gagnera. Elle ne peut en particulier RIEN dire du
jeu de features v2, dont l'intérêt tient à l'hétérogénéité des
instruments réels. Le chiffre qui compte reste le walk-forward de
l'utilisateur sur son propre historique (`gw train`), à comparer entre
révisions par `gw runs`.

## Les garde-fous, toutes révisions

### Anti-fuite

- **Stabilité par préfixe des features** : `compute(série)[:k] ==
  compute(série[:k])` pour tout k (tests des deux jeux).
- **Stabilité par préfixe des DÉCISIONS**, dans le banc de conformité
  commun : décider à la bougie i avec ou sans la suite de la série donne
  exactement le même signal. C'est ce qui interdit, par exemple, une
  médiane de spread calculée sur toute la série.
- **Fenêtre avant incomplète → pas de label** : jamais une cible calculée
  sur quelques bougies au lieu de plusieurs jours.
- **Anti-fuite entre actifs** : features et cible calculées symbole par
  symbole, colonne `symbol` ajoutée après, validation prise sur la queue
  de CHAQUE symbole.

### Un modèle appliqué à de mauvaises colonnes est refusé

Au chargement, les colonnes du modèle ET du manifeste sont comparées **par
nom et par ordre** à celles de la révision ; un modèle d'une autre
révision est refusé ; un modèle v1.2 sans les mesures d'une de ses têtes
aussi. Le message nomme la première colonne fautive.

### Un modèle par paire, même dans une seule instance

Une seule instance de stratégie sert toutes les paires en live. Pour une
révision mono-actif (v1.0), chaque chauffe écrasait autrefois le modèle de
la précédente : la dernière paire chargée imposait SON modèle à toutes les
autres. Les modèles sont désormais rangés par paire (v1.0) ou partagés
(révisions mutualisées).

### L'encodage de `symbol`

La liste ordonnée des symboles vus à l'entraînement est figée dans le
manifeste et rechargée avec le modèle. Un symbole inconnu reçoit une
valeur manquante : le modèle n'a rien appris sur lui, et c'est dit.

## Lire un résultat sans se mentir

| Métrique | Ce qu'elle dit |
|---|---|
| **AUC out-of-sample** | La seule qui mesure une compétence. **0,50 = hasard.** En v1.2, moyenne des AUC des deux têtes, chacune contre sa propre cible — elle n'est donc pas directement comparable à celle de v1.1. |
| AUC d'entraînement | Mesure la MÉMORISATION. L'écart avec l'AUC OOS **est** le surapprentissage. |
| Taux de gain OOS | Dépend des coûts et du départage stop/TP. À lire avec le profit factor. |
| Profit factor | `gains / pertes`. « ∞ » signifie « aucune perte », pas « excellent ». |
| SQN | Qualité rapportée à la dispersion, pondérée par le nombre de trades. |
| Sharpe | Annualisé sur la cadence RÉELLE des bougies. Taux sans risque supposé nul. |

Un backtest lancé depuis l'écran **Backtest** utilise le modèle de
production, entraîné sur tout l'historique : il est **IN-SAMPLE** et le
dit. Le chiffre honnête est toujours l'agrégat out-of-sample du
walk-forward.

## Ce qui reste volontairement ABSENT

Pas de features cross-actifs (corrélation DXY/S&P, VIX) ni de proximité
d'événements macro (NFP, CPI) : les deux exigent une source de données
externe. Les ajouter à moitié serait pire que ne pas les avoir — c'est le
terrain de la génération suivante.
