# Données de marché

## Sources de l'historique

Le téléchargement passe par une **source interchangeable**
(`internal/data/source.go`). Le téléchargeur ne connaît que le contrat
`data.Source` : il demande une année, écrit le fichier avec le compte des
manques et le nom de la source, tient le journal. Tout ce qui est propre à
un fournisseur (URL, format, découpage, symboles publiés, limite de débit)
vit dans son fichier.

| | `dukascopy` (défaut) | `fxcm` |
|---|---|---|
| Première année | 2003 | 2012 |
| Instruments | tout le catalogue (forex, métaux, indices) | 25 paires forex : ni métaux, ni indices, ni EURCAD, GBPAUD, CHFJPY |
| Prix | M1 bid ET ask | M1 bid ET ask |
| Volume | volume de ticks | **aucun** (écrit NaN, jamais 0) |
| Découpage, unité des manques | jours | semaines |
| Limite de débit | 429 au-delà de 3-4 requêtes simultanées | aucune constatée |
| Trous connus | — | des semaines entières (2026 : semaines 18 à 31) |

Choisir : `history.source` dans `config.yaml` ou l'écran **Paramètres**,
`GW_HISTORY_SOURCE`, ou pour un seul téléchargement
`gw download --source fxcm`. Une source inconnue refuse le démarrage.
`gw config` dit la source et les paires qu'elle ne publie pas ; l'écran
**Données** les marque « non publié », et sa colonne **Source** signale
une paire dont l'historique mêle deux fournisseurs (volumes et spreads
qui ne se comparent pas d'une année à l'autre).

Changer de source ne réécrit **pas** les années déjà complètes : seules
les années absentes ou incomplètes sont demandées.

⚠ **Colibri exige le volume.** Ses trois features de volume sont
obligatoires dans toutes les révisions publiées. Sur un historique FXCM,
l'entraînement le refuse en le disant (« l'historique de EURUSD n'a AUCUN
volume mesuré ») et l'écran Live affiche « ✗ vol. ». Troglodyte n'utilise
pas le volume et fonctionne sur les deux sources.

### Brancher une autre source

Un fichier `internal/data/<nom>.go` qui implémente `data.Source`
(`Info`, `Serves`, `FetchYear`) et s'enregistre dans un `init()` par
`data.RegisterSource("<nom>", …)`. Rien d'autre à toucher : la
configuration, l'écran Paramètres, la CLI et l'écran Données lisent le
registre. Obligations : horodatage **UTC**, **aucune bougie le samedi ni
le dimanche** (`data.IsWeekend`, voir plus bas), volume **NaN** s'il
n'est pas publié, et `Fetched.Missing` qui compte les unités de marché
restées vides — c'est lui qui rend une année incomplète. Le faux serveur
HTTP de `source_test.go` montre comment l'éprouver sans réseau.

### Pas de bougie le week-end, quelle que soit la source

`core.LastBarsOfWeek` (clôture de fin de semaine du backtest, étiquetage
de colibri_v1_2) découpe par **semaine ISO**, du lundi au dimanche. Une
bougie du dimanche soir — la réouverture du forex — serait la dernière de
SA semaine, et la clôture de fin de semaine aurait lieu APRÈS le week-end
qu'elle doit éviter. Dukascopy ne demande pas ces jours-là ; FXCM les
publie et la source les **écarte** ; l'import les écarte et le **dit**.

## Source : FXCM

Fichiers publics `https://candledata.fxcorporate.com/m1/<PAIRE>/<année>/<semaine>.csv.gz`,
un par semaine. Constaté le 27 septembre 2026, et rien de plus :

- colonnes `DateTime,BidOpen,BidHigh,BidLow,BidClose,AskOpen,AskHigh,AskLow,AskClose`,
  date `MM/JJ/AAAA hh:mm:ss.000`, **aucun volume** ;
- horodatage **UTC** : les semaines ouvrent le dimanche à 22 h en hiver et
  21 h en été, ferment le vendredi à 21 h 59 / 20 h 59 (17 h à New York) ;
