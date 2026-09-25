// Package strategies est le CATALOGUE des moteurs de décision livrés avec
// Golden Waterfall : le seul endroit du programme qui nomme une
// implémentation de stratégie.
//
// Chaque génération de moteur vit dans son propre paquet et s'enregistre
// dans le registre de `strategy` depuis un init(). Importer ce catalogue
// suffit à les rendre toutes disponibles ; rien d'autre — moteurs de
// backtest et de live, walk-forward, interface, CLI — ne dépend d'elles.
//
// Ajouter la génération suivante : écrire son paquet, ajouter UNE ligne
// ci-dessous, et choisir son nom dans `strategy.name` (config.yaml). Retirer
// une génération : supprimer sa ligne ; ses modèles archivés deviennent
// simplement « stratégie inconnue » au chargement, jamais un modèle
// appliqué par un autre moteur.
package strategies

import (
	// Colibri — première génération (révisions colibri_v1_0 à v1_2).
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy/colibri"
	// Troglodyte — deuxième génération (révision troglodyte_v1_0).
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy/troglodyte"
)
