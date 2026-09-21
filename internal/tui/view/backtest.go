package view

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/feature"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
)

type backtestDoneMsg struct {
	result *backtest.Result
	err    error
	took   time.Duration
}

// Backtest est l'écran de rejeu manuel d'une stratégie sur une paire.
//
// Il utilise le modèle de PRODUCTION du dernier entraînement (celui qui
// partirait en live). Sans modèle, il refuse de lancer et DIT pourquoi :
// un backtest sans modèle donnerait zéro trade, ce qu'on prendrait à tort
// pour « la stratégie ne trouve rien ».
type Backtest struct {
	deps    Deps
	symbols []string
	cursor  int
	tfIndex int

	mu      sync.Mutex
	running bool
	result  *backtest.Result
	err     string
	took    time.Duration
	cancel  context.CancelFunc
	rows    int
}

// NewBacktest construit l'écran de backtest.
func NewBacktest(deps Deps) Model {
	tf, err := data.ParseTimeframe(deps.App.Config.Training.Timeframe)
	if err != nil {
		tf = data.H4
	}
	idx := 0
	for i, t := range data.Timeframes {
		if t == tf {
			idx = i
		}
	}
	return &Backtest{
		deps:    deps,
		symbols: append([]string(nil), deps.App.Config.History.Instruments...),
		tfIndex: idx,
	}
}

func (v *Backtest) Title() string { return "Backtest" }

func (v *Backtest) Busy() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.running
}

func (v *Backtest) Init() tea.Cmd { return nil }

func (v *Backtest) Keys() [][2]string {
	return [][2]string{
		{"r", "lancer"},
		{"x", "interrompre"},
		{"u", "unité de temps"},
		{"↑↓", "paire"},
	}
}

func (v *Backtest) timeframe() data.Timeframe { return data.Timeframes[v.tfIndex] }

func (v *Backtest) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case backtestDoneMsg:
		v.mu.Lock()
		v.running, v.cancel = false, nil
		v.result, v.took = msg.result, msg.took
		v.err = ""
		if msg.err != nil {
			v.err = msg.err.Error()
		}
		v.mu.Unlock()
		if msg.err != nil {
			v.deps.Status("backtest en échec : " + msg.err.Error())
		} else if msg.result != nil {
			v.deps.Status(fmt.Sprintf("backtest terminé : %d trades en %s",
				msg.result.Stats.Trades, component.Duration(msg.took)))
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "K":
			v.move(-1)
		case "down", "J":
			v.move(1)
		case "u":
			v.tfIndex = (v.tfIndex + 1) % len(data.Timeframes)
		case "r":
			return v, v.run()
		case "x":
			v.mu.Lock()
			if v.cancel != nil {
				v.cancel()
				v.deps.Status("interruption demandée…")
			}
			v.mu.Unlock()
		}
	}
	return v, nil
}

func (v *Backtest) move(delta int) {
	if len(v.symbols) == 0 {
		return
	}
	v.cursor = (v.cursor + delta + len(v.symbols)) % len(v.symbols)
}

func (v *Backtest) run() tea.Cmd {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return nil
	}
	if v.cursor >= len(v.symbols) {
		v.mu.Unlock()
		return nil
	}
	symbol := v.symbols[v.cursor]
	tf := v.timeframe()
	ctx, cancel := context.WithCancel(context.Background())
	v.running, v.cancel, v.result, v.err = true, cancel, nil, ""
	v.mu.Unlock()

	v.deps.Status(fmt.Sprintf("backtest %s en %s…", symbol, tf))
	a := v.deps.App
	emit := v.deps.Emit

	return func() tea.Msg {
		started := time.Now()
		defer cancel()

		strategyName := a.Config.Strategy.Name
		modelDir, why := training.SelectModel(a.Config.Paths.ModelsDir(), strategyName, symbol)
		if modelDir == "" {
			emit(backtestDoneMsg{err: fmt.Errorf(
				"aucun modèle utilisable pour %s : %s. Lancer un entraînement (écran Entraînement) d'abord",
				symbol, why)})
			return nil
		}
		raw, err := data.Load(a.Config.Paths.HistoryDir(), symbol, time.Time{}, time.Time{})
		if err != nil {
			emit(backtestDoneMsg{err: err})
			return nil
		}
		series := data.Resample(raw, tf)
		if len(series) <= feature.ContextBars+10 {
			emit(backtestDoneMsg{err: fmt.Errorf(
				"%s : seulement %d bougies en %s, il en faut plus que le contexte de chauffe (%d)",
				symbol, len(series), tf, feature.ContextBars)})
			return nil
		}
		strat, err := strategy.New(strategyName)
		if err != nil {
			emit(backtestDoneMsg{err: err})
			return nil
		}
		defer strat.Shutdown()
		if err := strat.Warmup(ctx, strategy.WarmupRequest{
			Symbol: symbol, Series: series, Timeframe: tf, ModelDir: modelDir,
		}); err != nil {
			emit(backtestDoneMsg{err: err})
			return nil
		}
		res, err := a.Backtest.Run(ctx, backtest.Request{
			Symbol: symbol, Series: series, From: feature.ContextBars,
			Strategy: strat, Timeframe: tf,
		})
		emit(backtestDoneMsg{result: res, err: err, took: time.Since(started)})
		return nil
	}
}

