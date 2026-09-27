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
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
)

// downloadProgressMsg relaie l'avancement d'un téléchargement.
type downloadProgressMsg data.DownloadProgress

// downloadDoneMsg signale la fin (ou l'échec) d'un téléchargement.
type downloadDoneMsg struct {
	symbol string
	bars   int
	failed int // années en échec (les suivantes ont été tentées)
	err    error
}

// Data est l'écran des données historiques : ce qui est présent en local,
// et ce qu'il reste à télécharger.
//
// Les chiffres affichés proviennent de l'inventaire RÉEL des fichiers
// (en-têtes lus sur disque) : aucune estimation, aucune projection.
type Data struct {
	deps   Deps
	rows   []data.Inventory
	cursor int

	// span : période demandée au prochain téléchargement. Télécharger
	// vingt ans pour éprouver une idée sur un mois n'a aucun sens, et
	// c'est pourtant tout ce que l'écran savait faire.
	span     data.YearRange
	spanEdge int // 0 = borne de début sélectionnée, 1 = borne de fin

	// source : le fournisseur configuré (history.source). Il dit quelles
	// paires il publie et depuis quand — l'écran le montre avant qu'on
	// lance une heure de requêtes vouées au 404.
	source data.Source

	mu        sync.Mutex
	active    bool
	progress  data.DownloadProgress
	lastError string
	cancel    context.CancelFunc
	startedAt time.Time
}

// NewData construit l'écran des données.
func NewData(deps Deps) Model {
	v := &Data{deps: deps, span: data.FullRange(deps.App.Config.History.StartYear)}
	// app.New a déjà refusé une source inconnue : l'erreur ne peut venir
	// que d'une configuration construite à la main (tests).
	v.source, _ = data.NewSource(deps.App.Config.History.Source, data.SourceOptions{})
	return v
}

func (v *Data) Title() string { return "Données" }

func (v *Data) Busy() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.active
}

func (v *Data) Init() tea.Cmd {
	v.refresh()
	return nil
}

func (v *Data) Keys() [][2]string {
	return [][2]string{
		{"d", "télécharger la paire"},
		{"D", "télécharger tout"},
		{"←→", "borne d'année"},
		{"p", "début / fin"},
		{"a", "tout l'historique"},
		{"m", "convertir les .gwb"},
		{"x", "interrompre"},
		{"r", "rafraîchir"},
	}
}

// moveSpan déplace la borne sélectionnée d'une année.
func (v *Data) moveSpan(delta int) {
	if v.spanEdge == 0 {
		v.span.From += delta
	} else {
		v.span.To += delta
	}
	v.span = v.span.Normalize()
}

func (v *Data) refresh() {
	inv, err := data.Catalog(v.deps.App.Config.Paths.HistoryDir())
	if err != nil {
		v.deps.Status("inventaire illisible : " + err.Error())
		return
	}
	// On présente TOUS les instruments configurés, y compris ceux dont
	// rien n'est téléchargé : un tableau qui ne montrerait que le présent
	// masquerait précisément ce qui manque.
	byName := make(map[string]data.Inventory, len(inv))
	for _, i := range inv {
		byName[i.Symbol] = i
	}
	rows := make([]data.Inventory, 0, len(v.deps.App.Config.History.Instruments))
	for _, sym := range v.deps.App.Config.History.Instruments {
		if i, ok := byName[sym]; ok {
			rows = append(rows, i)
			delete(byName, sym)
			continue
		}
		rows = append(rows, data.Inventory{Symbol: sym})
	}
	for _, i := range byName { // téléchargés mais retirés de la config
		rows = append(rows, i)
	}
	v.rows = rows
}

