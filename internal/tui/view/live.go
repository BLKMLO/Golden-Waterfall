package view

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/live"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
)

// connectDoneMsg : résultat d'une tentative de connexion.
type connectDoneMsg struct{ err error }

// Live est l'écran de pilotage temps réel : compte, paires suivies,
// positions, graphique.
//
// Principe tenu partout ici : tout ce qui touche au COMPTE (équité,
// positions, trades) vient de la passerelle ou du journal. Quand la
// donnée manque, la cellule affiche « — ». Aucune valeur n'est estimée
// pour remplir un tableau.
type Live struct {
	deps       Deps
	snapshot   live.Snapshot
	cursor     int
	connecting bool
	chartTF    data.Timeframe
	tfIndex    int

	// Cache du graphique. Sans lui, chaque redessin recopiait le tampon
	// complet du symbole sélectionné puis le ré-échantillonnait — plusieurs
	// fois par seconde, pour des bougies qui, en H4, changent toutes les
	// quatre heures.
	chartKey    chartKey
	chartSeries core.Series
}

// chartKey identifie ce qui rendrait le graphique obsolète : la paire,
// l'unité de temps, et l'état du tampon de bougies.
type chartKey struct {
	symbol    string
	timeframe data.Timeframe
	bars      int
	last      int64
}

// NewLive construit l'écran live.
func NewLive(deps Deps) Model {
	tf, err := data.ParseTimeframe(deps.App.Config.UI.ChartTimeframe)
	if err != nil {
		tf = data.H1
	}
	idx := 0
	for i, t := range data.Timeframes {
		if t == tf {
			idx = i
		}
	}
	return &Live{deps: deps, chartTF: tf, tfIndex: idx}
}

func (v *Live) Title() string { return "Live" }
func (v *Live) Busy() bool    { return v.connecting }

func (v *Live) Init() tea.Cmd {
	v.snapshot = v.deps.App.Live.Snapshot()
	return nil
}

func (v *Live) Keys() [][2]string {
	return [][2]string{
		{"c", "connecter / déconnecter"},
		{"k", "kill-switch global"},
		{"espace", "armer la paire"},
		{"↑↓", "sélection"},
		{"u", "unité de temps"},
	}
}

func (v *Live) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectDoneMsg:
		v.connecting = false
		if msg.err != nil {
			v.deps.Status("connexion impossible : " + msg.err.Error())
		} else {
			v.deps.Status("passerelle connectée")
		}
		v.snapshot = v.deps.App.Live.Snapshot()

	case tea.KeyMsg:
		switch msg.String() {
		// « k » seul est pris par le kill-switch : la sélection se fait
		// aux flèches ou en majuscules, jamais en hjkl ici.
		case "up", "K":
			v.moveCursor(-1)
		case "down", "J":
			v.moveCursor(1)
		case "c":
			return v, v.toggleConnection()
		case "k":
			on := v.deps.App.Live.ToggleKillSwitch()
			if on {
				v.deps.Status("kill-switch ARMÉ — les paires armées peuvent trader")
			} else {
				v.deps.Status("kill-switch DÉSARMÉ — plus aucune entrée")
			}
		case " ":
			v.toggleSymbol()
		case "u":
			v.tfIndex = (v.tfIndex + 1) % len(data.Timeframes)
			v.chartTF = data.Timeframes[v.tfIndex]
			v.deps.Status("graphique en " + string(v.chartTF))
		}

	default:
		v.snapshot = v.deps.App.Live.Snapshot()
	}
	return v, nil
}

func (v *Live) moveCursor(delta int) {
	n := len(v.snapshot.Symbols)
	if n == 0 {
		return
	}
	v.cursor = (v.cursor + delta + n) % n
}

func (v *Live) toggleConnection() tea.Cmd {
	if v.deps.App.Live.Connected() {
		v.deps.App.Live.Disconnect()
		v.deps.Status("passerelle déconnectée")
		return nil
	}
	v.connecting = true
	v.deps.Status("connexion en cours…")
	app := v.deps.App
	emit := v.deps.Emit
	return func() tea.Msg {
		err := app.Live.Connect(context.Background())
		emit(connectDoneMsg{err: err})
		return nil
	}
}

