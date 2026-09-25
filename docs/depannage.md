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
| Trader un compte papier IB en mode `live`, ou l'inverse | refuse la connexion en nommant le compte |
| Envoyer une sortie pendant que ses barrières vivent encore chez IB | annule les barrières, attend la confirmation, puis seulement sort |
| Laisser une position nue quand IB annule une barrière | ferme la position au marché |
| Prendre la sortie d'une position d'avant le démarrage pour une entrée | ne journalise rien et le dit |
| Se connecter à ARGENT RÉEL sur une seule touche | demande de taper le numéro du compte, refuse s'il n'est pas celui de la session |
| Proposer au même rang des paires qui ne peuvent pas trader | ne propose que les paires dimensionnables dans la devise du compte (`v` pour tout voir) |
| Laisser « pourquoi rien ne se passe ? » sans réponse | une ligne de contrôle nomme, pour la paire sélectionnée, la condition qui bloque |
| Couper un écran sur un petit terminal | passe en disposition compacte ; rien n'est coupé dès 60×18 |
| Laisser une position traverser le week-end en live quand le backtest l'aurait fermée | ferme dans les 5 minutes avant la clôture hebdomadaire (vendredi 17 h, New York) ; en retard si aucun tick n'est arrivé à temps, et le journal le dit |
| Appliquer les variances d'une paire, d'une unité de temps ou d'une définition à une autre (Troglodyte) | refuse le modèle en nommant ce qui diffère |
| Afficher une AUC pour un moteur qui n'est pas un classifieur | affiche `—` |
| Prendre « aucune annonce » pour « calendrier absent » | compte à part les entrées décidées hors du calendrier archivé, et en avertit |
| Donner les actualités à Colibri | ne les donne qu'aux stratégies qui les déclarent ; un test l'interdit à Colibri |
| Fermer une position courte sur un signal pensé pour une longue | sorties orientées : `ExitLong` ne ferme qu'une position longue |


## Quand ça se passe mal

| Ce qui arrive | Ce que fait Golden Waterfall |
|---|---|
| Dukascopy répond 429 (limite de débit) | attend longuement, honore `Retry-After`, et ne monte jamais la concurrence |
| Le calendrier économique est injoignable | ne déclare rien couvert, garde l'archive, le dit (`gw news`, journal) |
| Un fichier étranger traîne dans le dossier `news/` | l'ignore : seuls les fichiers de semaine (`AAAA-MM-JJ.json`) comptent |
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
| TWS injoignable, API désactivée, mauvais port | refuse la connexion en disant quoi vérifier |
| « client id is already in use » (IB, code 326) | refuse la connexion : changer `broker.client_id` |
| TWS perd sa liaison avec IB (code 1100) | passe « déconnecté » jusqu'au rétablissement ; aucun ordre ne part |
| Le socket TWS se ferme | journalise l'erreur ; les stops et limites restent chez IB ; `c` reconnecte |
| La devise du compte IB diffère de `backtest.account_currency` | refuse la connexion : le dimensionnement au risque serait faux |

## Où vivent vos données

Jamais à côté du binaire — celui-ci est unique et déplaçable. `gw paths`
affiche les emplacements exacts de la machine.

| | Configuration | Données |
|---|---|---|
| Linux | `~/.config/golden-waterfall` | `~/.local/share/golden-waterfall` |
| macOS | `~/Library/Application Support/GoldenWaterfall` | idem |
| Windows | `%AppData%\GoldenWaterfall` | `%LocalAppData%\GoldenWaterfall` |

Le dossier de données contient l'historique, les modèles entraînés, la base
des trades, les exports CSV (`exports/`) et les journaux. `GW_CONFIG_DIR` et `GW_DATA_DIR` forcent ces
emplacements, pour une installation portable ou un conteneur.