func (v *Backtest) Render(width, height int) string {
	th := v.deps.Theme
	v.mu.Lock()
	running, result, errText, took := v.running, v.result, v.err, v.took
	v.mu.Unlock()

	var sb strings.Builder

	// --- Paramètres ---
	symbol := component.Dash
	if v.cursor < len(v.symbols) {
		symbol = v.symbols[v.cursor]
	}
	params := component.StatRow(th, []component.StatCard{
		{Label: "Paire", Value: symbol, Style: th.Accent},
		{Label: "Unité de temps", Value: string(v.timeframe())},
		{Label: "Stratégie", Value: v.deps.App.Config.Strategy.Name},
		{Label: "Capital", Value: component.Num(v.deps.App.Config.Backtest.InitialCapital, 0)},
		{Label: "Levier", Value: component.Num(v.deps.App.Config.Backtest.Leverage, 0) + "×"},
		{Label: "Taille", Value: component.Num(v.deps.App.Config.Risk.MaxPositionSize, 2)},
	}, width-4)
	sb.WriteString(component.Panel(th, "Paramètres", params, width))
	sb.WriteString("\n")

	switch {
	case running:
		sb.WriteString(component.Panel(th, "En cours",
			th.Accent.Render("⣿ rejeu en cours…")+"\n"+
				th.Muted.Render("x pour interrompre"), width))
		return sb.String()
	case errText != "":
		sb.WriteString(component.Panel(th, "Résultat",
			th.Negative.Render(wrap(errText, width-6)), width))
		return sb.String()
	case result == nil:
		sb.WriteString(component.Panel(th, "Résultat", th.Muted.Render(
			"Aucun backtest lancé.\n\n"+
				"r lance le rejeu de la paire sélectionnée avec le MODÈLE DE PRODUCTION du dernier\n"+
				"entraînement. Les coûts (spread mesuré + commission) sont appliqués ; le spread\n"+
				"est la médiane de ask_close − bid_close observée dans les données.\n\n"+
				"À quoi cet écran sert VRAIMENT : inspecter le comportement du modèle (rythme des\n"+
				"trades, motifs de sortie, allure de la courbe). Pas à juger sa performance — il\n"+
				"a été entraîné sur cette période. Pour juger, c'est l'écran Entraînement."), width))
		return sb.String()
	}

	stats := v.renderStats(result, took, width)
	sb.WriteString(stats)
	sb.WriteString("\n")

	chartHeight := height - lipgloss.Height(stats) - lipgloss.Height(component.Panel(th, "Paramètres", params, width)) - 2
	if chartHeight < 6 {
		chartHeight = 6
	}
	leftWidth := width / 2
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		v.renderEquity(result, leftWidth, chartHeight),
		v.renderTrades(result, width-leftWidth, chartHeight)))
	return sb.String()
}

