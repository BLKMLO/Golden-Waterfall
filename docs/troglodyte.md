# Troglodyte — suivi de tendance structurel

> Troglodyte est un MOTEUR DE DÉCISION. Le logiciel s'appelle Golden
> Waterfall. Une génération de moteur = un nom d'oiseau : Colibri est la
> première, Troglodyte la deuxième.

Tout ce qui lui est propre vit dans `internal/strategy/troglodyte/`. Le
reste du programme ne connaît que le contrat `strategy.Strategy` (voir
[`architecture.md`](architecture.md), règle 2 bis).

## Le principe en une phrase

Le logarithme du prix est décrit comme un **niveau** qui avance d'une
**pente** à chaque bougie, chacun secoué par ses propres chocs ; un
**filtre de Kalman** estime cette pente et son incertitude, et la
stratégie suit la tendance tant que la pente est nettement non nulle.

## Pourquoi cette approche, et pas une autre

Quatre familles ont été envisagées pour la deuxième génération. Le choix
tient à ce que l'architecture permet, pas à une mesure de performance, qui
n'existe pour aucune d'elles :

| Approche | Pourquoi pas maintenant |
|---|---|
| Arbitrage statistique, cointégration | Deux jambes par trade et plusieurs séries par décision : le contrat, le signal, le risque et le moteur live ne connaissent qu'une paire et une jambe. Les paires candidates sont surtout des croisées, que la conversion de devises ne permet pas encore de dimensionner. Et entre paires qui partagent une devise, ln EURGBP = ln EURUSD − ln GBPUSD (au spread près) : une partie de la « cointégration » est une identité comptable, pas une opportunité. |
| Frontière efficiente (Markowitz) | Produit des POIDS de portefeuille, pas des entrées et des sorties : sa place est dans la couche de risque (exposition croisée, reste-à-faire), pas dans une stratégie. La même identité rend la matrice de covariance des paires presque singulière, et l'optimiseur amplifie les erreurs d'estimation (Michaud, 1989, « The Markowitz Optimization Enigma », *Financial Analysts Journal*). |
| Microstructure, exécution | Aucune donnée pour cela (bougies d'une minute au mieux, pas de carnet d'ordres), et l'exécution appartient à la passerelle, pas à la stratégie. |
| **Tendance structurelle** | Une paire à la fois, une jambe, un stop : entre dans le contrat sans le modifier. Causal par construction. Une source de rendement documentée (Moskowitz, Ooi et Pedersen, 2012, « Time Series Momentum », *Journal of Financial Economics* ; Hurst, Ooi et Pedersen, 2017, « A Century of Evidence on Trend-Following Investing », *Journal of Portfolio Management*) — sur d'autres marchés, périodes et fréquences que les nôtres, ce qui ne prouve donc rien ici. |

Le filtre de Kalman écrit ici servira aussi à une future génération
d'arbitrage statistique (ratio de couverture dynamique), le jour où le
contrat saura porter deux jambes.

## Le modèle

Tendance locale linéaire (Harvey, 1989, *Forecasting, Structural Time
Series Models and the Kalman Filter*, Cambridge University Press, ch. 2
et 3), sur y_t = ln(close bid) :

```
y_t     = μ_t + ε_t           ε_t ~ N(0, σ²_ε)   bruit d'observation
μ_{t+1} = μ_t + β_t + η_t     η_t ~ N(0, σ²_η)   niveau
β_{t+1} = β_t + ζ_t           ζ_t ~ N(0, σ²_ζ)   pente : la tendance
```

Le filtre de Kalman (`kalman.go`) donne à chaque bougie la loi a
posteriori de (μ_t, β_t) sachant les observations jusqu'à t, et rien
d'après. Pour une matrice de transition T = [[1, 1], [0, 1]] et une
observation H = [1, 0] :

```
prédiction   x ← T x          P ← T P Tᵀ + diag(σ²_η, σ²_ζ)
innovation   v = y − μ̂        F = P₁₁ + σ²_ε
mise à jour  K = P Hᵀ / F     x ← x + K v      P ← P − K F Kᵀ
```

**Initialisation diffuse exacte.** Avec un a priori plat sur (μ₁, β₁),
les deux premières observations déterminent l'état :

```
μ̂₂ = y₂        β̂₂ = y₂ − y₁
Var μ₂ = σ²_ε   Cov(μ₂, β₂) = σ²_ε   Var β₂ = 2σ²_ε + σ²_η + σ²_ζ
```

C'est exact, là où l'artifice courant d'une variance initiale « très
grande » perd des chiffres significatifs par soustraction.

## La décision

```
z_t = β̂_t|t / √P_ββ,t|t          le « t de Student » de la pente

z_t ≥ +1,5      → entrée longue    stop = close − 3 × ATR(14)
z_t ≤ −1,5      → entrée courte    stop = close + 3 × ATR(14)
|z_t| < 0,5     → sortie           la tendance a disparu
sinon           → rien
```

