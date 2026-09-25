# Le filtre d'actualités

Une annonce économique importante (emploi américain, décision de taux,
inflation…) fait bouger une paire de façon brutale, dans un sens que les
prix d'avant ne disent pas. Le filtre d'actualités empêche d'**entrer**
dans la fenêtre d'une telle annonce sur l'une des deux devises de la
paire. Les sorties, elles, ne sont jamais filtrées.

## Qui y a droit

Le filtre ne s'applique qu'aux stratégies qui le **déclarent**
(`strategy.Description.UsesNews`) :

| Stratégie | Filtre |
|---|---|
| `colibri_v1_0`, `v1_1`, `v1_2` | **jamais** — règle du propriétaire du projet : Colibri n'a pas droit à internet. Un test (`TestColibriNeverUsesTheNews`) échoue si une révision Colibri le déclare. |
| `troglodyte_v1_0` | non (publiée sans lui ; la modifier changerait ce qu'elle veut dire) |
| `troglodyte_v1_1` | oui, si `news.enabled` |

Une stratégie ne va **jamais** elle-même sur internet. Le calendrier est
récupéré par le programme, archivé sur le disque, et appliqué par les
**moteurs** (backtest et live) avec la même fonction, `news.Gate.Check`.

## La règle

Pour une entrée sur la paire `BBBQQQ` décidée à l'instant `t` (le close de
la bougie de décision, en backtest comme en live) :

```
bloquée  si  une annonce d'impact ≥ news.min_impact
             sur la devise BBB ou QQQ
             tombe dans [t − after_minutes, t + before_minutes]
```

Par défaut : impact **fort** (« high »), **30 minutes** avant et après.
Ces valeurs sont des **conventions**, pas des mesures. Les jours fériés
ne bloquent jamais. Pour un métal ou un indice, seule la devise de
cotation est regardée (USD pour XAUUSD).

## D'où vient le calendrier

Les sources sont des **modules remplaçables**, enregistrés comme les
passerelles (`news.Register` dans un `init()`), choisis par `news.source` :

| Source | Ce qu'elle fait |
|---|---|
| `forexfactory` (défaut) | Flux public `nfs.faireconomy.media/ff_calendar_thisweek.json` (données du calendrier ForexFactory). Constaté le 25 septembre 2026 : il ne sert que la **semaine en cours** ; les semaines précédente et suivante répondent 404. |
| `none` | Aucune récupération : seule l'archive compte, alimentée par `gw news import`. |

Ajouter une source : un fichier dans `internal/news/` qui implémente
`Source` (`Name`, `Fetch` → annonces + semaines couvertes) et s'enregistre
dans un `init()`. Rien d'autre à toucher : l'écran Paramètres la propose
d'office.

## L'archive, et pourquoi elle est indispensable

Puisque le flux ne donne que la semaine en cours, tout ce qui est récupéré
est **archivé**, une semaine par fichier, dans `<données>/news/`
(`gw paths`). Chaque fichier déclare la semaine qu'il **couvre** (du
dimanche 0 h au dimanche suivant, heure de New York). Une nouvelle
récupération de la même semaine remplace le fichier : une annonce déplacée
en cours de semaine est prise en compte.

Récupérations :

- pendant une séance live, toutes les `news.refresh_minutes` (60 par
  défaut), seulement si la stratégie déclare le filtre ;
- à la demande : `gw news fetch`.

Pour un historique : `gw news import FICHIER.json` accepte le format du
flux (une liste de `{"title", "country", "date", "impact"}`, `date` en
RFC 3339 avec décalage, `country` = code de devise). Un import ne déclare
couvertes **que les semaines où il contient au moins une annonce** : un
fichier qui s'arrête le 10 mars ne dit rien du 20.

## Honnêteté : « pas d'annonce » n'est pas « on ne sait pas »

Une entrée décidée hors de toute semaine archivée **n'est pas filtrée** —
on ne sait pas s'il y avait une annonce — et elle est **comptée à part** :

| Où | Ce qui est affiché |
|---|---|
| Backtest, Entraînement (TUI), `gw backtest`, `gw train` | « filtre d'actualités : N écartée(s) · M décidée(s) hors du calendrier archivé, NON filtrée(s) », en avertissement dès que M > 0 |
| `run.json`, export CSV | `news_blocked`, `news_uncovered` ; cellules VIDES quand le filtre était inactif |
| Live, ligne de contrôle | `✓ news` / `✗ news` : le calendrier couvre-t-il l'instant du marché ? Informatif : il ne bloque pas la paire |
| Live, journal | chaque entrée écartée avec l'annonce en cause ; chaque entrée transmise sans calendrier |

Conséquence à connaître : sur un historique antérieur à l'archive, le
filtre ne filtre **rien**, et le walk-forward le dit. Pour qu'il compte
dans une mesure, il faut importer un calendrier historique.

Si une récupération échoue (réseau, 429), rien n'est déclaré couvert, le
journal et `gw news` le disent, et l'archive existante reste utilisée.

## Réglages (`news` dans config.yaml, écran 6 Paramètres)

| Clé | Défaut | Sens |
|---|---|---|
| `enabled` | `true` | Filtre actif pour les stratégies qui le déclarent. `GW_NEWS` l'écrase. |
| `source` | `forexfactory` | Clé du registre des sources. |
| `min_impact` | `high` | `low`, `medium` ou `high`. |
| `before_minutes` | 30 | Pas d'entrée si une annonce tombe dans les N minutes qui suivent. |
| `after_minutes` | 30 | Pas d'entrée si une annonce est survenue dans les N minutes qui précèdent. |
| `refresh_minutes` | 60 | Cadence de récupération en séance (≥ 5). |

## Tests

Aucun test n'appelle le réseau : la récupération est éprouvée contre un
faux serveur HTTP qui sert un extrait réel du flux
(`internal/news/testdata/ff_week_2026-09-20.json`), et un autre qui répond
429. Les moteurs sont éprouvés des deux côtés : une stratégie qui déclare
le filtre voit son entrée écartée, la même sans déclaration (Colibri) voit
la sienne partir.

## Limites

- Le filtre ne protège que l'**entrée**. Une position ouverte avant une
  annonce la traverse (Troglodyte garde ses positions plusieurs jours).
- Il n'a jamais été mesuré : aucun historique de calendrier n'est archivé
  à ce jour au-delà de la semaine du 20 septembre 2026.
- Les heures d'annonce sont celles publiées par la source ; une annonce
  avancée ou retardée sans mise à jour du flux n'est pas vue.