func (v *Backtest) renderStats(res *backtest.Result, took time.Duration, width int) string {
	th := v.deps.Theme
	s := res.Stats
	pnlStyle := th.Positive
	if s.NetPnL < 0 {
		pnlStyle = th.Negative
	}
	costNote := "spread mesuré"
	costStyle := th.Text
	if !s.CostsModelled {
		costNote = "AUCUN spread modélisé"
		costStyle = th.Warning
	}
	cards := []component.StatCard{
		{Label: "P&L net " + s.Currency, Value: component.Money(s.NetPnL), Style: pnlStyle},
		{Label: "Rendement", Value: component.Pct(s.ReturnPct, 2), Style: pnlStyle},
		{Label: "Trades", Value: component.Count(s.Trades),
			Note: fmt.Sprintf("%d gagnants", s.Wins)},
		{Label: "Taux de gain", Value: component.Num(s.WinRate, 1) + " %"},
		{Label: "Profit factor", Value: component.Ratio(s.ProfitFactor)},
		{Label: "Drawdown max", Value: component.Num(s.MaxDrawdownPct, 2) + " %", Style: th.Negative},
		{Label: "Sharpe", Value: component.Ratio(s.Sharpe)},
		{Label: "SQN", Value: component.Ratio(s.SQN)},
		{Label: "Coûts", Value: component.Num(s.Costs, 2), Style: costStyle, Note: costNote},
	}
	body := component.StatRow(th, cards, width-4)
	extra := fmt.Sprintf("%s → %s · %s bougies · calcul %s",
		component.Time(s.Start), component.Time(s.End), component.Count(s.Bars),
		component.Duration(took))
	if s.RejectedOrders > 0 {
		extra += fmt.Sprintf(" · %d ordre(s) refusé(s) faute de marge", s.RejectedOrders)
	}
	body += "\n" + th.Muted.Render(component.Truncate(extra, width-6))
	if !s.CostsModelled {
		body += "\n" + th.Warning.Render(
			"⚠ L'historique n'a pas de côté ask : AUCUN coût de transaction n'est modélisé. "+
				"Le résultat est donc optimiste.")
	}
	if !s.CurrencyExact {
		body += "\n" + th.Warning.Render(component.Truncate(fmt.Sprintf(
			"⚠ Montants exprimés en %s, NON convertis vers %s : cette paire exigerait un taux tiers.",
			s.Currency, v.deps.App.Config.Backtest.AccountCurrency), width-6))
	}
	// Avertissement PERMANENT, pas conditionnel : le modèle de production
	// est entraîné sur tout l'historique, cette période comprise. Ce rejeu
	// est donc un examen dont le modèle a vu le corrigé. Sans ce rappel,
	// un taux de gain flatteur se prendrait pour une performance.
	body += "\n" + th.Warning.Render(component.Truncate(
		"⚠ Rejeu IN-SAMPLE : le modèle de production connaît cette période. "+
			"Le chiffre honnête est l'agrégat out-of-sample de l'écran Entraînement.", width-6))
	return component.Panel(th, "Résultat", body, width)
}

func (v *Backtest) renderEquity(res *backtest.Result, width, height int) string {
	th := v.deps.Theme
	values := make([]float64, len(res.Equity))
	for i, p := range res.Equity {
		values[i] = p.Value
	}
	style := th.Positive
	if res.Stats.NetPnL < 0 {
		style = th.Negative
	}
	lines := component.LineChart(values, width-6, height-4, style)
	legend := component.Dash
	if len(res.Equity) > 0 {
		legend = fmt.Sprintf("%s → %s",
			component.Num(res.Equity[0].Value, 0),
			component.Num(res.Equity[len(res.Equity)-1].Value, 0))
	}
	return component.PanelH(th, "Valeur du compte",
		strings.Join(lines, "\n")+"\n"+th.Muted.Render(legend), width, height)
}

func (v *Backtest) renderTrades(res *backtest.Result, width, height int) string {
	th := v.deps.Theme
	cols := []component.Column{
		{Title: "Entrée", Width: 16},
		{Title: "Sens", Width: 6},
		{Title: "P&L", Width: 11, Right: true},
		{Title: "Sortie", Width: 8},
	}
	rows := make([][]string, 0, len(res.Trades))
	// Les plus récents d'abord : c'est la fin du backtest qui intéresse.
	for i := len(res.Trades) - 1; i >= 0; i-- {
		t := res.Trades[i]
		side := th.Positive.Render("LONG")
		if t.Side != "BUY" {
			side = th.Negative.Render("SHORT")
		}
		pnl := component.Money(t.PnL)
		if t.PnL >= 0 {
			pnl = th.Positive.Render(pnl)
		} else {
			pnl = th.Negative.Render(pnl)
		}
		rows = append(rows, []string{component.Time(t.EntryTime), side, pnl, t.ExitReason})
	}
	return component.PanelH(th, fmt.Sprintf("Trades (%d)", len(res.Trades)),
		component.Table(th, cols, rows, -1, height-4), width, height)
}

// wrap coupe un texte long à la largeur donnée.
func wrap(s string, width int) string {
	if width < 10 {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if len(cur)+len(w)+1 > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}
