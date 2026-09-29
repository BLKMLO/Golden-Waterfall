// Package strategies est le CATALOGUE des moteurs de décision livrés avec
// Golden Waterfall : le seul endroit du programme qui nomme une
// implémentation de stratégie.
//
// Chaque génération de moteur vit dans son propre paquet et s'enregistre
// dans le registre de `strategy` depuis un init(). Importer ce catalogue
// suffit à les rendre toutes disponibles ; rien d'autre — moteurs de
// backtest et de live, walk-forward, interface, CLI — ne dépend d'elles.
//
// Une génération ne livre que sa DERNIÈRE révision (règle du propriétaire,
// v0.8.0) ; les révisions retirées sont déclarées par `strategy.Retire`,
// pour qu'une configuration restée sur l'une d'elles apprenne laquelle
// prendre.
//
// Ajouter la génération suivante : écrire son paquet, ajouter UNE ligne
// ci-dessous, et choisir son nom dans `strategy.name` (config.yaml). Retirer
// une génération : supprimer sa ligne ; ses modèles archivés deviennent
// simplement « stratégie inconnue » au chargement, jamais un modèle
// appliqué par un autre moteur.
package strategies

import (
	// Colibri — première génération, classifieur GBDT (colibri_v1_2).
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy/colibri"
	// Troglodyte — deuxième génération, tendance structurelle (troglodyte_v1_1).
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy/troglodyte"
	// Martinet — troisième génération, scalping de zones de liquidité (martinet_v1_0).
	_ "github.com/BLKMLO/Golden-Waterfall/internal/strategy/martinet"
)
