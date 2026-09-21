package view

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
)

type trainingProgressMsg training.Progress

type trainingDoneMsg struct {
	result *training.Result
	err    error
	took   time.Duration
}

// Training est l'écran d'entraînement walk-forward.
//
// C'est l'écran le plus important du programme : c'est le seul qui dise
// si une stratégie vaut quelque chose. Les métriques affichées sont
// OUT-OF-SAMPLE, agrégées sur les trades bruts de tous les plis — jamais
// une moyenne de ratios, qui ne correspondrait à aucun portefeuille réel.
type Training struct {
	deps    Deps
	tfIndex int
	folds   int
	runs    []training.RunSummary
	cursor  int
	tab     int // 0 = résultat courant, 1 = historique des runs

	mu       sync.Mutex
	running  bool
	progress training.Progress
	result   *training.Result
	err      string
	took     time.Duration
	cancel   context.CancelFunc
	started  time.Time
}

// NewTraining construit l'écran d'entraînement.
func NewTraining(deps Deps) Model {
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
	v := &Training{deps: deps, tfIndex: idx, folds: deps.App.Config.Training.Folds}
	v.reloadRuns()
	return v
}

func (v *Training) Title() string { return "Entraînement" }

func (v *Training) Busy() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.running
}

func (v *Training) Init() tea.Cmd { return nil }

func (v *Training) Keys() [][2]string {
	return [][2]string{
		{"r", "lancer le walk-forward"},
		{"x", "interrompre"},
		{"u", "unité de temps"},
		{"+/-", "nombre de plis"},
		{"o", "historique / résultat"},
		{"suppr", "supprimer un run"},
	}
}

func (v *Training) timeframe() data.Timeframe { return data.Timeframes[v.tfIndex] }

func (v *Training) reloadRuns() {
	runs, err := training.ListRuns(v.deps.App.Config.Paths.ModelsDir())
	if err != nil {
		return
	}
	v.runs = runs
	if v.cursor >= len(v.runs) {
		v.cursor = 0
	}
}

func (v *Training) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case trainingProgressMsg:
		v.mu.Lock()
		v.progress = training.Progress(msg)
		v.mu.Unlock()

	case trainingDoneMsg:
		v.mu.Lock()
		v.running, v.cancel = false, nil
		v.result, v.took = msg.result, msg.took
		v.err = ""
		if msg.err != nil {
			v.err = msg.err.Error()
		}
		v.mu.Unlock()
		v.reloadRuns()
		if msg.err != nil {
			v.deps.Status("entraînement interrompu : " + msg.err.Error())
		} else if msg.result != nil {
			v.deps.Status(fmt.Sprintf("walk-forward terminé en %s", component.Duration(msg.took)))
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			return v, v.run()
		case "x":
			v.mu.Lock()
			if v.cancel != nil {
				v.cancel()
				v.deps.Status("interruption demandée…")
			}
			v.mu.Unlock()
		case "u":
			v.tfIndex = (v.tfIndex + 1) % len(data.Timeframes)
		case "+", "=":
			if v.folds < 20 {
				v.folds++
			}
		case "-":
			if v.folds > 2 {
				v.folds--
			}
		case "o":
			v.tab = 1 - v.tab
			v.reloadRuns()
		case "up", "K":
			v.move(-1)
		case "down", "J":
			v.move(1)
		case "delete", "backspace":
			v.deleteRun()
		}
	}
	return v, nil
}

func (v *Training) move(delta int) {
	if v.tab != 1 || len(v.runs) == 0 {
		return
	}
	v.cursor = (v.cursor + delta + len(v.runs)) % len(v.runs)
}

func (v *Training) deleteRun() {
	if v.tab != 1 || v.cursor >= len(v.runs) {
		return
	}
	run := v.runs[v.cursor]
	if err := training.DeleteRun(v.deps.App.Config.Paths.ModelsDir(), run.Dir); err != nil {
		v.deps.Status("suppression refusée : " + err.Error())
		return
	}
	v.deps.Status("run " + run.RunID + " supprimé")
	v.reloadRuns()
}

func (v *Training) run() tea.Cmd {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		v.deps.Status("un entraînement est déjà en cours")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.running, v.cancel, v.result, v.err = true, cancel, nil, ""
	v.started = time.Now()
	v.progress = training.Progress{Phase: "démarrage"}
	tf := v.timeframe()
	folds := v.folds
	v.mu.Unlock()

	a := v.deps.App
	emit := v.deps.Emit
	v.deps.Status(fmt.Sprintf("walk-forward %d plis en %s…", folds, tf))

	return func() tea.Msg {
		started := time.Now()
		defer cancel()
		res, err := a.Training.Run(ctx, training.Request{
			Strategy:   a.Config.Strategy.Name,
			Symbols:    a.Config.History.Instruments,
			Timeframe:  tf,
			Folds:      folds,
			Seed:       a.Config.Training.Seed,
			Workers:    a.Config.Training.Workers,
			TrainFinal: true,
			Progress: func(p training.Progress) {
				emit(trainingProgressMsg(p))
			},
		})
		emit(trainingDoneMsg{result: res, err: err, took: time.Since(started)})
		return nil
	}
}

