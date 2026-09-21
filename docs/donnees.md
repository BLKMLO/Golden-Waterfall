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

## Stockage : le format `.gwb`

```
history/<SYMBOLE>/<SYMBOLE>_m1_<année>.gwb
```

En-tête de 64 octets (magie, version, symbole, année, échelle, nombre de
bougies), puis des enregistrements de **40 octets à taille fixe** :

| Champ | Type | Note |
|---|---|---|
| offset | `int32` | secondes depuis le 1er janvier de l'année, UTC |
| bid o/h/l/c | 4 × `int32` | prix ENTIERS mis à l'échelle |
| ask o/h/l/c | 4 × `int32` | `0` = côté ask absent (un prix réel n'est jamais nul) |
| volume | `float32` | volume de TICKS en forex |

Pourquoi pas un format colonne générique :

1. aucune bibliothèque colonne lourde à embarquer — la promesse « un seul
   binaire » tient ;
2. les prix sont stockés **exactement comme Dukascopy les publie**, en
   entiers : la conversion est sans perte et le fichier est deux fois plus
   petit qu'en `float64` ;
3. à taille d'enregistrement fixe, lire une tranche de dates est un
   **seek** — pas le décodage de toute l'année pour trois mois.

L'écriture passe par un fichier temporaire renommé à la fin : une
interruption laisse l'ancien fichier intact plutôt qu'un fichier tronqué
que la relecture prendrait pour des données. Un fichier plus court que son
en-tête ne l'annonce est **refusé** avec le remède.

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

Le dossier de données contient `history/`, `models/`, `gw.db` (journal des
trades, interrupteurs, repères d'équité) et `logs/`.

## Ordres de grandeur

Une année de M1 sur une paire forex ≈ 372 000 bougies ≈ **15 Mo**. Trente
et un instruments sur quinze ans : compter **6 à 8 Go** et plusieurs heures
de téléchargement à concurrence 3.

## Ajouter un instrument

Une ligne dans `data.Instruments` : identifiant Dukascopy, nombre de
décimales, classe, devise de base, devise de cotation. Les deux devises ne
sont pas décoratives — elles décident de la conversion du notionnel et du
P&L (cf. [architecture.md](architecture.md#devises)).
