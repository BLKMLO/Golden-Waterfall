// Package tui est le routeur de l'interface : onglets, entête, barre
// d'état, raccourcis globaux. Il ne contient AUCUNE logique de trading —
// il lit des photos d'état (live.Snapshot) et délègue le reste aux écrans.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/live"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/view"
)

// tickMsg cadence le rafraîchissement des écrans temps réel.
type tickMsg time.Time

// statusLifetime : durée d'affichage d'un message éphémère.
const statusLifetime = 8 * time.Second

type statusMsg struct {
	text string
	at   time.Time
}

// Model est le modèle racine.
type Model struct {
	app    *app.App
	th     theme.Theme
	deps   view.Deps
	views  []view.Model
	active int

	width, height int
	events        chan tea.Msg
	status        string
	statusAt      time.Time
	showHelp      bool
	helpOffset    int
	refresh       time.Duration
	quitting      bool
}

// New construit le modèle racine et ses écrans.
func New(a *app.App) *Model {
	// L'ordre compte : Apply fixe la luminosité de fond AVANT que les
	// styles ne résolvent leurs couleurs adaptatives.
	theme.Apply(a.Config.UI.Theme)
	th := theme.ByName(a.Config.UI.Theme)
	m := &Model{
		app:     a,
		th:      th,
		events:  make(chan tea.Msg, 256),
		refresh: time.Duration(a.Config.UI.RefreshMillis) * time.Millisecond,
	}
	m.deps = view.Deps{
		App:   a,
		Theme: th,
		Emit: func(msg tea.Msg) {
			// Non bloquant : un écran qui n'écoute plus ne doit pas figer
			// la goroutine d'un entraînement.
			select {
			case m.events <- msg:
			default:
			}
		},
		Status: func(text string) {
			select {
			case m.events <- statusMsg{text: text, at: time.Now()}:
			default:
			}
		},
	}
	m.views = []view.Model{
		view.NewLive(m.deps),
		view.NewData(m.deps),
		view.NewBacktest(m.deps),
		view.NewTraining(m.deps),
		view.NewJournal(m.deps),
		view.NewSettings(m.deps),
	}
	return m
}

// Run démarre l'interface en plein écran.
func Run(a *app.App) error {
	m := New(a)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.tick(), m.listen()}
	for _, v := range m.views {
		if c := v.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// listen relaie les messages des goroutines de travail vers la boucle.
func (m *Model) listen() tea.Cmd {
	return func() tea.Msg { return <-m.events }
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tickMsg:
		// Le tick va à TOUS les écrans : celui du fond doit continuer de
		// suivre son travail même s'il n'est pas affiché.
		cmds := []tea.Cmd{m.tick()}
		for i, v := range m.views {
			updated, cmd := v.Update(msg)
			m.views[i] = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case statusMsg:
		m.status, m.statusAt = msg.text, msg.at
		return m, m.listen()

	case tea.KeyMsg:
		if cmd, handled := m.globalKey(msg); handled {
			return m, cmd
		}

	case tea.MouseMsg:
		// Les molettes servent au défilement de l'écran actif ; on laisse
		// passer, chaque écran décide.
	}

	// Tout le reste va à l'écran actif, puis on se remet à l'écoute si le
	// message venait du canal de travail.
	updated, cmd := m.views[m.active].Update(msg)
	m.views[m.active] = updated

	cmds := []tea.Cmd{cmd}
	if isWorkerMsg(msg) {
		// Un message de travail peut concerner un écran NON actif : on le
		// diffuse à tous, sinon un téléchargement lancé puis quitté
		// n'afficherait plus jamais sa fin.
		for i, v := range m.views {
			if i == m.active {
				continue
			}
			other, c := v.Update(msg)
			m.views[i] = other
			if c != nil {
				cmds = append(cmds, c)
			}
		}
		cmds = append(cmds, m.listen())
	}
	return m, tea.Batch(cmds...)
}

func isWorkerMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, tea.WindowSizeMsg, tickMsg, statusMsg:
		return false
	}
	return true
}

