package view

import (
	"fmt"
	"log/slog"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
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
		{"f", "niveau minimum"},
		{"s", "suivre / figer"},
		{"t", "journal / trades"},
		{"↑↓ pgup pgdn", "défiler"},
	}
}

func (v *Journal) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "t" {
			v.tradesFresh = false // forcer une relecture au changement d'onglet
		}
		switch msg.String() {
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
		if l.Level >= v.level {
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
		sb.WriteString(th.Muted.Render("(aucune ligne à ce niveau)"))
	}

	mode := "figé"
	if v.follow {
		mode = "suivi"
	}
	title := fmt.Sprintf("Journal applicatif · niveau ≥ %s · %s · %d/%d lignes",
		v.level.String(), mode, len(filtered), len(lines))
	body := sb.String() + "\n" + th.Muted.Render("fichier : "+v.deps.App.Config.Paths.LogFile())
	return component.Panel(th, title, body, width)
}

func (v *Journal) renderTrades(width, height int) string {
	th := v.deps.Theme
	if !v.tradesFresh {
		v.reloadTrades()
	}
	if v.tradesErr != "" {
		return component.Panel(th, "Trades", th.Negative.Render("journal illisible : "+v.tradesErr), width)
	}
	trades := v.trades

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
	body += "\n" + th.Muted.Render(
		"Ce journal ne contient QUE des exécutions rapportées par une passerelle. "+
			"Aucun trade simulé n'y figure.")
	return component.Panel(th, fmt.Sprintf("Trades exécutés (%d)", len(trades)), body, width)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