func (v *Live) toggleSymbol() {
	if v.cursor >= len(v.snapshot.Symbols) {
		return
	}
	sym := v.snapshot.Symbols[v.cursor].Symbol
	on, err := v.deps.App.Live.ToggleSymbol(sym)
	if err != nil {
		v.deps.Status(err.Error())
		return
	}
	if on {
		v.deps.Status(sym + " ARMÉE")
	} else {
		v.deps.Status(sym + " désarmée")
	}
}

func (v *Live) Render(width, height int) string {
	th := v.deps.Theme
	snap := v.snapshot

	// « moteur : moteur arrêté » disait deux fois le même mot, et la
	// ligne restait vide tant que le runtime n'avait pas d'instance.
	engine := snap.EngineStatus
	if engine == "" {
		engine = "au repos — aucune passerelle ouverte"
	}
	status := th.Muted.Render("Moteur : " + engine)

	// --- Répartition de la hauteur ------------------------------------
	//
	// Un plancher posé sur la seule bande centrale faisait déborder
	// l'écran entier : en 80×24, la taille de terminal la plus banale qui
	// soit, le panneau Compte étalait ses six cartes sur trois rangées et
	// mangeait onze lignes sur dix-neuf. Un corps plus haut que la
	// fenêtre ne perd pas ses dernières lignes — il pousse l'entête et la
	// barre de raccourcis dehors, donc « q quitter » et le bandeau de
	// mode.
	//
	// On alloue donc du plus rigide au plus souple, et on MESURE au lieu
	// de deviner : le panneau Compte essaie trois rangées de cartes, puis
	// deux, puis une, et garde la première qui laisse de quoi dessiner le
	// reste. Les cartes écartées restent comptées par la carte « +N ».
	account := component.FitBlock(
		height-minBandHeight-minPositionsHeight-lipgloss.Height(status),
		1, component.DefaultStatRows,
		func(rows int) string { return v.renderAccount(width, rows) })

	free := height - lipgloss.Height(account) - lipgloss.Height(status)
	// Les positions ouvertes cèdent AVANT la bande centrale : elles
	// tiennent en quelques lignes, alors que paires suivies et graphique
	// n'ont plus de sens sous une demi-douzaine.
	pos := positionsHeight(len(snap.Positions))
	if room := free - minBandHeight; pos > room {
		pos = room
	}
	if pos < minPositionsHeight {
		pos = minPositionsHeight
	}
	mid := free - pos
	if mid < minPositionsHeight {
		mid = minPositionsHeight
	}
	positions := v.renderPositions(width, pos)

	leftWidth := width / 2
	left := v.renderWatchlist(leftWidth, mid)
	right := v.renderChart(width-leftWidth, mid)

	var sb strings.Builder
	sb.WriteString(account)
	sb.WriteString("\n")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	sb.WriteString("\n")
	sb.WriteString(positions)
	sb.WriteString("\n")
	sb.WriteString(status)
	return sb.String()
}