- première année servie : 2012 (2011 : 404) ;
- la **numérotation des semaines n'est pas stable** d'une année à l'autre
  (la semaine du 29/12/2019 est « 2019/53 », celle du 04/01/2026 est
  « 2026/1 »). La source demande donc toutes les semaines candidates
  (52 et 53 de l'année précédente, 1 à 53 de l'année) et garde les
  bougies de l'année ;
- des semaines entières manquent. Une semaine de marché (au moins deux
  jours ouvrés dans l'année, hors 1er janvier et 25 décembre) restée vide
  est **comptée** dans `gw.failures` : l'année reste incomplète et sera
  redemandée, sans que rien ne soit comblé.

Mesuré dans le bac à sable le 27 septembre 2026 : EURUSD, GBPUSD et
USDJPY de 2012 à 2026, 15 530 792 bougies M1 ; une année demande
55 fichiers et environ 3 s ; 2012 à 2022 complètes ; manquent 1 semaine
en 2023, 4 en 2024, 4 en 2025 et 15 en 2026 (au 27 septembre).

## Source : Dukascopy

Historique M1 gratuit, profond (2010 et au-delà selon l'instrument), en
**bid ET ask**. Le côté ask n'est pas un luxe : c'est lui qui permet de
**mesurer** le spread au lieu de l'inventer. Sans lui, un aller-retour de
backtest serait gratuit — ce qui n'existe pas.

Trois pièges du format, tous traités :

- **le mois est 0-based dans les URLs** : janvier = `00` ;
- **404 = marché fermé**, pas une panne. Les week-ends ne sont même pas
  demandés (ce serait 104 requêtes inutiles par an et par symbole).
  Conséquence : les heures du **dimanche soir**, où le forex rouvre, ne
  sont pas dans l'historique — le fichier existe pourtant (constaté le
  25 septembre 2026 : réponse 200 de 2 724 octets pour le dimanche
  7 janvier 2024, contenu non décodé faute de débit). La clôture de fin de
  semaine du backtest (`core.LastBarsOfWeek`, semaine ISO) SUPPOSE cette
  absence : avec des bougies du dimanche, la dernière bougie de la semaine
  ISO serait celle du dimanche soir, après le week-end ;
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
| `volume` | `DOUBLE` | volume de TICKS en forex (Dukascopy) ; NaN = non publié |

Le côté ask absent s'écrit **NULL**, pas zéro. Zéro serait un prix, et
l'outil tiers qui ouvre le fichier l'ajouterait à ses moyennes : c'est la
règle « — plutôt que zéro » de l'interface, appliquée au fichier.

Les métadonnées du fichier portent ce que le schéma ne dit pas :
`gw.symbol`, `gw.year`, `gw.timeframe`, `gw.source` (`dukascopy`, `fxcm`,
`import`), `gw.writer` et surtout **`gw.failures`**, le nombre d'unités
(jours pour Dukascopy, semaines pour FXCM) que le téléchargement n'a pas
pu récupérer. Sans lui, une année trouée serait indiscernable d'une année
complète et la relance la sauterait.

Le `volume` vaut **NaN** quand la source n'en publie pas (FXCM, import
sans colonne volume) : zéro dirait « aucun échange ».

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

Un `.gwb` oublié à côté d'un Parquet **différent** de la même année
(année retéléchargée ou importée depuis) n'est **pas** converti : c'est
le Parquet que l'historique lit, et le remplacer effacerait des données
plus récentes. `gw migrate` le signale comme « sauté » et laisse le
`.gwb` intact.

### Importer un historique venu d'ailleurs

```bash
gw import --symbol EURUSD chemin/vers/eurusd.parquet
gw import --symbol EURUSD export.csv                      # entête nommée
gw import --symbol EURUSD --tz Europe/Athens \
   --columns date,time,open,high,low,close,volume EURUSD1.csv   # MetaTrader
gw import --symbol EURUSD --tz Etc/GMT+5 \
   --columns time,open,high,low,close,volume DAT_ASCII_EURUSD_M1_2024.csv  # HistData
```

