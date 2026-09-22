# Données de marché

## Source : Dukascopy

Historique M1 gratuit, profond (2010 et au-delà selon l'instrument), en
**bid ET ask**. Le côté ask n'est pas un luxe : c'est lui qui permet de
**mesurer** le spread au lieu de l'inventer. Sans lui, un aller-retour de
backtest serait gratuit — ce qui n'existe pas.

Trois pièges du format, tous traités :

- **le mois est 0-based dans les URLs** : janvier = `00` ;
- **404 = marché fermé**, pas une panne. Les week-ends ne sont même pas
  demandés (ce serait 104 requêtes inutiles par an et par symbole) ;
- **429 = limite de débit.** La concurrence par défaut est basse (3) et le
  backoff des 429 est LONG (`Retry-After` honoré, sinon 5 s × tentative).
  Le backoff court 1-2-4-8 s des autres erreurs est inadapté à une limite
  de débit : il ne fait que l'aggraver.

Le format d'un fichier `.bi5` est du LZMA contenant des enregistrements de
24 octets big-endian. **L'ordre des champs est `offset, open, close, low,
high, volume`** — *pas* OHLC. C'est l'erreur classique sur ce format ; un
test la verrouille.

L'ask est un **bonus** : son absence ne fait pas perdre le bid. La bougie
existe, seul le spread devient non mesurable — et c'est signalé.

## Stockage : Parquet

```
history/<SYMBOLE>/<SYMBOLE>_m1_<année>.parquet
```

Un format **colonne standard**, que pandas, polars, DuckDB, Spark ou R
ouvrent sans rien savoir de Golden Waterfall :

```python
import pandas as pd
df = pd.read_parquet("EURUSD_m1_2023.parquet")
```

### Schéma

| Colonne | Type | Note |
|---|---|---|
| `time` | `TIMESTAMP(MILLIS, UTC)` | début de la bougie |
| `bid_open` / `bid_high` / `bid_low` / `bid_close` | `DOUBLE` | obligatoires |
| `ask_open` / `ask_high` / `ask_low` / `ask_close` | `DOUBLE`, **nullable** | `NULL` = côté ask non mesuré |
| `volume` | `DOUBLE` | volume de TICKS en forex |

Le côté ask absent s'écrit **NULL**, pas zéro. Zéro serait un prix, et
l'outil tiers qui ouvre le fichier l'ajouterait à ses moyennes : c'est la
règle « — plutôt que zéro » de l'interface, appliquée au fichier.

Les métadonnées du fichier portent ce que le schéma ne dit pas :
`gw.symbol`, `gw.year`, `gw.timeframe`, `gw.source`, `gw.writer` et
surtout **`gw.failures`**, le nombre de jours que le téléchargement n'a
pas pu récupérer. Sans lui, une année trouée serait indiscernable d'une
année complète et la relance la sauterait.

### Pourquoi avoir quitté le format maison `.gwb`

Le stockage était un format binaire à enregistrements de 40 octets, lu par
seek. Il était compact et rapide — et lisible **par ce seul programme**.
Impossible d'y verser un historique téléchargé ailleurs, impossible de
l'ouvrir dans un tableur ou un notebook. Un format qui enferme son
utilisateur dans le logiciel qui l'a écrit est exactement l'inverse de ce
que ce projet promet.

Ce que la bascule coûte et rapporte, mesuré sur une année de M1
(372 000 bougies, `internal/data/bench_test.go`) :

| | `.gwb` | Parquet |
|---|---|---|
| Taille | 14,9 Mo | **9,2 Mo** |
| Lecture d'une année | 26 ms | 139 ms |
| Lecture d'un mois | seek | 50 ms |
| Écriture | 68 ms | 485 ms |

La lecture est cinq fois plus lente en valeur absolue, mais elle se
produit une fois par entraînement, derrière un calcul qui dure des
minutes. L'écriture se produit derrière un téléchargement réseau qui dure
des heures. La taille, elle, reste sur le disque pour toujours.

Le prix payé ailleurs : la bibliothèque Parquet fait passer le binaire de
8,8 à 15,2 Mo. Il reste unique, statique et sans `cgo`.

### Lire une tranche de dates

Les fichiers sont écrits par **groupes de lignes de 32 768 bougies**, soit
environ un mois. Les groupes dont la plage de dates ne croise pas la
période demandée ne sont jamais décompressés : c'est ce qui remplace le
seek de l'ancien format.

### Les `.gwb` déjà téléchargés

Ils **restent lus**. Rien n'en écrit plus — un format qu'on ne peut plus
produire ne peut plus se répandre — et l'écran **Données** signale les
années restées dans l'ancien format.

```bash
gw migrate            # convertit, garde les originaux
gw migrate --remove   # supprime chaque original APRÈS relecture du Parquet
```

La suppression n'intervient qu'après relecture **bougie à bougie** du
fichier écrit. Une conversion non vérifiée qui efface sa source est la
seule façon de perdre pour de bon un historique qui a coûté des heures.

### Importer un historique venu d'ailleurs

```bash
gw import --symbol EURUSD chemin/vers/eurusd.parquet
```

Les colonnes sont appariées **par leur nom**, avec les alias usuels
(`time`/`timestamp`/`datetime`, `open`/`bid_open`, …), et l'unité de
l'horodatage est lue dans le type logique du fichier — secondes,
millisecondes, microsecondes ou nanosecondes. Un fichier sans colonne de
prix reconnaissable est **refusé** : mieux vaut un refus qu'une colonne
appariée au hasard, qui produirait des prix crédibles et faux.

Ce qui est importé est marqué **importé**. Personne n'a compté ses jours
manquants : `Complete()` répond donc non, et l'écran Données le distingue
d'une année téléchargée plutôt que de la déclarer faite sur la foi de
rien.

### Robustesse commune

L'écriture passe par un fichier temporaire renommé à la fin : une
interruption laisse l'ancien fichier intact plutôt qu'un fichier tronqué
que la relecture prendrait pour des données.

La relecture est blindée : fichiers au nom non conforme **ignorés** (une
copie manuelle ne doit pas doubler les bougies), doublons d'horodatage
dédupliqués, ordre rétabli, période inversée refusée d'emblée.

## Unités de temps

`M1 M5 M15 M30 H1 H4 D1 W1 MN1`. Le ré-échantillonnage agrège
`first/max/min/last` et somme le volume, **bid et ask séparément** (ce qui
préserve la mesure du spread après conversion).

Les buckets sans aucune bougie M1 — week-ends, fériés — ne sont **pas
créés** : un marché fermé n'a pas de bougie, et en fabriquer une plate
inventerait des données.

Alignement : les unités infra-journalières sur l'époque Unix (H4 tombe donc
à 0, 4, 8, 12, 16, 20 h UTC), D1 sur minuit UTC, W1 sur le **lundi** (ISO),
MN1 sur le 1er du mois.

## Où vivent les données

Jamais à côté du binaire : celui-ci est unique et déplaçable.

| | Configuration | Données |
|---|---|---|
| Linux/BSD | `~/.config/golden-waterfall` | `~/.local/share/golden-waterfall` |
| macOS | `~/Library/Application Support/GoldenWaterfall` | idem |
| Windows | `%AppData%\GoldenWaterfall` | `%LocalAppData%\GoldenWaterfall` |

`GW_CONFIG_DIR` et `GW_DATA_DIR` forcent ces emplacements — pratique pour
une installation portable (clé USB) ou un conteneur. `gw paths` affiche ce
qui est réellement utilisé.

Le dossier de données contient `history/`, `models/`, `exports/`, `gw.db`
(journal des trades, interrupteurs, repères d'équité) et `logs/`.

## Exports CSV

Trois endroits produisent des CSV dans `exports/` :

| Où | Touche | Ce qui sort |
|---|---|---|
| Écran **5 Journal**, onglet trades | `e` | Le journal des trades, **filtre compris** |
| Écran **3 Backtest** | `e` | Trades, courbe de valeur, métriques (trois fichiers) |
| `gw backtest PAIRE --csv` | — | Les mêmes trois fichiers |

**Convention de fichier, assumée** : séparateur `;`, décimale `,`, UTF-8
précédé d'une marque d'ordre des octets. C'est ce qu'un tableur francophone
ouvre d'un double-clic, sans boîte de dialogue d'import et sans transformer
`1.25` en date. Pour pandas : `read_csv(path, sep=";", decimal=",")`.

Deux règles y survivent telles quelles :

- une métrique **non mesurée** donne une cellule **vide**, jamais un zéro —
  un zéro serait additionné par le tableur ;
- les drapeaux d'honnêteté (`couts_modelises`, `devise_exacte`) voyagent
  **avec** les chiffres : sortis de l'écran qui les affiche, ils sont la
  seule chose qui dise si ces chiffres peuvent être additionnés.

L'export **ne recalcule rien**. Il recopie ce que le programme a déjà
mesuré, champ pour champ : un fichier qui refait un cumul pourrait afficher
un chiffre différent de l'écran qui l'a produit.

## Ordres de grandeur

Une année de M1 sur une paire forex ≈ 372 000 bougies ≈ **9 Mo** en
Parquet (15 Mo dans l'ancien `.gwb`). Trente et un instruments sur quinze
ans : compter **4 à 5 Go** et plusieurs heures de téléchargement à
concurrence 3.

## Ajouter un instrument

Une ligne dans `data.Instruments` : identifiant Dukascopy, nombre de
décimales, classe, devise de base, devise de cotation. Les deux devises ne
sont pas décoratives — elles décident de la conversion du notionnel et du
P&L (cf. [architecture.md](architecture.md#devises)).
