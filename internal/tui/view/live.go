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

	// onlyTradable : la liste ne montre que les paires dimensionnables
	// dans la devise du compte (touche v).
	onlyTradable bool

	// Confirmation d'une connexion à ARGENT RÉEL : le numéro de compte
	// doit être tapé. Pendant la saisie, l'écran prend toutes les touches.
	confirming bool
	typed      string
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
	return &Live{deps: deps, chartTF: tf, tfIndex: idx,
		onlyTradable: deps.App.Config.Risk.RiskPerTradePct > 0}
}

// CapturesKeys : pendant la saisie du numéro de compte, « q » ou « 3 »
// sont des caractères, pas des commandes.
func (v *Live) CapturesKeys() bool { return v.confirming }

// watch : les paires affichées, filtre « tradables » appliqué. Une liste
// que le filtre viderait est montrée entière : un écran vide ne permet
// même pas de voir le problème.
func (v *Live) watch() []live.SymbolState {
	all := v.snapshot.Symbols
	if !v.onlyTradable {
		return all
	}
	out := make([]live.SymbolState, 0, len(all))
	for _, s := range all {
		if tradable(v.deps.App.Config, s.Symbol) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

// current : la paire sélectionnée.
func (v *Live) current() (live.SymbolState, bool) {
	w := v.watch()
	if v.cursor < len(w) {
		return w[v.cursor], true
	}
	return live.SymbolState{}, false
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
		{"v", "tradables / toutes"},
		{"u", "unité de temps"},
	}
}

// needsConfirmation : une connexion engage-t-elle de l'argent réel ?
func (v *Live) needsConfirmation() bool {
	return v.deps.App.Config.Broker.Mode == "live" && !v.snapshot.Simulated
}

// confirmKey traite la saisie du numéro de compte.
func (v *Live) confirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		v.confirming, v.typed = false, ""
		v.deps.Status("connexion LIVE abandonnée")
	case tea.KeyEnter:
		account := strings.TrimSpace(v.typed)
		if account == "" {
			v.deps.Status("tapez le numéro du compte, ou échap pour renoncer")
			return nil
		}
		v.confirming, v.typed = false, ""
		return v.connect(account)
	case tea.KeyBackspace:
		if r := []rune(v.typed); len(r) > 0 {
			v.typed = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		v.typed += string(msg.Runes)
	}
	return nil
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
		if v.confirming {
			return v, v.confirmKey(msg)
		}
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
		case "v":
			cur, _ := v.current()
			v.onlyTradable = !v.onlyTradable
			v.cursor = 0
			for i, s := range v.watch() {
				if s.Symbol == cur.Symbol {
					v.cursor = i
				}
			}
			if v.onlyTradable {
				v.deps.Status("paires " + tradableLabel(v.deps.App.Config) + " seulement")
			} else {
				v.deps.Status("toutes les paires suivies")
			}
		}

	default:
		v.snapshot = v.deps.App.Live.Snapshot()
	}
	return v, nil
}

func (v *Live) moveCursor(delta int) {
	n := len(v.watch())
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
	if v.needsConfirmation() {
		v.confirming, v.typed = true, ""
		v.deps.Status("LIVE — ARGENT RÉEL : tapez le numéro du compte pour confirmer")
		return nil
	}
	return v.connect("")
}

// connect lance la connexion ; account non vide l'impose à la passerelle.
func (v *Live) connect(account string) tea.Cmd {
	v.connecting = true
	v.deps.Status("connexion en cours…")
	app := v.deps.App
	emit := v.deps.Emit
	return func() tea.Msg {
		var err error
		if account != "" {
			err = app.Live.ConnectAs(context.Background(), account)
		} else {
			err = app.Live.Connect(context.Background())
		}
		emit(connectDoneMsg{err: err})
		return nil
	}
}

