package view

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/export"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
)

// Journal affiche le journal applicatif ET le journal des trades.
//
// Dans une TUI, stdout est l'écran : un log écrit directement
// corromprait l'affichage. Tout passe donc par un tampon mémoire (et le
// fichier), et cet écran en est la fenêtre.
type Journal struct {
	deps   Deps
	level  slog.Level
	follow bool
	offset int
	tab    int // 0 = journal applicatif, 1 = trades

	// Les trades sont relus au RYTHME DU TICK, pas à chaque image.
	//
	// Les relire dans Render ouvrait une transaction et décodait cinq
	// cents enregistrements JSON à chaque redessin — plusieurs fois par
	// seconde, pour des données qui changent au rythme des exécutions.
	trades      []core.Trade
	tradesErr   string
	tradesFresh bool

	// Filtre texte. Le niveau minimum ne suffit pas : sur un millier de
	// lignes, retrouver ce qu'une paire a fait demande de chercher son
	// nom, pas de baisser un seuil de gravité.
	filter    string
	searching bool
	buffer    string
}

// NewJournal construit l'écran de journal.
func NewJournal(deps Deps) Model {
	return &Journal{deps: deps, level: slog.LevelInfo, follow: true}
}

func (v *Journal) Title() string { return "Journal" }
func (v *Journal) Busy() bool    { return false }
func (v *Journal) Init() tea.Cmd { return nil }

func (v *Journal) Keys() [][2]string {
	return [][2]string{
		{"/", "filtrer"},
		{"f", "niveau minimum"},
		{"s", "suivre / figer"},
		{"t", "journal / trades"},
		{"e", "exporter les trades"},
		{"↑↓ pgup pgdn", "défiler"},
	}
}

// CapturesKeys : pendant une saisie de filtre, l'écran prend TOUTES les
// touches. Sans cela, taper « 3 » dans le filtre changerait d'onglet et
// « q » quitterait le programme au milieu d'un mot.
func (v *Journal) CapturesKeys() bool { return v.searching }

// searchKey traite la saisie du filtre.
func (v *Journal) searchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEnter:
		v.filter, v.searching, v.offset = strings.TrimSpace(v.buffer), false, 0
		if v.filter == "" {
			v.deps.Status("filtre effacé")
		} else {
			v.deps.Status("filtre : " + v.filter)
		}
	case tea.KeyEsc:
		v.searching, v.buffer = false, ""
		v.deps.Status("recherche abandonnée — le filtre précédent reste actif")
	case tea.KeyBackspace:
		if r := []rune(v.buffer); len(r) > 0 {
			v.buffer = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		v.buffer += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			v.buffer += " "
		}
	}
}

// matches : la comparaison est insensible à la casse, parce que personne
// ne tape « EURUSD » en majuscules pour chercher une ligne de journal.
func (v *Journal) matches(text string) bool {
	if v.filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(v.filter))
}

func (v *Journal) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if v.searching {
			v.searchKey(msg)
			return v, nil
		}
		if msg.String() == "t" {
			v.tradesFresh = false // forcer une relecture au changement d'onglet
		}
		switch msg.String() {
		case "/":
			v.searching, v.buffer = true, v.filter
		case "esc":
			if v.filter != "" {
				v.filter, v.offset = "", 0
				v.deps.Status("filtre effacé")
			}
		case "e":
			v.exportTrades()
		case "f":
			// Cycle debug → info → warn → error → debug.
			switch v.level {
			case slog.LevelDebug:
				v.level = slog.LevelInfo
			case slog.LevelInfo:
				v.level = slog.LevelWarn
			case slog.LevelWarn:
				v.level = slog.LevelError
			default:
				v.level = slog.LevelDebug
			}
			v.deps.Status("niveau minimum : " + v.level.String())
		case "s":
			v.follow = !v.follow
			if v.follow {
				v.deps.Status("défilement automatique activé")
			} else {
				v.deps.Status("affichage figé")
			}
		case "t":
			v.tab = 1 - v.tab
			v.offset = 0
		case "up", "K":
			v.offset++
			v.follow = false
		case "down", "J":
			if v.offset > 0 {
				v.offset--
			}
		case "pgup":
			v.offset += 10
			v.follow = false
		case "pgdown":
			v.offset -= 10
			if v.offset < 0 {
				v.offset = 0
			}
		case "home":
			v.offset = 1 << 20
			v.follow = false
		case "end":
			v.offset = 0
			v.follow = true
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			v.offset += 3
			v.follow = false
		case tea.MouseButtonWheelDown:
			v.offset -= 3
			if v.offset < 0 {
				v.offset = 0
			}
		}

	default:
		// Tout autre message (le tick de rafraîchissement, notamment)
		// rafraîchit le journal des trades.
		v.reloadTrades()
	}
	return v, nil
}

