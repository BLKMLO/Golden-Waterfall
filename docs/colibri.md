# Colibri — le moteur de décision

> Colibri est le MOTEUR DE DÉCISION. Le logiciel s'appelle Golden
> Waterfall. Ne jamais confondre les deux.

## Le principe en une phrase

Un classifieur binaire apprend, sur des bougies étiquetées par triple
barrière, la probabilité que la barrière **haute** soit touchée avant la
**basse**. Probabilité élevée → long, faible → short, entre les deux →
abstention.

Le label étant **symétrique**, un seul modèle sert les deux sens :
`P(haute d'abord)` est aussi bien `1 − P(basse d'abord)`.

## Le cycle complet

```
     bougies OHLCV (bid)
            │
      ┌─────┴─────┐
      ▼           ▼
  features     labels          features : STRICTEMENT causales (≤ t)
  (34, ≤ t)   (triple          labels   : regardent l'avenir — c'est la cible
              barrière)
      └─────┬─────┘
            ▼
   lignes où features complètes ET label défini
            │
            ▼
         GBDT  ──────────►  model.json + metadata.json
            │
            ▼
   P(haute d'abord) à chaque bougie
            │
   ┌────────┴────────┐
   ▼                 ▼
 ≥ seuil long    ≤ seuil short
 ENTER_LONG      ENTER_SHORT
   │                 │
   └────────┬────────┘
            ▼
   TP/SL à ±1,5 × ATR(14)  — le MÊME multiple qu'au labeling
```

Le filtre « features complètes ET label défini » retire, en une seule
opération, la **chauffe** au début (les fenêtres glissantes ne sont pas
encore remplies) et la **queue non étiquetable** à la fin (l'horizon
dépasse les données disponibles).

## Les 34 features

Toutes causales : la valeur à la bougie *t* n'utilise que des données
≤ *t*. Aucune fenêtre centrée, aucun décalage négatif.

| Famille | Features |
|---|---|
| Retours | `ret_log_{1,3,5,10,20}` |
| Position dans le range | `range_pos_20`, `gap_open` |
| Tendance | `sma_dev_{10,20,50}`, `sma_slope_20`, `ema_dev_{12,26}`, `macd`, `macd_signal`, `macd_hist`, `adx_14` |
| Momentum | `rsi_{7,14}`, `stoch_k_14`, `stoch_d_14`, `roc_{5,10}` |
| Volatilité | `atr_norm_14`, `realized_vol_20`, `bb_width_20`, `vol_ratio_10_50` |
| Volume | `vol_rel_20`, `vol_spike_20`, `obv_z_20` |
| Calendrier | `dow`, `days_to_friday`, `hour_utc`, `session` |

Trois points qui ne sont pas des détails :

- **le MACD est normalisé par le prix.** Sans cela, un MACD d'EURUSD
  (≈ 1,08) et d'USDJPY (≈ 150) ne vivent pas sur la même échelle, et un
  modèle mutualisé apprendrait l'instrument au lieu du marché ;
- **l'OBV est ramené à un z-score glissant.** L'OBV brut est un cumul sans
  borne : tel quel, le modèle apprendrait la DATE ;
- **le volume Dukascopy en forex est un volume de TICKS**, pas un volume
  réel. Les features de volume mesurent donc l'activité, pas les montants.

### La garantie anti-fuite

Un test vérifie la **stabilité par préfixe** :

```
Compute(série)[:k] == Compute(série[:k])   pour tout k
```

Une feature qui regarderait vers l'avant verrait sa valeur à l'indice *k*
changer selon qu'on lui donne la suite de la série ou non — et le test
échoue. C'est le garde-fou le moins cher et le plus efficace contre la
fuite temporelle.

### Ce qui est volontairement ABSENT

Pas de features cross-actifs (corrélation DXY/S&P, VIX) ni de proximité
d'événements macro (NFP, CPI). Les deux exigeraient une source de données
externe. Les ajouter à moitié serait pire que ne pas les avoir.

## Le labeling : triple barrière

Pour chaque bougie *t*, trois barrières depuis son close :

- **haute** : `close_t + 1,5 × ATR_t` ;
- **basse** : `close_t − 1,5 × ATR_t` ;
- **verticale** : horizon de 5 jours calendaires.