func (m *Model) globalKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return tea.Quit, true
	}
	// L'aide est une FENÊTRE MODALE : tant qu'elle couvre l'écran, les
	// touches lui appartiennent. Sans cela, les flèches faisaient défiler
	// un tableau invisible derrière elle.
	if m.showHelp {
		return m.helpKey(msg)
	}
	// Un écran en SAISIE garde toutes ses touches : un chiffre tapé dans
	// un champ ne doit pas changer d'onglet.
	if c, ok := m.views[m.active].(view.KeyCapturer); ok && c.CapturesKeys() {
		return nil, false
	}
	switch msg.String() {
	case "q":
		// « q » ne quitte que si aucun écran ne travaille : interrompre un
		// entraînement de vingt minutes sur une frappe malheureuse serait
		// cruel.
		if m.anyBusy() {
			m.status = "un travail est en cours — Ctrl+C pour forcer la sortie"
			m.statusAt = time.Now()
			return nil, true
		}
		m.quitting = true
		return tea.Quit, true
	case "?":
		m.showHelp = !m.showHelp
		m.helpOffset = 0
		return nil, true
	case "tab":
		m.active = (m.active + 1) % len(m.views)
		return nil, true
	case "shift+tab":
		m.active = (m.active - 1 + len(m.views)) % len(m.views)
		return nil, true
	}
	// ← et → ne changent PLUS d'onglet : ce sont les touches naturelles
	// pour régler une valeur, et aucun écran ne pouvait s'en servir tant
	// que le routeur les interceptait. tab, ⇧tab et les chiffres suffisent
	// à circuler.
	if len(msg.String()) == 1 && msg.String() >= "1" && msg.String() <= "9" {
		idx := int(msg.String()[0] - '1')
		if idx < len(m.views) {
			m.active = idx
			return nil, true
		}
	}
	return nil, false
}

// helpKey traite les touches pendant que l'aide est affichée.
func (m *Model) helpKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "?", "esc", "q":
		m.showHelp, m.helpOffset = false, 0
	case "up", "K":
		m.helpOffset--
	case "down", "J":
		m.helpOffset++
	case "pgup":
		m.helpOffset -= 10
	case "pgdown":
		m.helpOffset += 10
	case "home":
		m.helpOffset = 0
	}
	if m.helpOffset < 0 {
		m.helpOffset = 0
	}
	// Toute autre touche est AVALÉE : l'écran de dessous ne doit pas
	// réagir à une frappe destinée à une fenêtre qui le couvre.
	return nil, true
}

