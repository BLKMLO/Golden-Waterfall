// Package view contient les écrans de l'interface. Chaque écran est un
// modèle Bubbletea autonome : il possède son état, ses touches et son
// rendu, et ne connaît des autres écrans que ce que le routeur lui passe.
package view

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// Deps : tout ce qu'un écran reçoit du routeur.
type Deps struct {
	App   *app.App
	Theme theme.Theme
	// Emit pousse un message dans la boucle Bubbletea depuis une
	// goroutine de travail. Non bloquant : un écran qui n'écoute plus ne
	// doit jamais figer un téléchargement ou un entraînement.
	Emit func(tea.Msg)
	// Status affiche un message éphémère dans la barre d'état.
	Status func(string)
}

// Model : contrat d'un écran.
type Model interface {
	// Title est le libellé de l'onglet.
	Title() string
	// Init renvoie la commande initiale de l'écran.
	Init() tea.Cmd
	// Update traite un message et renvoie l'écran mis à jour.
	Update(msg tea.Msg) (Model, tea.Cmd)
	// Render dessine l'écran dans l'espace donné.
	Render(width, height int) string
	// Keys liste les raccourcis de l'écran pour la barre d'aide.
	Keys() [][2]string
	// Busy indique qu'un travail de fond est en cours : le routeur
	// affiche alors un indicateur et évite de proposer de quitter sans
	// prévenir.
	Busy() bool
}

// KeyCapturer : un écran qui, dans certains états, a besoin des touches
// que le routeur intercepte normalement.
//
// Sans ce contrat, saisir « 3 » dans un champ de l'écran Paramètres
// basculerait sur le troisième onglet, et « q » quitterait le programme au
// milieu d'une saisie. Seul Ctrl+C reste toujours global : il doit rester
// possible de sortir quoi qu'il arrive.
type KeyCapturer interface {
	CapturesKeys() bool
}

// JobDone signale la fin d'un travail de fond lancé par un écran.
type JobDone struct {
	View string
	Err  error
}