Label = `1` si la haute est touchée d'abord, `0` sinon. Si l'horizon expire
sans barrière touchée, on étiquette par le signe du retour.

**Deux règles qui font toute la différence :**

1. **Les deux barrières dans la même bougie → on retient la BASSE.**
   C'est exactement la convention du moteur d'exécution, qui suppose le
   stop touché d'abord faute de connaître l'ordre intrabar réel.
   Entraîner sur une règle plus optimiste apprendrait au modèle des gains
   que l'exécution ne délivre jamais : la cible doit décrire ce que le
   programme obtiendra vraiment.

2. **Fenêtre avant incomplète → pas de label.** Une bougie dont l'horizon
   dépasse la fin des données reçoit `NaN`, jamais un label calculé sur
   une fenêtre tronquée. Sans cette règle, la queue de chaque bloc serait
   étiquetée sur quelques minutes au lieu de cinq jours — un label faux,
   non-NaN, donc conservé par le filtrage. Ce n'est pas une fuite : c'est
   du bruit appris comme un signal.

**L'ATR a une source unique** (`feature.ATR`), utilisée par le labeling ET
par l'inférence. Mêmes valeurs des deux côtés, aucune divergence possible
entre ce que le modèle apprend et ce que l'exécution vise.

## Les deux versions

Toute évolution de la DÉFINITION (features, barrières, seuils) crée une
**nouvelle version**, jamais une modification en place — sinon un modèle
archivé ne voudrait plus rien dire.

| | `colibri_v1_0` | `colibri_v1_1` |
|---|---|---|
| Modèles | un **par actif** | **un seul**, mutualisé |
| Features | 34 | 34 + `symbol` (catégorielle) |
| Seuil long / short | 0,55 / 0,45 | **0,60 / 0,40** |
| Barrières | 1,5 × ATR(14), 5 jours | identiques |
| Unité de temps conseillée | H1–H4 | **H4** |

Pourquoi v1.1 mutualise : chaque paire prise seule a peu d'historique. Un
modèle unique mutualise la statistique tout en laissant la feature
`symbol` capter les spécificités de chaque actif.

Pourquoi les seuils sont plus sélectifs : en v1.0, les probabilités montraient
des signaux sur plus de 60 % des bougies — un coût de spread supérieur à
l'avantage statistique.

**Anti-fuite entre actifs** (v1.1) : features et labels sont calculés
**symbole par symbole**, jamais sur une concaténation des OHLC bruts (les
fenêtres glissantes déborderaient d'un actif sur l'autre). La colonne
`symbol` est ajoutée après, et la queue de validation de l'arrêt anticipé
est prise sur CHAQUE symbole séparément.

**Encodage de `symbol`** : la liste ordonnée des symboles vus à
l'entraînement est figée, persistée dans `metadata.json` et rechargée avec
le modèle. Sans cette liste, un modèle rechargé associerait le code 3 à une
autre paire qu'au fit — des probabilités crédibles et fausses. Un symbole
inconnu reçoit une valeur manquante, ce qui est honnête : le modèle n'a
rien appris sur lui.

## Lire un résultat sans se mentir

| Métrique | Ce qu'elle dit |
|---|---|
| **AUC out-of-sample** | La seule qui mesure une compétence. **0,50 = hasard.** En dessous de 0,52, il n'y a rien. |
| AUC d'entraînement | Mesure la MÉMORISATION. L'écart avec l'AUC OOS **est** le surapprentissage. |
| Taux de gain OOS | Dépend des coûts et du départage stop/TP. À lire avec le profit factor. |
| Profit factor | `gains / pertes`. « ∞ » signifie « aucune perte », pas « excellent ». |
| SQN | Qualité rapportée à la dispersion, pondérée par le nombre de trades. |
| Sharpe | Annualisé sur la cadence RÉELLE des bougies. Taux sans risque supposé nul. |

Un backtest lancé depuis l'écran **Backtest** utilise le modèle de
production, entraîné sur tout l'historique : il est **IN-SAMPLE** et le
dit. Le chiffre honnête est toujours l'agrégat out-of-sample du
walk-forward.