func (v *Live) renderAccount(width, rows int) string {
	th := v.deps.Theme
	snap := v.snapshot

	equity, margin, dayLoss := component.Dash, component.Dash, component.Dash
	lossStyle := th.Text
	if snap.HasAccount {
		equity = component.Num(snap.Account.Equity, 2)
		margin = component.Num(snap.Account.Margin, 2)
		if pct, ok := snap.Account.DayLossPct(); ok {
			dayLoss = component.Num(pct, 2) + " %"
			if pct >= v.deps.App.Config.Risk.MaxDailyLossPct && v.deps.App.Config.Risk.MaxDailyLossPct > 0 {
				lossStyle = th.Negative
			} else if pct > 0 {
				lossStyle = th.Warning
			} else {
				lossStyle = th.Positive
			}
		}
	}
	note := ""
	if snap.AccountErr != "" {
		note = "donnée périmée"
	}

	cards := []component.StatCard{
		{Label: "Équité", Value: equity, Style: th.Accent, Note: note},
		{Label: "Marge", Value: margin},
		{Label: "Perte du jour", Value: dayLoss, Style: lossStyle,
			Note: fmt.Sprintf("plafond %.1f %%", v.deps.App.Config.Risk.MaxDailyLossPct)},
		{Label: "Ticks", Value: component.Count(int(snap.Stats.Ticks)),
			Note: "dernier " + component.Clock(snap.Stats.LastTick)},
		{Label: "Bougies", Value: component.Count(int(snap.Stats.Bars))},
		{Label: "Ordres", Value: component.Count(int(snap.Stats.Orders)),
			Note: fmt.Sprintf("%d exécutés", snap.Stats.Fills)},
	}
	body := component.StatRowMax(th, cards, component.PanelContent(width), rows)
	if snap.AccountErr != "" {
		body += "\n" + th.Warning.Render("⚠ "+component.Truncate(snap.AccountErr, width-8))
	} else if !snap.HasAccount {
		body += "\n" + th.Muted.Render("Aucune donnée de compte : la passerelle n'est pas connectée. "+
			"Rien n'est estimé à sa place.")
	}
	return component.Panel(th, "Compte", body, width)
}

// minBandHeight : en deçà, la bande « paires suivies + graphique » ne
// montre plus qu'un cadre. minPositionsHeight : un cadre et une ligne.
const (
	minBandHeight      = 7
	minPositionsHeight = 4
)

// positionsHeight dimensionne le panneau des positions : assez pour
// toutes les voir jusqu'à une demi-douzaine, sans dévorer l'écran.
func positionsHeight(n int) int {
	h := n + 4
	if h < 6 {
		h = 6
	}
	if h > 10 {
		h = 10
	}
	return h
}

func (v *Live) renderWatchlist(width, height int) string {
	th := v.deps.Theme
	cols := []component.Column{
		// Priorités : la paire et son état sont le sens de la ligne ; le
		// signal se comprime, la variation puis le prix s'effacent si le
		// panneau est étroit.
		{Title: "Paire", Width: 8},
		{Title: "Bid", Width: 10, Right: true, Priority: 2},
		{Title: "Var.", Width: 8, Right: true, Priority: 3},
		{Title: "État", Width: 9},
		// Largeur calée sur la plus longue valeur possible
		// (« pas de modèle ») : une colonne qui explique un silence n'a
		// aucun intérêt à moitié coupée. Ce sont le prix et la variation
		// qui cèdent la place.
		{Title: "Signal", Width: 13, Flex: true, Min: 8, Priority: 1},
	}
	rows := make([][]string, 0, len(v.snapshot.Symbols))
	for _, s := range v.snapshot.Symbols {
		change := component.Dash
		if s.HasQuote && s.DayOpen > 0 {
			change = component.Pct(s.Change, 2)
			if s.Change >= 0 {
				change = th.Positive.Render(change)
			} else {
				change = th.Negative.Render(change)
			}
		}
		state := th.Muted.Render("arrêtée")
		if s.Armed {
			state = th.Positive.Render("armée")
		}
		if s.InFlight != "" {
			state = th.Warning.Render("ordre")
		}
		signal := component.Dash
		if s.HasSignal {
			switch s.LastSignal {
			case core.EnterLong:
				signal = th.Positive.Render(fmt.Sprintf("LONG %.2f", s.Confidence))
			case core.EnterShort:
				signal = th.Negative.Render(fmt.Sprintf("SHORT %.2f", s.Confidence))
			default:
				signal = th.Muted.Render(fmt.Sprintf("neutre %.2f", s.Confidence))
			}
		} else if !s.ModelLoaded {
			signal = th.Warning.Render("pas de modèle")
		}
		rows = append(rows, []string{
			s.Symbol, component.Price(s.Symbol, s.Bid), change, state, signal,
		})
	}
	body := component.Table(th, cols, rows, v.cursor, height-4, component.PanelContent(width))
	return component.PanelH(th, "Paires suivies", body, width, height)
}