**Parquet, CSV, TSV**, éventuellement compressés en gzip (`.csv.gz`).
Les colonnes sont appariées **par leur nom**, avec les alias usuels
(`time`/`timestamp`/`datetime`/`date`, `open`/`bid_open`/`BidOpen`,
`<TICKVOL>`, …) ; `open/high/low/close` sans préfixe sont lus comme le
côté **bid**, et l'ask n'est lu que s'il est nommé. Un fichier sans
colonne de prix reconnaissable est **refusé** : mieux vaut un refus
qu'une colonne appariée au hasard, qui produirait des prix crédibles et
faux. Un CSV **sans entête** exige `--columns` : rien n'est apparié par
position sans que l'utilisateur l'ait dit.

Pour un CSV, en plus :

- **séparateur** détecté (`,` `;` tabulation `|`) ; avec `;`, la virgule
  décimale est acceptée (tableur francophone) ;
- **date** : époque Unix (secondes à nanosecondes) ou format détecté sur
  les 50 000 premières lignes. Un format **ambigu** (`01/02/2024` : 1er
  février ou 2 janvier ?) est **refusé** — `--time-format` le tranche
  (format Go, ex. `"01/02/2006 15:04:05.000"`) ;
- **fuseau** : UTC par défaut, `--tz` sinon (nom IANA). Un export
  MetaTrader est souvent à l'heure du courtier ; HistData est à l'heure
  de New York sans changement d'heure (`Etc/GMT+5`) ;
- une ligne illisible (prix non numérique ou non positif, champs
  manquants) ou impossible (voir ci-dessous) **refuse le fichier**, avec
  son numéro.

Pour tout import, CSV **et Parquet** (v0.8.3 ; un Parquet tiers n'était
jusque-là contrôlé en rien) :

- chaque bougie doit être **possible** : prix finis et positifs, plus haut
  au-dessus du plus bas, ouverture et clôture DANS la bougie, côté bid
  comme côté ask s'il est présent, volume absent ou positif. Des colonnes
  décalées donnent des prix plausibles et une bougie impossible : le
  fichier est refusé, en nommant la ligne (CSV) ou l'horodatage
  (Parquet). Vérifié sur 4 176 619 bougies FXCM réelles : aucune refusée ;

- l'historique doit être du **M1** : un écart médian entre bougies
  différent d'une minute est refusé (un fichier H1 ferait des « bougies
  M1 » d'une heure) ;
- les bougies du **samedi et du dimanche** (UTC) sont écartées et
  comptées (voir « Pas de bougie le week-end ») ; si le fichier n'est pas
  en UTC, c'est souvent le signe qu'il faut `--tz` ;
- une année **déjà présente** n'est pas écrasée : rien n'est écrit et la
  commande le dit ; `--replace` l'autorise ;
- volume absent = NaN.

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
dédupliqués, ordre rétabli, période inversée refusée d'emblée. Une page
Parquet **corrompue** est une erreur qui nomme le fichier (v0.8.3 : elle
était prise pour la fin de la colonne, et l'année se relisait amputée
sans un mot) ; la mémoire réservée d'après l'en-tête est bornée à une
année de M1, quoi que le fichier annonce.

Le téléchargement est borné lui aussi (v0.8.3) : une réponse, ou un
fichier une fois décompressé, au-delà de 64 Mo est refusé sans nouvelle
tentative (un jour Dukascopy décompressé tient en 1 440 × 24 octets), et
un `Retry-After` ne peut suspendre le téléchargement plus de 5 minutes
par tentative.

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
| `gw`, écran **2 Journal**, onglet trades | `e` | Le journal des trades, **filtre compris** |
| `gw backtrain`, écran **3 Backtest** | `e` | Trades, courbe de valeur, métriques (trois fichiers) |
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

Une ligne dans `data.Instruments` : nombre de décimales, classe, devise
de base, devise de cotation. Les deux devises ne sont pas décoratives —
elles décident de la conversion du notionnel et du P&L (cf.
[architecture.md](architecture.md#devises)). Ce qui est propre à un
fournisseur reste dans sa source : un identifiant Dukascopy différent du
symbole va dans `dukascopyIDs`, une paire publiée par FXCM dans
`fxcmSymbols` — après avoir CONSTATÉ qu'elle y est servie.