- **Pas de limite** : un suivi de tendance laisse courir ses gains. Il
  sort quand la pente revient vers zéro, sur un signal opposé, ou au stop.
- **Hystérésis** : l'écart entre 1,5 et 0,5 évite d'entrer et de sortir en
  boucle autour d'un seul seuil.
- **Confiance publiée** : Φ(z) pour une entrée longue, Φ(−z) pour une
  courte — la probabilité a posteriori, **sous le modèle gaussien**, que
  la pente soit du côté retenu. Un modèle faux (queues épaisses,
  volatilité qui change) rend ce chiffre optimiste ; il sert à lire le
  signal, pas à dimensionner.
- **ATR(14) de Wilder**, même définition que `indicator.ATR`.

⚠ **1,5 ; 0,5 ; 3 × ATR ; fenêtre de 500 : ce sont des CONVENTIONS de
départ, pas des mesures.** Aucune n'a été confrontée à un historique
réel. Un démenti par la mesure donnera `troglodyte_v1_1`, jamais une
retouche de v1_0.

### Une fenêtre fixe, pour que le live décide comme le backtest

Chaque décision refiltre les **500 dernières bougies**, et seulement
elles, depuis l'initialisation diffuse exacte.

Pourquoi : quand σ²_ζ est petit, la pente filtrée a une mémoire très
longue. Filtrée depuis le début de la série, sa valeur à la bougie t
dépendrait de l'endroit où la série commence — or le backtest part du
début de son bloc, et le live d'un tampon glissant. Avec une fenêtre
fixe, la décision est une fonction de ces 500 bougies-là, identique
partout, et la stabilité par préfixe des décisions est garantie par
construction.

Coût mesuré (`bench_test.go`, Xeon 2,1 GHz du bac à sable de
développement) : **21 à 24 µs par décision** selon les passes (une
allocation de 4 Ko). Un an de H4 (≈ 1 560 bougies) se rejoue donc en
quelques dizaines de millisecondes ; un an de M1 (≈ 370 000 bougies) en
moins de dix secondes.

## L'entraînement : maximum de vraisemblance

Entraîner = estimer σ²_ε, σ²_η, σ²_ζ, **paire par paire** (`mle.go`).
Rien d'autre n'est appris : ni seuil, ni stop.

**Vraisemblance** par décomposition de l'erreur de prédiction, sur les
m = n − 2 innovations qui suivent l'initialisation :

```
ln L = −½ Σ_t [ ln(2π F_t) + v_t² / F_t ]
```

**Concentrée en σ²_η** (Harvey, 1989, § 3.4) : en filtrant avec σ²_η = 1
et les rapports qε = σ²_ε/σ²_η, qζ = σ²_ζ/σ²_η, on obtient F*_t, et

```
σ̂²_η  = (1/m) Σ_t v_t² / F*_t
ln L_c = −(m/2) (ln 2π + 1 + ln σ̂²_η) − ½ Σ_t ln F*_t
```

Il reste deux dimensions, (ln qε, ln qζ) : une grille fixe de 13 × 11
points, puis un simplexe de Nelder-Mead (Nelder et Mead, 1965, *The
Computer Journal* 7(4)) depuis le meilleur point. **Aucun tirage** :
deux entraînements sur les mêmes données donnent le même modèle au bit
près. Coût mesuré : **40 ms** pour 10 000 bougies (même machine).

**Pourquoi l'échelle est σ²_η.** Un taux de change ressemble à une marche
aléatoire, dont le bruit d'observation est presque nul. La première
version (jamais publiée) rapportait tout à σ²_ε : sur une série sans
bruit, les rapports partaient vers l'infini et butaient sur les bornes,
l'estimation de σ²_ζ ne tenait plus qu'à une compensation de
l'optimiseur, et le drapeau « borne atteinte » ne voulait plus rien
dire. C'est le premier essai du binaire, pas un test unitaire, qui l'a
montré.

**Variances nulles.** Une variance indiscernable de zéro est une solution
au bord, légitime : le manifeste le dit (`negligible_eps`,
`negligible_zeta`) au terme d'un test du rapport de vraisemblance au
seuil de 5 % — 2 (ln L* − ln L₀) < 2,71, valeur critique d'un paramètre
au bord de son domaine (mélange ½ χ²₀ + ½ χ²₁ ; Self et Liang, 1987,
*Journal of the American Statistical Association* 82(398)). L'autre
rapport reste à son optimum pendant le test, ce qui le rend prudent : il
déclare « nul » moins souvent qu'un test réoptimisé. `at_upper_bound`,
lui, signale un modèle inadapté à la série (le niveau ne bouge presque
pas devant le bruit ou la pente).

