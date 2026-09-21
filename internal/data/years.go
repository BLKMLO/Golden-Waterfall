package data

import (
	"fmt"
	"time"
)

// FirstYear : première année pour laquelle Dukascopy sert des données sur
// les paires majeures. En demander moins ne renvoie que des 404, ce qui
// fait passer un téléchargement pour un échec.
const FirstYear = 2003

// YearRange est un intervalle d'années, BORNES COMPRISES.
//
// Il existe pour que « je veux juste 2019 pour essayer » soit une demande
// exprimable — et exprimée de la même façon en ligne de commande et dans
// l'interface, plutôt que deux fois avec deux comportements aux bords.
type YearRange struct {
	From int
	To   int
}

// FullRange renvoie l'intervalle complet défini par la configuration :
// de l'année de départ à l'année courante.
func FullRange(startYear int) YearRange {
	return YearRange{From: startYear, To: time.Now().UTC().Year()}.Normalize()
}

// Normalize remet les bornes dans l'ordre et dans le domaine servi.
//
// Les bornes viennent d'un humain — un indicatif de clavier, un argument
// de ligne de commande. Une année à 1987 ou un intervalle à l'envers doit
// se corriger sans bruit plutôt que de produire une heure de requêtes
// vouées au 404.
func (r YearRange) Normalize() YearRange {
	current := time.Now().UTC().Year()
	if r.From > r.To {
		r.From, r.To = r.To, r.From
	}
	if r.From < FirstYear {
		r.From = FirstYear
	}
	if r.To > current {
		r.To = current
	}
	if r.To < r.From {
		r.To = r.From
	}
	return r
}

// Years énumère les années de l'intervalle.
func (r YearRange) Years() []int {
	r = r.Normalize()
	out := make([]int, 0, r.To-r.From+1)
	for y := r.From; y <= r.To; y++ {
		out = append(out, y)
	}
	return out
}

// Covers indique si l'intervalle couvre tout ce que la configuration
// demande. Sert à dire « historique complet » plutôt que « 2003 → 2026 »,
// qui oblige à faire le calcul de tête.
func (r YearRange) Covers(full YearRange) bool {
	r, full = r.Normalize(), full.Normalize()
	return r.From <= full.From && r.To >= full.To
}

// String : « 2019 » pour une année seule, « 2019 → 2021 » sinon.
func (r YearRange) String() string {
	r = r.Normalize()
	if r.From == r.To {
		return fmt.Sprintf("%d", r.From)
	}
	return fmt.Sprintf("%d → %d", r.From, r.To)
}
