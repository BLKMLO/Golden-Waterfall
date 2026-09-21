# Garanties et dépannage

Deux tableaux qui vivaient dans le README : ce que le programme refuse de
faire, et ce qu'il fait quand les choses tournent mal. Chaque ligne
correspond à un champ dans le code et à un test qui échouerait si elle
cessait d'être vraie — ce ne sont pas des intentions.

## Ce qu'il ne fera jamais

| Ce qu'un programme pourrait faire | Ce que celui-ci fait |
|---|---|
| Afficher `0` pour une donnée qu'il n'a pas | affiche `—` |
| Estimer l'équité quand le courtier ne répond pas | affiche des tirets et signale l'anomalie |
| Facturer zéro coût faute de côté ask | dit qu'aucun coût n'est modélisé, et que le résultat est donc optimiste |
| Compter un ordre refusé comme une abstention du modèle | compte les refus à part et les affiche |
| Additionner des P&L nés dans des devises différentes | signale que la somme mélange des devises |
| Présenter un rejeu comme une performance | marque le backtest **IN-SAMPLE** en permanence |
| Inventer un P&L que le courtier n'a pas rapporté | journalise le trade avec un P&L nul et le dit |
| Se déclarer connecté par optimisme | échoue explicitement |
| Fabriquer un aller-retour à partir de deux entrées du même sens | ne journalise rien et signale l'incohérence |
| Appliquer un modèle dont les colonnes ont bougé | refuse le modèle en nommant la colonne fautive |


## Quand ça se passe mal

| Ce qui arrive | Ce que fait Golden Waterfall |
|---|---|
| Dukascopy répond 429 (limite de débit) | attend longuement, honore `Retry-After`, et ne monte jamais la concurrence |
| Un jour n'a pas de donnée (week-end, férié) | le compte comme normal, pas comme un échec |
| Une année se télécharge à moitié | l'écrit, la marque incomplète, et la refait à la prochaine demande |
| Un fichier d'historique est tronqué | refuse de le lire et nomme le fichier et le remède |
| Un fichier au nom inattendu traîne dans le dossier | l'ignore, plutôt que de compter ses bougies deux fois |
| La configuration contient une valeur absurde | refuse de démarrer, en nommant la clé fautive |
| La configuration contient une faute de frappe | refuse de démarrer : une clé inconnue est une limite jamais appliquée |
| Le jeu d'entraînement est trop petit | refuse d'entraîner, plutôt que de livrer un modèle décoratif |
| Une seule classe dans les labels | refuse d'entraîner, plutôt qu'un modèle constant déguisé |
| Le courtier ne répond pas sur les positions | s'abstient de décider, plutôt que de trader sur une image périmée |
| La perte journalière maximale est atteinte | bloque toute nouvelle entrée ; les sorties restent toujours possibles |
| Une deuxième instance est lancée | échoue proprement : la base est déjà ouverte |

## Où vivent vos données

Jamais à côté du binaire — celui-ci est unique et déplaçable. `gw paths`
affiche les emplacements exacts de la machine.

| | Configuration | Données |
|---|---|---|
| Linux | `~/.config/golden-waterfall` | `~/.local/share/golden-waterfall` |
| macOS | `~/Library/Application Support/GoldenWaterfall` | idem |
| Windows | `%AppData%\GoldenWaterfall` | `%LocalAppData%\GoldenWaterfall` |

Le dossier de données contient l'historique, les modèles entraînés, la base
des trades et les journaux. `GW_CONFIG_DIR` et `GW_DATA_DIR` forcent ces
emplacements, pour une installation portable ou un conteneur.