// exportTrades écrit le journal des trades en CSV.
//
// Ce qui est exporté est ce qui est AFFICHÉ, filtre compris : un fichier
// dont le contenu ne correspond pas à l'écran qui l'a produit est un
// piège, et la ligne d'état dit combien de trades sont partis.
func (v *Journal) exportTrades() {
	if !v.tradesFresh {
		v.reloadTrades()
	}
	if v.tradesErr != "" {
		v.deps.Status("export impossible : " + v.tradesErr)
		return
	}
	trades := v.visibleTrades()
	if len(trades) == 0 {
		v.deps.Status("aucun trade à exporter")
		return
	}
	path := filepath.Join(v.deps.App.Config.Paths.ExportsDir(),
		export.Name("trades", time.Now()))
	if err := export.Trades(path, trades); err != nil {
		v.deps.Status("export impossible : " + err.Error())
		return
	}
	v.deps.Status(fmt.Sprintf("%d trades exportés → %s", len(trades), path))
}

// visibleTrades applique le filtre texte au journal des trades.
func (v *Journal) visibleTrades() []core.Trade {
	if v.filter == "" {
		return v.trades
	}
	out := make([]core.Trade, 0, len(v.trades))
	for _, t := range v.trades {
		if v.matches(t.Symbol + " " + string(t.Side) + " " + t.ExitReason + " " + t.Strategy) {
			out = append(out, t)
		}
	}
	return out
}

func (v *Journal) reloadTrades() {
	trades, err := v.deps.App.Store.Trades(maxJournalTrades)
	if err != nil {
		v.tradesErr = err.Error()
		return
	}
	v.trades, v.tradesErr, v.tradesFresh = trades, "", true
}

// maxJournalTrades borne ce que l'écran relit : au-delà, un terminal
// n'affiche pas plus d'information, il consomme juste plus de mémoire.
const maxJournalTrades = 500

func (v *Journal) Render(width, height int) string {
	if v.tab == 1 {
		return v.renderTrades(width, height)
	}
	return v.renderLog(width, height)
}