**Refus.** Moins de 1 000 bougies, un prix nul ou négatif, une série
constante, plusieurs paires dans un même entraînement : l'entraînement
est refusé avec son motif.

## Le modèle archivé

Tout le modèle tient dans `metadata.json`, le manifeste du catalogue :
les trois variances, les mesures de l'estimation (log-vraisemblance,
nombre d'observations et d'itérations, drapeaux de bord), et la
**définition** de la révision (fenêtre, seuils, stop, période d'ATR). Au
chargement, le modèle est **refusé** s'il vient d'une autre révision,
d'une autre paire, d'une autre unité de temps (les variances dépendent de
la durée d'une bougie) ou d'une autre définition.

`gw train` écrit aussi, par pli, les métriques `log_vraisemblance_par_obs`,
`sigma2_eps`, `sigma2_eta`, `sigma2_zeta`, `iterations`,
`sigma2_eps_negligeable`, `sigma2_zeta_negligeable` et
`borne_haute_atteinte` (moyennes sur les paires du pli).

## Les règles d'exécution qu'il déclare

| Déclaration | Valeur | Effet, en backtest comme en live |
|---|---|---|
| `ContextBars` | 500 | la fenêtre ; aucune décision avant |
| `MaxHold` | 0 | aucune sortie forcée par le temps |
| `HoldsOverWeekend` | oui | pas de clôture le vendredi ; un stop franchi par le gap du lundi est servi à l'ouverture |
| `ExitOnReversal` | oui | un signal opposé à la position la ferme (motif `reversal`), sans rouvrir sur la même bougie |

## Tests

| Test | Ce qu'il garantit |
|---|---|
| `TestFilterMatchesDenseGaussianConditioning` | Le filtre donne la même loi a posteriori de la pente qu'un conditionnement gaussien DENSE, sans récursion (écart < 10⁻⁵) |
| `TestLikelihoodMatchesDenseGaussianDensity` | La vraisemblance diffuse concentrée égale ln p(y₁..ₙ) − ln p(y₁,y₂) calculée par Cholesky |
| `TestWindowATRMatchesTheIndicator` | L'ATR de fenêtre égale `indicator.ATR` |
| `TestEstimateFindsAtLeastTheTrueLikelihood` | Sur 4 000 bougies simulées, l'optimum n'est jamais sous la vraisemblance au vrai point ; variances retrouvées à 1,066 / 1,058 / 1,132 fois leur vraie valeur |
| `TestEstimateRecognisesAPriceWithoutObservationNoise` | Sans bruit d'observation, σ²_ε est déclaré négligeable ; σ²_η retrouvé à 0,979, σ²_ζ à 0,589 fois sa vraie valeur (la moins identifiable des trois) |
| `TestEstimateIsDeterministic` | Deux estimations identiques au bit près |
| `TestFollowsTheTrendInBothDirections` | Sur une dérive nette, entrées dans le sens de la dérive, aucune à contresens |
| `TestModelIsRefusedOutsideItsPairTimeframeAndStrategy` | Les quatre refus au chargement |
| banc `strategytest` | Contrat commun, dont la stabilité par préfixe des décisions |

Le premier test a été vérifié en retour : une erreur introduite
volontairement dans la prédiction de covariance le fait échouer.

## Ce qui a été vérifié, et ce qui ne l'a pas été

**Vérifié** : le chemin complet sur le vrai binaire (`gw train`,
`gw backtest`) avec un historique M1 synthétique de trois ans ; aucune
clôture de fin de semaine, sorties sur signal, AUC affichée « — ».
Les chiffres de P&L de cet essai ne mesurent rien : la série contenait
des tendances fabriquées exprès.

**Jamais mesuré** : Troglodyte sur un historique réel. Tenté pendant le
développement de v0.7.0 : Dukascopy a limité le débit au point de
rendre le téléchargement impraticable depuis le bac à sable
(4 jours sur 260 en un quart d'heure). À faire avant toute conclusion :
`gw train` sur votre historique, en comparant avec `colibri_v1_2`
(`gw runs`).

## Limites connues de v1_0

- **Variances constantes** sur tout l'historique d'entraînement : la
  volatilité du change varie ; z en hérite une échelle trop petite dans
  les périodes agitées, trop grande dans les calmes.
- **Stop fixe à l'entrée** : pas de stop suiveur (le contrat ne sait pas
  déplacer un stop). Les gains ne sont protégés que par la sortie sur z.
- **Pas de limite** : le risque par trade est borné par le stop, pas le
  gain — voulu, mais le walk-forward liquide en fin de bloc (motif
  `final`) une position que le live aurait gardée.
- **Pas d'AUC** : Troglodyte n'est pas un classifieur. On le juge au P&L,
  au profit factor et au drawdown out-of-sample.
