// Package news fournit le calendrier des annonces économiques et le FILTRE
// qui en découle : pas d'entrée dans la fenêtre d'une annonce importante
// sur l'une des deux devises d'une paire.
//
// # Qui y a droit
//
// Le filtre ne s'applique qu'aux stratégies qui le DÉCLARENT
// (`strategy.Description.UsesNews`). Colibri ne le déclare pas et n'y a
// donc jamais accès : ses révisions publiées ont appris une cible sans
// actualités, et la leur donner changerait ce qu'elles veulent dire. Une
// stratégie ne va jamais elle-même sur internet : le calendrier est
// récupéré ici, archivé sur le disque, et appliqué par les MOTEURS
// (backtest et live) avec la même fonction, Gate.Check.
//
// # Modulaire
//
// Une source de calendrier est un module remplaçable, enregistré comme une
// passerelle : `Register()` dans un `init()`, choisie par `news.source`.
// Deux sont livrées : « forexfactory » (flux public de la semaine en cours)
// et « none » (aucune récupération : l'archive seule, alimentée par
// `gw news import`).
//
// # Honnêteté
//
// Le flux public ne sert que la semaine EN COURS. Tout ce qui est récupéré
// est donc archivé, semaine par semaine, et le calendrier sait quelles
// périodes il COUVRE. Une entrée décidée hors de la période couverte n'est
// pas filtrée — on ne sait pas s'il y avait une annonce — et elle est
// COMPTÉE à part (Uncovered), jamais confondue avec « aucune annonce ».
package news

import (
	"fmt"
	"strings"
	"time"
)

// Impact : importance d'une annonce, telle que la source la publie.
type Impact int

const (
	// ImpactNone : jour férié ou impact inconnu. Jamais filtrant.
	ImpactNone Impact = iota
	ImpactLow
	ImpactMedium
	ImpactHigh
)

// ParseImpact lit un niveau d'impact (« low », « medium », « high »,
// insensible à la casse). « holiday » et le vide donnent ImpactNone.
func ParseImpact(s string) (Impact, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return ImpactLow, nil
	case "medium":
		return ImpactMedium, nil
	case "high":
		return ImpactHigh, nil
	case "holiday", "non-economic", "":
		return ImpactNone, nil
	}
	return ImpactNone, fmt.Errorf("impact inconnu %q (low, medium, high)", s)
}

func (i Impact) String() string {
	switch i {
	case ImpactLow:
		return "low"
	case ImpactMedium:
		return "medium"
	case ImpactHigh:
		return "high"
	}
	return "none"
}

// Event : une annonce datée, sur une devise.
type Event struct {
	Time     time.Time `json:"time"`
	Currency string    `json:"currency"`
	Impact   Impact    `json:"impact"`
	Title    string    `json:"title"`
}

func (e Event) String() string {
	return fmt.Sprintf("%s %s %s (%s)", e.Time.UTC().Format("2006-01-02 15:04"), e.Currency, e.Title, e.Impact)
}

// newYork : les semaines du calendrier forex commencent le dimanche, à
// New York (réouverture du marché).
var newYork = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Base des fuseaux embarquée par le paquet core (time/tzdata).
		panic("fuseau America/New_York introuvable : " + err.Error())
	}
	return loc
}()

// WeekOf renvoie la semaine de calendrier qui contient t : du dimanche
// 0 h au dimanche suivant 0 h, heure de New York.
func WeekOf(t time.Time) (from, to time.Time) {
	local := t.In(newYork)
	from = time.Date(local.Year(), local.Month(), local.Day()-int(local.Weekday()), 0, 0, 0, 0, newYork)
	return from, from.AddDate(0, 0, 7)
}