func (v *Journal) renderLog(width, height int) string {
	th := v.deps.Theme
	lines := v.deps.App.Logging.Buffer.Lines()

	filtered := make([]core.LogLine, 0, len(lines))
	for _, l := range lines {
		if l.Level >= v.level && v.matches(l.String()) {
			filtered = append(filtered, l)
		}
	}

	visible := height - 4
	if visible < 3 {
		visible = 3
	}
	if v.follow {
		v.offset = 0
	}
	end := len(filtered) - v.offset
	if end > len(filtered) {
		end = len(filtered)
	}
	if end < 0 {
		end = 0
	}
	start := end - visible
	if start < 0 {
		start = 0
	}
	if v.offset > len(filtered) {
		v.offset = maxInt(len(filtered)-visible, 0)
	}

	var sb strings.Builder
	for _, l := range filtered[start:end] {
		style := th.Text
		switch {
		case l.Level >= slog.LevelError:
			style = th.Negative
		case l.Level >= slog.LevelWarn:
			style = th.Warning
		case l.Level < slog.LevelInfo:
			style = th.Muted
		}
		sb.WriteString(style.Render(component.Truncate(l.String(), width-6)) + "\n")
	}
	if len(filtered) == 0 {
		// Dire POURQUOI la liste est vide : un filtre oublié ressemble
		// trait pour trait à un programme silencieux.
		reason := "(aucune ligne à ce niveau)"
		if v.filter != "" {
			reason = fmt.Sprintf("(aucune ligne à ce niveau contenant « %s » — échap efface le filtre)", v.filter)
		}
		sb.WriteString(th.Muted.Render(reason))
	}

	mode := "figé"
	if v.follow {
		mode = "suivi"
	}
	title := fmt.Sprintf("Journal applicatif · niveau ≥ %s · %s · %d/%d lignes",
		v.level.String(), mode, len(filtered), len(lines))
	body := sb.String() + "\n" + v.filterLine(width) +
		th.Muted.Render("fichier : "+v.deps.App.Config.Paths.LogFile())
	return component.Panel(th, title, body, width)
}

// filterLine affiche la saisie en cours ou le filtre actif. Elle renvoie
// une chaîne VIDE quand il n'y a rien à dire, pour ne pas voler une ligne
// à l'affichage.
func (v *Journal) filterLine(width int) string {
	th := v.deps.Theme
	switch {
	case v.searching:
		return th.Accent.Render("/"+component.Truncate(v.buffer, width-8)+"▏") +
			th.Muted.Render("  entrée valide · échap annule") + "\n"
	case v.filter != "":
		return th.Info.Render("filtre : "+component.Truncate(v.filter, width-24)) +
			th.Muted.Render("  échap efface") + "\n"
	}
	return ""
}

func (v *Journal) renderTrades(width, height int) string {
	th := v.deps.Theme
	if !v.tradesFresh {
		v.reloadTrades()
	}
	if v.tradesErr != "" {
		return component.Panel(th, "Trades", th.Negative.Render("journal illisible : "+v.tradesErr), width)
	}
	trades := v.visibleTrades()

	cols := []component.Column{
		{Title: "#", Width: 6, Right: true, Priority: 4},
		{Title: "Paire", Width: 8},
		{Title: "Sens", Width: 6, Priority: 3},
		{Title: "Entrée", Width: 17, Priority: 2},
		{Title: "Sortie", Width: 17},
		{Title: "P&L", Width: 12, Right: true},
		{Title: "Motif", Width: 20, Flex: true, Min: 8, Priority: 1},
	}
	rows := make([][]string, 0, len(trades))
	for _, t := range trades {
		side := th.Positive.Render("LONG")
		if t.Side != "BUY" {
			side = th.Negative.Render("SHORT")
		}
		pnl := component.Money(t.PnL)
		if t.PnL > 0 {
			pnl = th.Positive.Render(pnl)
		} else if t.PnL < 0 {
			pnl = th.Negative.Render(pnl)
		} else {
			pnl = th.Muted.Render(component.Dash)
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", t.ID), t.Symbol, side,
			component.Time(t.EntryTime), component.Time(t.ExitTime),
			pnl, component.Truncate(t.ExitReason, 20),
		})
	}
	body := component.Table(th, cols, rows, -1, height-5, component.PanelContent(width))
	body += "\n" + v.filterLine(width) + th.Muted.Render(
		"Ce journal ne contient QUE des exécutions rapportées par une passerelle. "+
			"Aucun trade simulé n'y figure. · e exporte en CSV")
	title := fmt.Sprintf("Trades exécutés (%d)", len(trades))
	if v.filter != "" {
		title = fmt.Sprintf("Trades exécutés (%d sur %d)", len(trades), len(v.trades))
	}
	return component.Panel(th, title, body, width)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