func (v *Live) renderChart(width, height int) string {
	th := v.deps.Theme
	if v.cursor >= len(v.snapshot.Symbols) {
		return component.PanelH(th, "Graphique", th.Muted.Render("aucune paire"), width, height)
	}
	sym := v.snapshot.Symbols[v.cursor].Symbol
	series := v.deps.App.Live.Buffer(sym)
	title := fmt.Sprintf("%s · %s", sym, v.chartTF)

	if len(series) == 0 {
		msg := th.Muted.Render("aucune bougie reçue pour l'instant.\n" +
			"Le graphique se remplit à la connexion de la passerelle.")
		if m := v.snapshot.Symbols[v.cursor].Notice; m != "" {
			msg += "\n\n" + th.Warning.Render(component.Truncate("⚠ "+m, width-6))
		}
		return component.PanelH(th, title, msg, width, height)
	}
	shown := v.chartFor(sym, series)
	lines := component.CandleChart(shown, width-6, height-4, th)
	last := shown[len(shown)-1]
	legend := th.Muted.Render(fmt.Sprintf("O %s  H %s  B %s  C %s  ·  %d bougies",
		component.Price(sym, last.Open()), component.Price(sym, last.High()),
		component.Price(sym, last.Low()), component.Price(sym, last.Close()), len(shown)))
	// L'anomalie de la paire sélectionnée est TOUJOURS visible, pas
	// seulement quand le graphique est vide : c'est elle qui explique un
	// silence de la stratégie.
	if notice := v.snapshot.Symbols[v.cursor].Notice; notice != "" {
		legend = th.Warning.Render(component.Truncate("⚠ "+notice, width-6)) + "\n" + legend
		lines = lines[:maxInt(len(lines)-1, 0)]
	}
	return component.PanelH(th, title, strings.Join(lines, "\n")+"\n"+legend, width, height)
}

// chartFor renvoie la série ré-échantillonnée du symbole, recalculée
// seulement quand quelque chose a réellement changé.
func (v *Live) chartFor(symbol string, series core.Series) core.Series {
	key := chartKey{symbol: symbol, timeframe: v.chartTF, bars: len(series)}
	if len(series) > 0 {
		key.last = series[len(series)-1].Time.UnixNano()
	}
	if key == v.chartKey && v.chartSeries != nil {
		return v.chartSeries
	}
	v.chartKey = key
	v.chartSeries = data.Resample(series, v.chartTF)
	return v.chartSeries
}

func (v *Live) renderPositions(width, height int) string {
	th := v.deps.Theme
	cols := []component.Column{
		{Title: "Paire", Width: 8},
		{Title: "Sens", Width: 6},
		{Title: "Quantité", Width: 10, Right: true, Priority: 2},
		{Title: "Prix moyen", Width: 12, Right: true, Priority: 3},
		{Title: "P&L latent", Width: 12, Right: true},
	}
	rows := make([][]string, 0, len(v.snapshot.Positions))
	for _, p := range v.snapshot.Positions {
		side := th.Positive.Render("LONG")
		if !p.IsLong() {
			side = th.Negative.Render("SHORT")
		}
		// Le courtier ne rapporte pas toujours le P&L latent (IB ne le
		// donne pas dans son flux de positions) : un tiret, pas un zéro.
		pnl := component.Dash
		switch {
		case !p.UnrealizedKnown:
		case p.UnrealizedPnL >= 0:
			pnl = th.Positive.Render(component.Money(p.UnrealizedPnL))
		default:
			pnl = th.Negative.Render(component.Money(p.UnrealizedPnL))
		}
		rows = append(rows, []string{
			p.Symbol, side, component.Num(p.Quantity, 2),
			component.Price(p.Symbol, p.AveragePrice), pnl,
		})
	}
	return component.PanelH(th, "Positions ouvertes (rapportées par la passerelle)",
		component.Table(th, cols, rows, -1, height-4, component.PanelContent(width)), width, height)
}