func (v *Training) Render(width, height int) string {
	th := v.deps.Theme
	var sb strings.Builder

	sb.WriteString(v.renderHeader(width))
	sb.WriteString("\n")

	if v.tab == 1 {
		sb.WriteString(v.renderRuns(width, height-10))
		return sb.String()
	}

	v.mu.Lock()
	result, errText, took := v.result, v.err, v.took
	v.mu.Unlock()

	switch {
	case errText != "":
		sb.WriteString(component.Panel(th, "Résultat", th.Negative.Render(wrap(errText, width-6)), width))
		return sb.String()
	case result == nil:
		sb.WriteString(component.Panel(th, "Résultat", th.Muted.Render(
			"Aucun walk-forward dans cette session.\n\n"+
				"Principe : la SECONDE MOITIÉ de l'historique est découpée en blocs de test\n"+
				"consécutifs. Chaque pli s'entraîne sur tout ce qui précède son bloc, puis est\n"+
				"évalué dessus. Rien n'est jamais testé sur des données vues à l'entraînement.\n\n"+
				"Un modèle de PRODUCTION est ensuite entraîné sur tout l'historique : c'est lui\n"+
				"qui part en live, et les plis disent s'il mérite qu'on l'y envoie.\n\n"+
				"o affiche les entraînements déjà archivés."), width))
		return sb.String()
	}

	aggregate := v.renderAggregate(result, took, width)
	sb.WriteString(aggregate)
	sb.WriteString("\n")
	chartHeight := height - lipgloss.Height(aggregate) - lipgloss.Height(v.renderHeader(width)) - 2
	if chartHeight < 6 {
		chartHeight = 6
	}
	leftWidth := width / 2
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		v.renderFolds(result, leftWidth, chartHeight),
		v.renderEquity(result, width-leftWidth, chartHeight)))
	return sb.String()
}

func (v *Training) renderHeader(width int) string {
	th := v.deps.Theme
	v.mu.Lock()
	running, p, started := v.running, v.progress, v.started
	v.mu.Unlock()

	if !running {
		body := component.StatRow(th, []component.StatCard{
			{Label: "Stratégie", Value: v.deps.App.Config.Strategy.Name, Style: th.Accent},
			{Label: "Unité de temps", Value: string(v.timeframe())},
			{Label: "Plis", Value: fmt.Sprintf("%d", v.folds)},
			{Label: "Paires", Value: fmt.Sprintf("%d", len(v.deps.App.Config.History.Instruments))},
			{Label: "Graine", Value: fmt.Sprintf("%d", v.deps.App.Config.Training.Seed),
				Note: "reproductible"},
		}, width-4)
		return component.Panel(th, "Paramètres", body, width)
	}

	bar := component.ProgressBar(p.Ratio, width-30, th)
	line := fmt.Sprintf("%s %s  %s", bar, th.Accent.Render(fmt.Sprintf("%3.0f %%", p.Ratio*100)),
		component.Duration(time.Since(started)))
	detail := p.Phase
	if p.Folds > 0 {
		detail += fmt.Sprintf(" %d/%d", p.Fold, p.Folds)
	}
	if p.Message != "" {
		detail += " · " + p.Message
	}
	return component.Panel(th, "Walk-forward en cours",
		line+"\n"+th.Muted.Render(component.Truncate(detail, width-6)), width)
}

