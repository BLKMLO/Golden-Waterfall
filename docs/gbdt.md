# Le GBDT maison

`internal/ml/gbdt` est un gradient boosting sur arbres de décision écrit
en **Go pur**, pour la classification binaire (perte logistique).

## Pourquoi l'écrire plutôt que l'emprunter

Les implémentations de référence du boosting sur arbres sont en C++. Les
lier depuis Go imposerait `cgo`, une chaîne de compilation C++ et une
bibliothèque native à côté de l'exécutable. Compilation croisée
Windows/macOS/Linux depuis n'importe quelle machine, un seul fichier à
copier : la promesse « un seul binaire » vaut la réimplémentation.

Second bénéfice, moins attendu : l'entraînement est **reproductible au
bit près** à graine égale, et un test le vérifie.

## L'algorithme

C'est l'algorithme classique du boosting par histogrammes, sans les
extensions dont le projet n'a pas besoin.

**1. Binning par quantiles.** Chaque feature est discrétisée en au plus
254 bins d'effectifs équilibrés. Des bins de largeur égale mettraient
99 % des points dans un seul bin sur des retours financiers très
leptokurtiques — l'histogramme deviendrait aveugle. Un bin dédié,
distinct, reçoit les valeurs **manquantes** : elles ne sont jamais
confondues avec un extrême.

**2. Croissance FEUILLE PAR FEUILLE.** À chaque étape on coupe la feuille
dont le gain est le plus élevé — et non la plus profonde. À nombre de
feuilles égal, l'arbre est nettement plus expressif.

**3. Histogrammes et soustraction.** Pour chaque nœud, on accumule
`(gradient, hessienne, effectif)` par bin. L'histogramme du frère le plus
gros n'est jamais recalculé : il se **déduit** de celui du parent moins
celui du petit frère. Les features sont réparties entre goroutines, chacune
écrivant dans sa propre tranche — aucun verrou.

**4. Choix de la coupure.**

```
gain = G_g²/(H_g+λ) + G_d²/(H_d+λ) − G_p²/(H_p+λ)
```

Contraintes : `min_data_in_leaf` et `min_sum_hessian_in_leaf` de chaque
côté. La seconde complète la première : deux cents échantillons dont le
modèle est déjà certain ne portent presque aucune information.

**5. Valeurs manquantes.** Pour chaque seuil candidat, les deux
directions du bin manquant sont évaluées et la meilleure est retenue. La
direction par défaut est donc **apprise**, jamais conventionnelle — c'est
ce qui permet de traiter les NaN de chauffe sans les imputer.

**6. Features catégorielles.** Les catégories sont triées par gradient
moyen lissé (`cat_smooth`), puis on balaie les préfixes. Chercher la
meilleure partition arbitraire serait exponentiel ; ce tri donne l'optimum
pour une perte convexe. Le lissage empêche une catégorie à trois exemples
de prendre la tête. Les catégories trop rares (`min_data_per_group`) sont
écartées.

**7. Valeur d'une feuille** : `−G / (H + λ) × learning_rate`.

**8. Arrêt anticipé** sur la perte logistique de validation. Le modèle
conserve tous ses arbres mais n'évalue que jusqu'à `BestIteration` — les
suivants restent pour le diagnostic.

Dans ce projet, la validation est toujours la **queue du bloc
d'entraînement**, donc strictement dans le passé du bloc out-of-sample :
l'arrêt anticipé ne voit jamais l'avenir.

## Ce qui est volontairement absent

GOSS, EFB, l'apprentissage distribué, les objectifs multiclasses et la
régression. Le projet n'en a pas besoin ; les implémenter à moitié serait
pire que ne pas les avoir.

## Hyperparamètres par défaut

Volontairement **prudents** : les historiques d'une paire sont courts au
regard du bruit, et un modèle qui mémorise est pire qu'inutile — il donne
confiance.

| Paramètre | Valeur | Rôle |
|---|---|---|
| `num_rounds` | 300 | arbres maximum |
| `learning_rate` | 0,03 | contribution par arbre |
| `num_leaves` | 31 | complexité d'un arbre |
| `min_data_in_leaf` | 50 | effectif minimal d'une feuille |
| `lambda_l2` | 1,0 | régularisation |
| `feature_fraction` | 0,8 | features tirées par arbre |
| `bagging_fraction` | 0,8 | lignes tirées par arbre |
| `early_stopping_rounds` | 30 | patience |
| `max_bins` | 254 | + 1 réservé aux manquants |
| `seed` | 42 | reproductibilité |

## Format du modèle

JSON indenté : lisible, inspectable, comparable d'une version à l'autre.
Trois cents arbres de trente et une feuilles pèsent moins de deux
mégaoctets. Le fichier porte un numéro de format, et un modèle produit par
une version incompatible est **refusé** plutôt que relu de travers.

Les seuils y sont stockés en **valeurs réelles** (pas en indices de bin) :
la prédiction n'a donc besoin d'aucune table de discrétisation.

## Métriques

- **AUC** par la statistique de Mann-Whitney sur les RANGS, avec rangs
  moyens pour les ex æquo. Elle ne dépend d'aucun seuil : elle mesure le
  pouvoir de CLASSEMENT, pas le hasard d'un point de coupure bien choisi.
  Avec une seule classe présente, elle renvoie `NaN` — car elle n'est pas
  définie, et renvoyer 0,5 laisserait croire à une mesure.
- **Perte logistique** calculée sur l'échelle des log-odds via un softplus
  stable, pour ne pas déborder sur des scores extrêmes.