func (v *Live) toggleSymbol() {
	cur, ok := v.current()
	if !ok {
		return
	}
	sym := cur.Symbol
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
	if v.confirming {
		return v.renderConfirmation(width)
	}
	snap := v.snapshot
	check := v.renderChecklist(width)

	// --- Petits terminaux ------------------------------------------------
	//
	// Sous liveCompactHeight lignes, le panneau Compte (sept lignes au
	// mieux) et le panneau Positions (quatre) ne laissaient plus rien au
	// reste, et Fit coupait la liste des paires. Le compte passe alors sur
	// UNE ligne, et le panneau des positions n'apparaît que s'il y en a.
	if height < liveCompactHeight {
		summary := v.renderAccountLine(width)
		band := height - lipgloss.Height(summary) - lipgloss.Height(check)
		positions := ""
		if len(snap.Positions) > 0 && band-minPositionsHeight >= minCompactBand {
			pos := minInt(positionsHeight(len(snap.Positions)), band-minCompactBand)
			positions = v.renderPositions(width, pos)
			band -= pos
		}
		if band < minCompactBand {
			band = minCompactBand
		}
		parts := []string{summary, v.renderBand(width, band)}
		if positions != "" {
			parts = append(parts, positions)
		}
		parts = append(parts, check)
		return strings.Join(parts, "\n")
	}

	// --- Répartition de la hauteur ------------------------------------
	//
	// On alloue du plus rigide au plus souple, et on MESURE au lieu de
	// deviner : le panneau Compte essaie trois rangées de cartes, puis
	// deux, puis une, et garde la première qui laisse de quoi dessiner le
	// reste. Les cartes écartées restent comptées par la carte « +N ».
	account := component.FitBlock(
		height-minBandHeight-minPositionsHeight-lipgloss.Height(check),
		1, component.DefaultStatRows,
		func(rows int) string { return v.renderAccount(width, rows) })

	free := height - lipgloss.Height(account) - lipgloss.Height(check)
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

	var sb strings.Builder
	sb.WriteString(account)
	sb.WriteString("\n")
	sb.WriteString(v.renderBand(width, mid))
	sb.WriteString("\n")
	sb.WriteString(v.renderPositions(width, pos))
	sb.WriteString("\n")
	sb.WriteString(check)
	return sb.String()
}

// liveCompactHeight : hauteur de corps sous laquelle l'écran passe en
// disposition compacte. Mesurée : la disposition complète demande
// compte (7) + bande (7) + positions (4) + contrôle (1) = 19 lignes.
const (
	liveCompactHeight = 19
	minCompactBand    = 5
)

// renderBand : paires suivies à gauche, graphique à droite.
func (v *Live) renderBand(width, height int) string {
	leftWidth := width / 2
	return lipgloss.JoinHorizontal(lipgloss.Top,
		v.renderWatchlist(leftWidth, height), v.renderChart(width-leftWidth, height))
}

// renderAccountLine : le compte en une ligne, pour les petits terminaux.
// Mêmes règles que le panneau : « — » pour ce qu'on n'a pas.
func (v *Live) renderAccountLine(width int) string {
	th := v.deps.Theme
	snap := v.snapshot
	equity, dayLoss := component.Dash, component.Dash
	if snap.HasAccount {
		equity = component.Num(snap.Account.Equity, 2)
		if pct, ok := snap.Account.DayLossPct(); ok {
			dayLoss = component.Num(pct, 2) + " %"
		}
	}
	line := fmt.Sprintf("Équité %s %s · perte du jour %s (plafond %.1f %%) · %s ordres",
		equity, snap.Account.Currency, dayLoss, v.deps.App.Config.Risk.MaxDailyLossPct,
		component.Count(int(snap.Stats.Orders)))
	if snap.AccountErr != "" {
		return th.Warning.Render(component.Truncate("⚠ "+snap.AccountErr, width))
	}
	return th.Text.Render(component.Truncate(line, width))
}

// checkState : réponse d'un point de contrôle.
type checkState int

const (
	checkUnknown checkState = iota
	checkOK
	checkKO
)

// check : un point de la liste de contrôle.
type check struct {
	long, short string
	state       checkState
	// advisory : informe sans bloquer (le filtre d'actualités sans
	// calendrier ne filtre rien, mais n'empêche pas de trader). Il ne
	// décide jamais du verdict.
	advisory bool
}

// checks : tout ce qui doit être vrai pour qu'une paire trade.
//
// Le moteur en tient déjà le compte, mais le disait en UNE phrase — la
// première condition manquante — et pour aucune paire en particulier.
// « Pourquoi rien ne se passe ? » demandait alors de faire le tour de
// l'entête, de la liste des paires et du journal. La réponse est ici,
// d'un coup d'œil. Ce qui ne se sait qu'à la connexion (modèle,
// historique) reste « ? » avant elle : rien n'est supposé.
func (v *Live) checks(s live.SymbolState) []check {
	snap := v.snapshot
	yes := func(b bool) checkState {
		if b {
			return checkOK
		}
		return checkKO
	}
	afterConnect := func(b bool) checkState {
		if !snap.Connected {
			return checkUnknown
		}
		return yes(b)
	}
	cfg := v.deps.App.Config
	items := []check{
		{"passerelle", "pass.", yes(snap.Connected), false},
		{"barrières", "SL", yes(snap.SupportsBracket), false},
		{"kill-switch", "k-s", yes(snap.KillSwitch), false},
		{"paire armée", "armée", yes(s.Armed), false},
		{"modèle", "modèle", afterConnect(s.ModelLoaded), false},
		{"historique", "hist.", afterConnect(s.HistoryOK), false},
		{"devise " + cfg.Backtest.AccountCurrency, cfg.Backtest.AccountCurrency, yes(tradable(cfg, s.Symbol)), false},
	}
	if snap.NewsActive {
		// Couverture du calendrier à l'instant du marché : sans elle, le
		// filtre d'actualités laisse tout passer.
		items = append(items, check{"calendrier news", "news", afterConnect(snap.NewsCovered), true})
	}
	return items
}