func (v *Data) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case downloadProgressMsg:
		v.mu.Lock()
		v.progress = data.DownloadProgress(msg)
		v.mu.Unlock()

	case downloadDoneMsg:
		v.mu.Lock()
		v.active = false
		v.cancel = nil
		if msg.err != nil {
			v.lastError = msg.err.Error()
		} else {
			v.lastError = ""
		}
		v.mu.Unlock()
		v.refresh()
		switch {
		case msg.failed > 0:
			v.deps.Status(fmt.Sprintf("%s bougies écrites · %d année(s) en échec : %s",
				component.Count(msg.bars), msg.failed, msg.err.Error()))
		case msg.err != nil:
			v.deps.Status("téléchargement interrompu : " + msg.err.Error())
		default:
			v.deps.Status(fmt.Sprintf("%s : %s bougies écrites", msg.symbol, component.Count(msg.bars)))
		}

	case JobDone:
		// Fin d'une conversion : l'inventaire a changé de format.
		if msg.View == "données" {
			v.refresh()
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "K":
			v.move(-1)
		case "down", "J":
			v.move(1)
		case "left", "h":
			v.moveSpan(-1)
		case "right", "l":
			v.moveSpan(1)
		case "p":
			v.spanEdge = 1 - v.spanEdge
		case "a":
			v.span = data.FullRange(v.deps.App.Config.History.StartYear)
			v.deps.Status("période remise à l'historique complet")
		case "m":
			return v, v.migrate()
		case "r":
			v.refresh()
			v.deps.Status("inventaire rafraîchi")
		case "d":
			return v, v.startDownload(false)
		case "D":
			return v, v.startDownload(true)
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

// mixedCount compte les paires dont l'historique vient de plusieurs
// sources.
func (v *Data) mixedCount() int {
	n := 0
	for _, inv := range v.rows {
		if len(inv.Sources) > 1 {
			n++
		}
	}
	return n
}

// legacyCount compte les années restées dans l'ancien format.
func (v *Data) legacyCount() int {
	n := 0
	for _, inv := range v.rows {
		n += len(inv.Legacy)
	}
	return n
}

// migrate convertit les .gwb en Parquet, sans supprimer les originaux.
//
// La suppression reste à la ligne de commande (`gw migrate --remove`) :
// effacer des gigaoctets d'historique est irréversible, et une touche de
// l'interface est trop facile à frapper par mégarde.
func (v *Data) migrate() tea.Cmd {
	if v.legacyCount() == 0 {
		v.deps.Status("aucun fichier .gwb : rien à convertir")
		return nil
	}
	dir := v.deps.App.Config.Paths.HistoryDir()
	emit, status := v.deps.Emit, v.deps.Status
	status("conversion en cours…")
	return func() tea.Msg {
		report, err := data.Migrate(dir, false, nil)
		if err != nil {
			status("conversion impossible : " + err.Error())
		} else if report.Failed > 0 {
			status(fmt.Sprintf("%d converti(s), %d en échec — les originaux sont intacts",
				report.Converted, report.Failed))
		} else {
			status(fmt.Sprintf("%d fichier(s) converti(s) · %.0f Mo → %.0f Mo · "+
				"`gw migrate --remove` supprime les .gwb",
				report.Converted, float64(report.Before)/1e6, float64(report.After)/1e6))
		}
		emit(JobDone{View: "données"})
		return nil
	}
}

func (v *Data) move(delta int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor = (v.cursor + delta + len(v.rows)) % len(v.rows)
}

// startDownload lance le téléchargement en tâche de fond.
func (v *Data) startDownload(all bool) tea.Cmd {
	v.mu.Lock()
	if v.active {
		v.mu.Unlock()
		v.deps.Status("un téléchargement est déjà en cours")
		return nil
	}
	symbols := []string{}
	if all {
		symbols = append(symbols, v.deps.App.Config.History.Instruments...)
	} else if v.cursor < len(v.rows) {
		symbols = append(symbols, v.rows[v.cursor].Symbol)
	}
	var unserved []string
	if v.source != nil {
		symbols, unserved = data.ServedSymbols(v.source, symbols)
	}
	if len(symbols) == 0 {
		v.mu.Unlock()
		if len(unserved) > 0 {
			v.deps.Status(fmt.Sprintf("%s ne publie pas %s", v.sourceLabel(), strings.Join(unserved, " ")))
		}
		return nil
	}
	if len(unserved) > 0 {
		v.deps.Status(fmt.Sprintf("%s ne publie pas %s : paire(s) sautée(s)",
			v.sourceLabel(), strings.Join(unserved, " ")))
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.active, v.cancel, v.startedAt = true, cancel, time.Now()
	v.progress = data.DownloadProgress{Symbol: symbols[0], CurrentStep: "démarrage"}
	v.mu.Unlock()

	cfg := v.deps.App.Config
	logger := v.deps.App.Logger
	emit := v.deps.Emit
	span := v.span.Normalize()

	return func() tea.Msg {
		dl, err := data.NewDownloader(cfg.History.Source, cfg.Paths.HistoryDir(), cfg.History.Concurrency, logger)
		if err != nil {
			emit(downloadDoneMsg{err: err})
			return nil
		}
		first := dl.Source.Info().FirstYear
		total, failed := 0, 0
		endYear := time.Now().UTC().Year()
		var lastErr error
		var lastSymbol string
	loop:
		for _, sym := range symbols {
			lastSymbol = sym
			for _, year := range span.Years() {
				if ctx.Err() != nil {
					lastErr = ctx.Err()
					break loop
				}
				// Une année COMPLÈTE n'est pas retéléchargée. Une année
				// présente mais trouée l'est, ainsi que l'année courante
				// (incomplète par nature). Une année antérieure à la
				// source n'est pas demandée : elle ne rendrait que des 404.
				if year < first || !data.NeedsDownload(cfg.Paths.HistoryDir(), sym, year, endYear) {
					continue
				}
				n, err := dl.DownloadYear(ctx, sym, year, func(p data.DownloadProgress) {
					emit(downloadProgressMsg(p))
				})
				if err != nil {
					if ctx.Err() != nil {
						lastErr = ctx.Err()
						break loop
					}
					// Une année en échec n'arrête pas les suivantes : « D »
					// lancé pour la nuit ne doit pas s'arrêter à la
					// première coupure. L'erreur reste affichée.
					failed++
					lastErr = fmt.Errorf("%s %d : %w", sym, year, err)
					logger.Warn("année non téléchargée", "symbole", sym, "annee", year, "erreur", err)
					continue
				}
				total += n
			}
		}
		emit(downloadDoneMsg{symbol: lastSymbol, bars: total, failed: failed, err: lastErr})
		return nil
	}
}

// sourceLabel : nom lisible de la source configurée.
func (v *Data) sourceLabel() string {
	if v.source == nil {
		return v.deps.App.Config.History.Source
	}
	return v.source.Info().Label
}

func (v *Data) Render(width, height int) string {
	th := v.deps.Theme
	var sb strings.Builder

	header := v.renderHeader(width, false)
	// Petit terminal : la note sur la source passe en version courte
	// plutôt que de laisser le tableau sans une seule ligne.
	if height-lipgloss.Height(header) < dataMinTable {
		header = v.renderHeader(width, true)
	}
	sb.WriteString(header)
	sb.WriteString("\n")

	cols := []component.Column{
		{Title: "Paire", Width: 9},
		{Title: "Classe", Width: 9, Priority: 3},
		{Title: "Années", Width: 14, Priority: 2},
		{Title: "Bougies M1", Width: 14, Right: true, Priority: 1},
		{Title: "Source", Width: 10, Priority: 4},
		{Title: "Manquant", Width: 26, Flex: true, Min: 10},
	}
	endYear := time.Now().UTC().Year()
	startYear := v.deps.App.Config.History.StartYear
	if v.source != nil {
		// Une année que la source ne sert pas n'est pas « à télécharger » :
		// la compter manquante l'y laisserait pour toujours.
		startYear = max(startYear, v.source.Info().FirstYear)
	}
	rows := make([][]string, 0, len(v.rows))
	for _, inv := range v.rows {
		class := component.Dash
		if inst, err := data.LookupInstrument(inv.Symbol); err == nil {
			class = inst.Class
		}
		years := component.Dash
		if len(inv.Years) > 0 {
			years = fmt.Sprintf("%d → %d", inv.Years[0], inv.Years[len(inv.Years)-1])
		}
		bars := component.Dash
		if inv.Bars > 0 {
			bars = component.Count(int(inv.Bars))
		}
		missing := missingSummary(inv.Years, inv.Partial, startYear, endYear)
		style := th.Warning
		if missing == "complet" {
			style = th.Positive
		} else if v.source != nil && !v.source.Serves(inv.Symbol) {
			// Ce qui manque ne viendra pas de la source configurée : le
			// dire ici plutôt qu'au moment où « d » ne fait rien.
			missing = "non publié (" + v.source.Info().Label + ")"
		}
		// La provenance se lit, elle ne se devine pas. Un .gwb reste
		// lisible par ce programme mais par AUCUN autre ; deux sources
		// sur une même paire, ce sont des volumes et des spreads qui ne
		// se comparent pas d'une année à l'autre.
		format := component.Dash
		switch {
		case len(inv.Legacy) > 0:
			format = th.Warning.Render(fmt.Sprintf("%d .gwb", len(inv.Legacy)))
		case len(inv.Sources) > 1:
			format = th.Warning.Render(fmt.Sprintf("%d sources", len(inv.Sources)))
		case len(inv.Sources) == 1:
			format = th.Muted.Render(inv.Sources[0])
		}
		rows = append(rows, []string{inv.Symbol, class, years, bars, format, style.Render(missing)})
	}
	// Tronqué : hors panneau, rien ne borne ces lignes, et un chemin long
	// débordait la largeur du terminal — ce qui décale toute la mise en
	// page, pas seulement cette ligne.
	footer := ""
	if n := v.legacyCount(); n > 0 {
		footer += "\n" + th.Warning.Render(component.Truncate(fmt.Sprintf(
			"⚠ %d année(s) encore au format .gwb, lisible par ce seul programme. "+
				"« m » les convertit en Parquet.", n), width))
	}
	if n := v.mixedCount(); n > 0 {
		footer += "\n" + th.Warning.Render(component.Truncate(fmt.Sprintf(
			"⚠ %d paire(s) mêlent plusieurs sources : volumes et spreads ne se comparent pas "+
				"d'une année à l'autre.", n), width))
	}
	footer += "\n" + th.Muted.Render(component.Truncate(
		"Dossier : "+v.deps.App.Config.Paths.HistoryDir(), width))

	// Hauteur MESURÉE : l'ancien « height − 10 » supposait un entête de
	// taille fixe, qui s'enroule pourtant selon la largeur.
	budget := height - lipgloss.Height(header) - lipgloss.Height(footer) + 1
	sb.WriteString(component.FitBlock(budget, 1, maxInt(budget, 1), func(n int) string {
		return component.Panel(th, "Historique local",
			component.Table(th, cols, rows, v.cursor, n, component.PanelContent(width)), width)
	}))
	sb.WriteString(footer)
	return sb.String()
}

// dataMinTable : cadre, entête et pied de l'historique local, une ligne
// de données, et le chemin du dossier.
const dataMinTable = 7

func (v *Data) renderHeader(width int, compact bool) string {
	th := v.deps.Theme
	v.mu.Lock()
	active, p, lastErr, started := v.active, v.progress, v.lastError, v.startedAt
	v.mu.Unlock()

	if !active {
		// Une seule phrase par ligne, sans coupure manuelle : le panneau
		// habille le texte à la largeur réelle.
		source, short := "Source : "+v.sourceLabel(), "Source : "+v.sourceLabel()
		if v.source != nil {
			info := v.source.Info()
			source += " (history.source) — " + info.Note
			short += fmt.Sprintf(" depuis %d, M1 bid et ask", info.FirstYear)
			if !info.HasVolume {
				short += ", sans volume"
			}
		}
		if compact {
			source = component.Truncate(short+".", component.PanelContent(width))
		}
		body := v.renderSpan(width) + "\n" + th.Muted.Render(source)
		if lastErr != "" {
			body += "\n" + th.Negative.Render("⚠ "+component.Truncate(lastErr, component.PanelContent(width)))
		}
		return component.Panel(th, "Téléchargement", body, width)
	}

	ratio := 0.0
	if p.UnitsTotal > 0 {
		ratio = float64(p.UnitsDone) / float64(p.UnitsTotal)
	}
	unit := p.Unit
	if unit == "" {
		unit = "unités"
	}
	bar := component.ProgressBar(ratio, width-30, th)
	line := fmt.Sprintf("%s %s %d/%d %s", bar, th.Accent.Render(fmt.Sprintf("%3.0f %%", ratio*100)),
		p.UnitsDone, p.UnitsTotal, unit)
	detail := fmt.Sprintf("%s %d sur %s · %s · %s · %s bougies · %d %s sans donnée · %d échecs · %s",
		p.Symbol, p.Year, v.span, v.sourceLabel(), p.CurrentStep, component.Count(p.Bars), p.Skipped, unit,
		p.Failures, component.Duration(time.Since(started)))
	return component.Panel(th, "Téléchargement en cours",
		line+"\n"+th.Muted.Render(component.Truncate(detail, component.PanelContent(width))), width)
}

// renderSpan montre la période demandée, borne sélectionnée mise en
// évidence.
//
// La période est affichée même quand elle vaut l'historique complet :
// c'est elle qui décide de ce que « d » va chercher, et un réglage
// invisible est un réglage qu'on oublie avoir changé.
func (v *Data) renderSpan(width int) string {
	th := v.deps.Theme
	from, to := fmt.Sprintf("%d", v.span.From), fmt.Sprintf("%d", v.span.To)
	if v.spanEdge == 0 {
		from = th.Accent.Bold(true).Render("[" + from + "]")
		to = th.Text.Render(" " + to + " ")
	} else {
		from = th.Text.Render(" " + from + " ")
		to = th.Accent.Bold(true).Render("[" + to + "]")
	}
	note := "période partielle — seules ces années seront demandées"
	style := th.Warning
	if v.span.Covers(data.FullRange(v.deps.App.Config.History.StartYear)) {
		note, style = "historique complet", th.Muted
	}
	return th.Muted.Render("Période : ") + from + th.Muted.Render(" → ") + to +
		"  " + style.Render(component.Truncate(note, component.PanelContent(width)-28))
}

// missingSummary résume ce qu'il reste à télécharger, sans tout énumérer.
//
// Une année PRÉSENTE MAIS TROUÉE compte comme manquante : elle sera
// refaite à la prochaine demande, et l'annoncer comme faite laisserait
// croire à un historique complet qui ne l'est pas.
func missingSummary(present, partial []int, start, end int) string {
	have := make(map[int]bool, len(present))
	for _, y := range present {
		have[y] = true
	}
	for _, y := range partial {
		have[y] = false
	}
	var missing []int
	for y := start; y <= end; y++ {
		if !have[y] {
			missing = append(missing, y)
		}
	}
	if len(missing) == 0 {
		return "complet"
	}
	if len(missing) > 4 {
		return fmt.Sprintf("%d années (%d…%d)", len(missing), missing[0], missing[len(missing)-1])
	}
	parts := make([]string, len(missing))
	for i, y := range missing {
		parts[i] = fmt.Sprintf("%d", y)
	}
	return strings.Join(parts, " ")
}