func (v *Training) renderAggregate(res *training.Result, took time.Duration, width int) string {
	th := v.deps.Theme
	s := res.Aggregate
	pnlStyle := th.Positive
	if s.NetPnL < 0 {
		pnlStyle = th.Negative
	}
	auc := component.Dash
	aucStyle := th.Text
	if res.HasMeanAUC {
		auc = component.Num(res.MeanOOSAUC, 3)
		switch {
		case res.MeanOOSAUC >= 0.55:
			aucStyle = th.Positive
		case res.MeanOOSAUC <= 0.52:
			// Autour de 0,50, le modèle ne classe pas mieux qu'un tirage
			// au sort. Le dire franchement évite bien des illusions.
			aucStyle = th.Negative
		default:
			aucStyle = th.Warning
		}
	}
	cards := []component.StatCard{
		{Label: "AUC out-of-sample", Value: auc, Style: aucStyle, Note: "0,50 = hasard"},
		{Label: "P&L net OOS " + s.Currency, Value: component.Money(s.NetPnL), Style: pnlStyle},
		{Label: "Trades OOS", Value: component.Count(s.Trades)},
		{Label: "Taux de gain", Value: component.Num(s.WinRate, 1) + " %"},
		{Label: "Profit factor", Value: component.Ratio(s.ProfitFactor)},
		{Label: "SQN", Value: component.Ratio(s.SQN)},
		{Label: "Coûts", Value: component.Num(s.Costs, 2)},
		{Label: "Durée", Value: component.Duration(took)},
	}
	body := component.StatRow(th, cards, component.PanelContent(width))
	note := fmt.Sprintf("run %s · %d plis · graine %d", res.RunID, len(res.Folds), res.Seed)
	if res.FinalDir != "" {
		note += " · modèle de production écrit"
	} else {
		note += " · " + "PAS de modèle de production"
	}
	body += "\n" + th.Muted.Render(component.Truncate(note, width-6))
	if !s.CostsModelled {
		body += "\n" + th.Warning.Render("⚠ Un actif au moins n'a pas de côté ask : les coûts sont incomplets.")
	}
	if !s.CurrencyExact {
		body += "\n" + th.Warning.Render(component.Truncate(
			"⚠ Certains actifs ne sont pas convertibles vers la devise du compte sans taux tiers : "+
				"la somme des P&L mélange des devises. Les ratios (taux de gain, AUC) restent exacts.",
			width-6))
	}
	if s.RejectedOrders > 0 {
		body += "\n" + th.Warning.Render(fmt.Sprintf(
			"⚠ %d ordre(s) refusé(s) faute de marge : ce ne sont PAS des abstentions du modèle.",
			s.RejectedOrders))
	}
	return component.Panel(th, "Agrégat OUT-OF-SAMPLE", body, width)
}

func (v *Training) renderFolds(res *training.Result, width, height int) string {
	th := v.deps.Theme
	cols := []component.Column{
		{Title: "Pli", Width: 4, Right: true},
		{Title: "Test depuis", Width: 12, Priority: 2},
		{Title: "Trades", Width: 7, Right: true, Priority: 1},
		{Title: "P&L", Width: 11, Right: true},
		{Title: "AUC", Width: 7, Right: true},
	}
	rows := make([][]string, 0, len(res.Folds))
	for _, f := range res.Folds {
		pnl := component.Money(f.Stats.NetPnL)
		if f.Stats.NetPnL >= 0 {
			pnl = th.Positive.Render(pnl)
		} else {
			pnl = th.Negative.Render(pnl)
		}
		auc := component.Dash
		if f.HasOOSAUC {
			auc = component.Num(f.OOSAUC, 3)
		}
		label := f.TestStart.Format("2006-01-02")
		if f.Err != "" {
			label = th.Negative.Render("échec")
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", f.Index), label,
			component.Count(f.Stats.Trades), pnl, auc,
		})
	}
	return component.PanelH(th, "Plis",
		component.Table(th, cols, rows, -1, height-4, component.PanelContent(width)), width, height)
}

func (v *Training) renderEquity(res *training.Result, width, height int) string {
	th := v.deps.Theme
	values := make([]float64, len(res.Equity))
	for i, p := range res.Equity {
		values[i] = p.Value
	}
	style := th.Positive
	if res.Aggregate.NetPnL < 0 {
		style = th.Negative
	}
	lines := component.LineChart(values, width-6, height-4, style)
	return component.PanelH(th, "Équité out-of-sample (tous actifs)",
		strings.Join(lines, "\n")+"\n"+
			th.Muted.Render("somme des P&L cumulés par actif sur la timeline commune"),
		width, height)
}

func (v *Training) renderRuns(width, height int) string {
	th := v.deps.Theme
	cols := []component.Column{
		{Title: "Run", Width: 16},
		{Title: "Stratégie", Width: 16, Flex: true, Min: 8, Priority: 4},
		{Title: "TF", Width: 4, Priority: 5},
		{Title: "Paires", Width: 7, Right: true, Priority: 3},
		{Title: "Trades", Width: 8, Right: true, Priority: 2},
		{Title: "AUC OOS", Width: 9, Right: true},
		{Title: "P&L", Width: 12, Right: true},
		{Title: "Modèle", Width: 9, Priority: 1},
	}
	rows := make([][]string, 0, len(v.runs))
	for _, r := range v.runs {
		auc := component.Dash
		if r.HasAUC {
			auc = component.Num(r.MeanOOSAUC, 3)
		}
		pnl := component.Money(r.NetPnL)
		if r.NetPnL >= 0 {
			pnl = th.Positive.Render(pnl)
		} else {
			pnl = th.Negative.Render(pnl)
		}
		model := th.Muted.Render("aucun")
		if r.FinalDir != "" {
			model = th.Positive.Render("production")
		}
		rows = append(rows, []string{
			r.RunID, r.Strategy, r.Timeframe, fmt.Sprintf("%d", len(r.Symbols)),
			component.Count(r.Trades), auc, pnl, model,
		})
	}
	body := component.Table(th, cols, rows, v.cursor, height-5, component.PanelContent(width))
	body += "\n" + th.Muted.Render("o revient au résultat courant · suppr efface le run sélectionné")
	return component.PanelH(th, "Entraînements archivés", body, width, height)
}