// renderChecklist : une ligne, pour la paire sélectionnée.
func (v *Live) renderChecklist(width int) string {
	th := v.deps.Theme
	s, ok := v.current()
	if !ok {
		return th.Muted.Render("Aucune paire suivie.")
	}
	items := v.checks(s)
	blocked := ""
	for _, c := range items {
		if c.state != checkOK && !c.advisory && blocked == "" {
			blocked = c.long
		}
	}
	verdict := th.Positive.Render("→ peut trader")
	if blocked != "" {
		verdict = th.Warning.Render("→ bloquée : " + blocked)
	}
	// Le VERDICT vient juste après la paire : c'est la réponse, les coches
	// n'en sont que le détail, et c'est la fin de la ligne qu'un terminal
	// étroit rogne.
	for _, short := range []bool{false, true} {
		parts := []string{th.Accent.Render(s.Symbol), verdict}
		for _, c := range items {
			label := c.long
			if short {
				label = c.short
			}
			switch c.state {
			case checkOK:
				parts = append(parts, th.Positive.Render("✓ "+label))
			case checkKO:
				parts = append(parts, th.Negative.Render("✗ "+label))
			default:
				parts = append(parts, th.Muted.Render("? "+label))
			}
		}
		line := strings.Join(parts, "  ")
		if lipgloss.Width(line) <= width || short {
			return component.Clip(line, width)
		}
	}
	return ""
}

// renderConfirmation : la saisie du numéro de compte avant une séance à
// argent réel.
func (v *Live) renderConfirmation(width int) string {
	th := v.deps.Theme
	cfg := v.deps.App.Config
	body := th.Warning.Render("LIVE — ARGENT RÉEL") + "\n\n" +
		"La passerelle « " + cfg.Broker.Name + " » va se connecter en mode live.\n" +
		"Tapez le numéro de votre compte courtier pour confirmer. La connexion\n" +
		"sera refusée si ce n'est pas le compte de la session.\n\n" +
		th.Accent.Render("compte : "+v.typed+"▏") + "\n\n" +
		th.Muted.Render("entrée confirme · échap renonce")
	return component.Panel(th, "Confirmer la connexion", body, width)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	watch := v.watch()
	rows := make([][]string, 0, len(watch))
	for _, s := range watch {
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
			case core.Exit:
				signal = th.Muted.Render(fmt.Sprintf("sortie %.2f", s.Confidence))
			case core.ExitLong:
				signal = th.Muted.Render(fmt.Sprintf("sortie L %.2f", s.Confidence))
			case core.ExitShort:
				signal = th.Muted.Render(fmt.Sprintf("sortie S %.2f", s.Confidence))
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
	title := "Paires suivies"
	if v.onlyTradable && len(watch) < len(v.snapshot.Symbols) {
		title = fmt.Sprintf("Paires tradables %s · %d/%d", v.deps.App.Config.Backtest.AccountCurrency,
			len(watch), len(v.snapshot.Symbols))
	}
	return component.PanelH(th, title, body, width, height)
}

func (v *Live) renderChart(width, height int) string {
	th := v.deps.Theme
	cur, ok := v.current()
	if !ok {
		return component.PanelH(th, "Graphique", th.Muted.Render("aucune paire"), width, height)
	}
	sym := cur.Symbol
	series := v.deps.App.Live.Buffer(sym)
	title := fmt.Sprintf("%s · %s", sym, v.chartTF)

	if len(series) == 0 {
		msg := th.Muted.Render("aucune bougie reçue pour l'instant.\n" +
			"Le graphique se remplit à la connexion de la passerelle.")
		if m := cur.Notice; m != "" {
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
	if notice := cur.Notice; notice != "" {
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