func (m *Model) anyBusy() bool {
	for _, v := range m.views {
		if v.Busy() {
			return true
		}
	}
	return false
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width < 60 || m.height < 18 {
		return m.th.Warning.Render(fmt.Sprintf(
			"Terminal trop petit : %d×%d. Golden Waterfall demande au moins 60×18.",
			m.width, m.height))
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	body := m.views[m.active].Render(m.width, bodyHeight)
	if m.showHelp {
		body = m.renderHelp(bodyHeight)
	}
	// Le pied de page est ANCRÉ en bas : un écran dont le contenu est
	// court ne doit pas faire remonter les raccourcis au milieu de rien.
	return strings.Join([]string{header, component.Fit(m.th, body, bodyHeight), footer}, "\n")
}

// renderHeader affiche le titre, les onglets et les badges d'état.
//
// Les badges sont la première chose que l'utilisateur voit : mode, nature
// de la passerelle, connexion, kill-switch. Une session de REJEU doit se
// reconnaître d'un coup d'œil, sans avoir à se souvenir de ce qu'on a
// lancé.
func (m *Model) renderHeader() string {
	snap := m.app.Live.Snapshot()

	badges := m.badges(snap, false)
	right := lipgloss.JoinHorizontal(lipgloss.Top, badges...)

	// Les badges d'état ne se négocient PAS.
	//
	// Ils disaient « LIVE — ARGENT RÉEL », « passerelle connectée »,
	// « kill-switch armé » — et ils étaient les premiers supprimés quand
	// la largeur manquait, dès 100 colonnes. On rogne donc dans l'autre
	// sens : d'abord les libellés d'onglets (leur numéro suffit à les
	// atteindre), ensuite les badges eux-mêmes en version courte, jamais
	// l'avertissement de mode.
	//
	// Dernier cran avant de passer les badges sur leur propre ligne : des
	// onglets réduits à leur chiffre, sans marge. Une ligne gagnée compte
	// quand le terminal n'en a que dix-huit.
	for _, attempt := range []struct {
		compactTabs, compactBadges, bareTabs bool
	}{{false, false, false}, {true, false, false}, {true, true, false}, {true, true, true}} {
		if attempt.compactBadges {
			badges = m.badges(snap, true)
			right = lipgloss.JoinHorizontal(lipgloss.Top, badges...)
		}
		left := m.headerLeft(attempt.compactTabs)
		if attempt.bareTabs {
			left = m.headerBare()
		}
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap >= 1 {
			return left + strings.Repeat(" ", gap) + right + "\n" + m.rule()
		}
	}
	// Terminal vraiment étroit : les badges passent sur leur propre ligne
	// plutôt que de disparaître.
	return component.Clip(m.headerLeft(true), m.width) + "\n" +
		component.Clip(right, m.width) + "\n" + m.rule()
}

// headerLeft dessine le titre et les onglets, en version longue ou
// compacte.
func (m *Model) headerLeft(compact bool) string {
	parts := []string{}
	if !compact {
		parts = append(parts, m.th.Title.Render("◆ Golden Waterfall"), " ")
	}
	for i, v := range m.views {
		label := fmt.Sprintf(" %d %s ", i+1, v.Title())
		if compact {
			label = fmt.Sprintf(" %d ", i+1)
		}
		if v.Busy() {
			label += "⣿"
		}
		if i == m.active {
			parts = append(parts, m.th.TabActive.Render(label))
		} else {
			parts = append(parts, m.th.Tab.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// headerBare : onglets réduits à leur chiffre, séparés d'une espace.
func (m *Model) headerBare() string {
	parts := make([]string, 0, len(m.views))
	for i, v := range m.views {
		label := fmt.Sprintf("%d", i+1)
		if v.Busy() {
			label += "⣿"
		}
		style := m.th.Tab
		if i == m.active {
			style = m.th.TabActive
		}
		parts = append(parts, style.UnsetPadding().Render(label))
	}
	return strings.Join(parts, " ")
}

// badges construit les pastilles d'état. En version courte, les libellés
// raccourcissent mais AUCUNE pastille ne disparaît.
func (m *Model) badges(snap live.Snapshot, compact bool) []string {
	pick := func(long, short string) string {
		if compact {
			return short
		}
		return long
	}
	out := []string{}
	switch {
	case snap.Simulated:
		out = append(out, m.th.BadgeSim.Render(pick("REJEU — COMPTE SIMULÉ", "REJEU")))
	case snap.Mode == "live":
		out = append(out, m.th.BadgeWarn.Render(pick("LIVE — ARGENT RÉEL", "LIVE €")))
	default:
		out = append(out, m.th.BadgeOff.Render("PAPER"))
	}
	// La devise du compte décide des paires qui peuvent trader (21 sur 31
	// ne le peuvent pas sur un compte en dollars) : elle se lit en
	// permanence, pas seulement dans l'écran Paramètres.
	cur := m.app.Config.Backtest.AccountCurrency
	out = append(out, m.th.BadgeOff.Render(pick("compte "+cur, cur)))
	out = append(out, component.Badge(m.th, snap.GatewayName, snap.Connected))
	if !snap.SupportsBracket {
		// Le moteur refusera toute entrée : mieux vaut le lire dans
		// l'entête que le déduire d'une heure sans ordre.
		out = append(out, m.th.BadgeWarn.Render(pick("SANS BARRIÈRES", "SANS SL")))
	}
	out = append(out, component.Badge(m.th, pick("kill-switch", "k-s"), snap.KillSwitch))
	if !compact {
		// Le nom de la stratégie est la seule pastille informative plutôt
		// que protectrice : c'est elle qui cède en dernier recours.
		out = append(out, m.th.BadgeOff.Render(snap.StrategyName))
	}
	return out
}

// rule dessine le filet de séparation, à la largeur EXACTE des panneaux
// — que component.Panel fait désormais tenir dans la largeur demandée.
func (m *Model) rule() string {
	return m.th.Muted.Render(strings.Repeat("─", m.width))
}

func (m *Model) renderFooter() string {
	// Les touches GLOBALES d'abord : elles étaient en fin de liste, donc
	// les premières rognées par le Clip final. Un utilisateur se
	// retrouvait sans « ? aide » ni « q quitter » à l'écran, c'est-à-dire
	// sans moyen d'apprendre comment sortir.
	global := [][2]string{{"tab", "écran"}, {"?", "aide"}, {"q", "quitter"}}
	hints := component.KeyHints(m.th, append(global, m.views[m.active].Keys()...)...)

	// La ligne d'état est TOUJOURS réservée, même vide.
	//
	// La faire apparaître et disparaître changeait la hauteur du pied de
	// page, donc celle du corps : tout l'écran sautait d'une ligne à
	// chaque message, ce qui est exactement ce qu'on ne veut pas d'une
	// interface qu'on regarde en continu.
	status := ""
	if m.status != "" && time.Since(m.statusAt) < statusLifetime {
		status = m.th.Info.Render(" " + component.Truncate(m.status, m.width-2))
	}
	return m.rule() + "\n" + status + "\n" + component.Clip(hints, m.width)
}

// renderHelp dessine l'aide, DÉFILABLE.
//
// Elle occupait cinquante-neuf lignes quelle que soit la fenêtre : sur un
// terminal de vingt-quatre lignes, les trois quarts partaient hors de
// l'écran — y compris la ligne qui explique comment la refermer. Une aide
// qu'on ne peut pas lire en entier, et dont on ne voit pas qu'elle
// continue, est pire que pas d'aide.
func (m *Model) renderHelp(height int) string {
	lines := m.helpLines()

	// La dernière ligne est réservée à l'indicateur de position : il doit
	// rester visible, sinon rien ne dit qu'il y a une suite.
	visible := height - 1
	if visible < 1 {
		visible = 1
	}
	if max := len(lines) - visible; m.helpOffset > max {
		m.helpOffset = maxInt(max, 0)
	}
	end := m.helpOffset + visible
	if end > len(lines) {
		end = len(lines)
	}

	shown := strings.Join(lines[m.helpOffset:end], "\n")
	if len(lines) <= visible {
		return shown
	}
	return shown + "\n" + m.th.Muted.Render(fmt.Sprintf(
		"↑↓ pgup pgdn défiler · lignes %d à %d sur %d · ? ou échap pour fermer",
		m.helpOffset+1, end, len(lines)))
}

// helpLines construit l'aide ligne à ligne : c'est ce découpage qui la
// rend défilable.
func (m *Model) helpLines() []string {
	lines := []string{m.th.Title.Render("Aide"), "", m.th.Subtitle.Render("Navigation")}
	for _, p := range [][2]string{
		{"1…6 / tab / ⇧tab", "changer d'écran"},
		{"?", "afficher ou masquer cette aide"},
		{"q", "quitter (refusé pendant un travail de fond)"},
		{"ctrl+c", "quitter immédiatement"},
	} {
		lines = append(lines, "  "+m.th.KeyCap.Render(component.Pad(p[0], 20))+m.th.Text.Render(p[1]))
	}
	for _, v := range m.views {
		keys := v.Keys()
		if len(keys) == 0 {
			continue
		}
		lines = append(lines, "", m.th.Subtitle.Render(v.Title()))
		for _, p := range keys {
			lines = append(lines, "  "+m.th.KeyCap.Render(component.Pad(p[0], 20))+m.th.Text.Render(p[1]))
		}
	}
	lines = append(lines, "",
		m.th.Muted.Render("Configuration : "+m.app.Config.Paths.ConfigFile()),
		m.th.Muted.Render("Données       : "+m.app.Config.Paths.DataDir))
	return lines
}

// maxInt : Go 1.25 a « max », mais le garder nommé rend les bornes de
// défilement lisibles.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
