package view

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// Settings est l'écran de configuration.
//
// Il travaille sur un BROUILLON, jamais sur la configuration vivante :
// le gestionnaire de risque, le moteur live et le moteur de backtest ont
// reçu la leur au démarrage et ne la relisent pas. Laisser croire qu'un
// réglage modifié ici s'applique tout de suite serait le mensonge le plus
// coûteux de toute l'interface — on écrit dans config.yaml, et l'écran
// répète que cela prend effet au PROCHAIN démarrage.
//
// Deux autres refus, de la même famille :
//
//   - un réglage forcé par une variable GW_* est affiché comme tel et
//     n'est pas modifiable : l'environnement a le dernier mot au
//     démarrage, donc l'éditer ici ne changerait rien ;
//   - la sauvegarde passe par config.Validate() et REFUSE d'écrire un
//     fichier que le programme refuserait de relire.
type Settings struct {
	deps   Deps
	draft  config.Config
	fields []settingField
	cursor int

	editing bool   // saisie libre en cours
	buffer  string // texte en cours de saisie
	// picker : sélecteur de paires ouvert par-dessus l'écran. Une liste
	// de trente et une paires ne se modifie pas dans un champ de texte.
	picker  *SymbolPicker
	confirm string // chemin du réglage en attente de confirmation
	invalid string // message de validation, s'il y en a un
	dirty   bool
}

// NewSettings construit l'écran des paramètres.
func NewSettings(deps Deps) Model {
	v := &Settings{deps: deps, draft: deps.App.Config}
	v.fields = settingsFields()
	return v
}

func (v *Settings) Title() string { return "Paramètres" }
func (v *Settings) Busy() bool    { return false }
func (v *Settings) Init() tea.Cmd { return nil }

func (v *Settings) Keys() [][2]string {
	if v.picker != nil {
		return v.picker.Keys()
	}
	if v.editing {
		return [][2]string{{"entrée", "valider"}, {"échap", "annuler"}}
	}
	return [][2]string{
		{"↑↓", "réglage"},
		{"←→", "modifier"},
		{"entrée", "saisir"},
		{"d", "défaut"},
		{"s", "enregistrer"},
		{"r", "recharger"},
	}
}

// --- Champs -------------------------------------------------------------

type settingKind int

const (
	kindEnum settingKind = iota
	kindNumber
	kindText
	kindBool
	kindList    // liste libre, séparée par des virgules
	kindSymbols // liste de PAIRES, éditée par le sélecteur
)

// settingField décrit UN réglage : comment le lire, comment l'écrire, et
// surtout ce qu'il change. L'aide n'est pas un luxe : un plafond de risque
// mal compris est un plafond mal réglé.
type settingField struct {
	Section string
	Path    string // clé YAML, et clé de repérage d'une surcharge GW_*
	Label   string
	Help    string
	Kind    settingKind
	Choices func() []string
	Step    float64
	Digits  int
	// Danger : le réglage engage de l'argent réel et demande une
	// confirmation explicite.
	Danger bool
	Get    func(*config.Config) string
	Set    func(*config.Config, string) error
}

func setFloat(dst *float64, raw string) error {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("nombre attendu, reçu %q", raw)
	}
	*dst = v
	return nil
}

func setInt(dst *int, raw string) error {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("entier attendu, reçu %q", raw)
	}
	*dst = v
	return nil
}

func timeframeNames() []string {
	out := make([]string, len(data.Timeframes))
	for i, tf := range data.Timeframes {
		out[i] = string(tf)
	}
	return out
}

func instrumentNames() []string {
	out := data.SupportedSymbols()
	sort.Strings(out)
	return out
}

// settingsFields est la LISTE DE RÉFÉRENCE des réglages exposés.
//
// Chaque entrée dit son chemin YAML : c'est ce qui permet de retrouver le
// même réglage dans config.yaml, dans la documentation et dans un message
// d'erreur de validation.
func settingsFields() []settingField {
	return []settingField{
		// --- Compte ---
		{
			Section: "Compte", Path: "broker.name", Label: "Passerelle", Kind: kindEnum,
			Help:    "Qui exécute les ordres. « replay » rejoue l'historique local sur un compte fictif.",
			Choices: func() []string { return broker.ListNames() },
			Get:     func(c *config.Config) string { return c.Broker.Name },
			Set:     func(c *config.Config, s string) error { c.Broker.Name = s; return nil },
		},
		{
			Section: "Compte", Path: "broker.mode", Label: "Mode", Kind: kindEnum, Danger: true,
			Help:    "« live » engage de l'ARGENT RÉEL. C'est le seul réglage qui fasse la différence.",
			Choices: func() []string { return []string{"paper", "live"} },
			Get:     func(c *config.Config) string { return c.Broker.Mode },
			Set:     func(c *config.Config, s string) error { c.Broker.Mode = s; return nil },
		},
		{
			Section: "Compte", Path: "broker.host", Label: "Hôte", Kind: kindText,
			Help: "Adresse du terminal courtier (TWS, IB Gateway). Sans objet pour le rejeu.",
			Get:  func(c *config.Config) string { return c.Broker.Host },
			Set:  func(c *config.Config, s string) error { c.Broker.Host = s; return nil },
		},
		{
			Section: "Compte", Path: "broker.port", Label: "Port", Kind: kindNumber, Step: 1,
			Help: "Port d'écoute du terminal courtier. TWS : 7497 en papier, 7496 en réel.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Broker.Port) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Broker.Port, s) },
		},
		{
			Section: "Compte", Path: "broker.client_id", Label: "Client ID (IB)", Kind: kindNumber, Step: 1,
			Help: "Interactive Brokers : identifiant de connexion. Unique par programme connecté au même TWS.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Broker.ClientID) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Broker.ClientID, s) },
		},
		{
			Section: "Compte", Path: "broker.account", Label: "Compte (IB)", Kind: kindText,
			Help: "Interactive Brokers : compte à utiliser si la session TWS en gère plusieurs. Vide = le seul compte.",
			Get:  func(c *config.Config) string { return c.Broker.Account },
			Set:  func(c *config.Config, s string) error { c.Broker.Account = strings.TrimSpace(s); return nil },
		},
		{
			Section: "Compte", Path: "backtest.account_currency", Label: "Devise du compte", Kind: kindEnum,
			Help:    "Devise dans laquelle sont exprimés capital, P&L et budget de risque. Elle doit être celle du compte réel.",
			Choices: func() []string { return []string{"USD", "EUR", "GBP", "CHF", "JPY", "CAD", "AUD", "NZD"} },
			Get:     func(c *config.Config) string { return c.Backtest.AccountCurrency },
			Set:     func(c *config.Config, s string) error { c.Backtest.AccountCurrency = s; return nil },
		},
		{
			Section: "Compte", Path: "backtest.initial_capital", Label: "Capital de départ", Kind: kindNumber, Step: 1000, Digits: 2,
			Help: "Capital du compte SIMULÉ : backtest et rejeu. N'a aucun effet sur un compte réel.",
			Get:  func(c *config.Config) string { return component.Num(c.Backtest.InitialCapital, 2) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Backtest.InitialCapital, s) },
		},
		{
			Section: "Compte", Path: "backtest.leverage", Label: "Levier", Kind: kindNumber, Step: 1, Digits: 0,
			Help: "Une entrée immobilise notionnel / levier. 30 = plafond retail ESMA.",
			Get:  func(c *config.Config) string { return component.Num(c.Backtest.Leverage, 0) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Backtest.Leverage, s) },
		},
		{
			Section: "Compte", Path: "broker.timeframe", Label: "Unité de temps (live)", Kind: kindEnum,
			Help:    "Bougies agrégées en direct. La stratégie ne décide qu'à la CLÔTURE de l'une d'elles.",
			Choices: timeframeNames,
			Get:     func(c *config.Config) string { return c.Broker.Timeframe },
			Set:     func(c *config.Config, s string) error { c.Broker.Timeframe = s; return nil },
		},
		{
			Section: "Compte", Path: "broker.replay_speed", Label: "Vitesse de rejeu", Kind: kindNumber, Step: 10, Digits: 0,
			Help: "Bougies M1 rejouées par seconde. 60 ≈ une heure de marché par minute réelle.",
			Get:  func(c *config.Config) string { return component.Num(c.Broker.ReplaySpeed, 0) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Broker.ReplaySpeed, s) },
		},

		// --- Risque ---
		{
			Section: "Risque", Path: "risk.max_position_size", Label: "Plafond de taille", Kind: kindNumber, Step: 10000, Digits: 0,
			Help: "GARDE : aucune entrée ne dépassera cette taille, quel que soit le mode. 100 000 = lot standard.",
			Get:  func(c *config.Config) string { return component.Num(c.Risk.MaxPositionSize, 0) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Risk.MaxPositionSize, s) },
		},
		{
			Section: "Risque", Path: "risk.fixed_position_size", Label: "Taille fixe", Kind: kindNumber, Step: 1000, Digits: 0,
			Help: "Taille employée quand le risque par trade vaut 0. 10 000 = mini-lot.",
			Get:  func(c *config.Config) string { return component.Num(c.Risk.FixedPositionSize, 0) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Risk.FixedPositionSize, s) },
		},
		{
			Section: "Risque", Path: "risk.risk_per_trade_pct", Label: "Risque par trade", Kind: kindNumber, Step: 0.25, Digits: 2,
			Help: "% de l'équité risqué jusqu'au stop. 0 = taille fixe. Sur une paire non convertible, TOUTES les entrées sont refusées.",
			Get:  func(c *config.Config) string { return component.Num(c.Risk.RiskPerTradePct, 2) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Risk.RiskPerTradePct, s) },
		},
		{
			Section: "Risque", Path: "risk.max_positions_per_symbol", Label: "Positions par paire", Kind: kindNumber, Step: 1,
			Help: "Empêche d'empiler des entrées sur la même paire.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Risk.MaxPositionsPerSymbol) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Risk.MaxPositionsPerSymbol, s) },
		},
		{
			Section: "Risque", Path: "risk.max_open_positions", Label: "Positions au total", Kind: kindNumber, Step: 1,
			Help: "Plafond d'exposition du compte, toutes paires confondues.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Risk.MaxOpenPositions) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Risk.MaxOpenPositions, s) },
		},
		{
			Section: "Risque", Path: "risk.max_daily_loss_pct", Label: "Perte journalière max", Kind: kindNumber, Step: 0.5, Digits: 2,
			Help: "En % de l'équité de début de journée UTC. Au-delà, plus aucune ENTRÉE ; les sorties restent permises. Garde LIVE uniquement.",
			Get:  func(c *config.Config) string { return component.Num(c.Risk.MaxDailyLossPct, 2) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Risk.MaxDailyLossPct, s) },
		},
		{
			Section: "Risque", Path: "costs.commission_per_unit", Label: "Commission / unité", Kind: kindNumber, Step: 0.00001, Digits: 5,
			Help: "S'ajoute au spread MESURÉ dans les données. Le spread, lui, ne se règle pas.",
			Get:  func(c *config.Config) string { return component.Num(c.Costs.CommissionPerUnit, 5) },
			Set:  func(c *config.Config, s string) error { return setFloat(&c.Costs.CommissionPerUnit, s) },
		},

		// --- Stratégie ---
		{
			Section: "Stratégie", Path: "strategy.name", Label: "Moteur de décision", Kind: kindEnum,
			Help:    "Révision de stratégie utilisée par le live, le backtest et l'entraînement.",
			Choices: func() []string { return strategy.List() },
			Get:     func(c *config.Config) string { return c.Strategy.Name },
			Set:     func(c *config.Config, s string) error { c.Strategy.Name = s; return nil },
		},
		{
			Section: "Stratégie", Path: "strategy.enabled", Label: "Kill-switch au démarrage", Kind: kindBool,
			Help: "État du kill-switch GLOBAL au lancement. En séance, il se pilote depuis l'écran Live.",
			Get:  func(c *config.Config) string { return strconv.FormatBool(c.Strategy.Enabled) },
			Set: func(c *config.Config, s string) error {
				c.Strategy.Enabled = s == "true"
				return nil
			},
		},

		// --- Historique ---
		{
			Section: "Historique", Path: "history.start_year", Label: "Première année", Kind: kindNumber, Step: 1,
			Help: "Début de l'historique téléchargé depuis Dukascopy.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.History.StartYear) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.History.StartYear, s) },
		},
		{
			Section: "Historique", Path: "history.concurrency", Label: "Téléchargements simultanés", Kind: kindNumber, Step: 1,
			Help: "Au-delà de 3-4, Dukascopy répond 429 (limite de débit). La valeur basse est voulue.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.History.Concurrency) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.History.Concurrency, s) },
		},
		{
			Section: "Historique", Path: "history.instruments", Label: "Paires suivies", Kind: kindSymbols,
			Help:    "Paires téléchargées et proposées partout ailleurs. Entrée ouvre le sélecteur.",
			Choices: instrumentNames,
			Get:     func(c *config.Config) string { return strings.Join(c.History.Instruments, ", ") },
			Set: func(c *config.Config, s string) error {
				list := splitList(s)
				if len(list) == 0 {
					return fmt.Errorf("au moins une paire est nécessaire")
				}
				for _, sym := range list {
					if _, err := data.LookupInstrument(sym); err != nil {
						return err
					}
				}
				c.History.Instruments = list
				return nil
			},
		},

		// --- Entraînement ---
		{
			Section: "Entraînement", Path: "training.folds", Label: "Plis", Kind: kindNumber, Step: 1,
			Help: "Blocs de test consécutifs du walk-forward. Plus de plis = plus de mesures, plus long.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Training.Folds) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Training.Folds, s) },
		},
		{
			Section: "Entraînement", Path: "training.timeframe", Label: "Unité de temps", Kind: kindEnum,
			Help:    "Unité de travail de l'entraînement ET du backtest.",
			Choices: timeframeNames,
			Get:     func(c *config.Config) string { return c.Training.Timeframe },
			Set:     func(c *config.Config, s string) error { c.Training.Timeframe = s; return nil },
		},
		{
			Section: "Entraînement", Path: "training.workers", Label: "Plis en parallèle", Kind: kindNumber, Step: 1,
			Help: "0 = un par cœur disponible.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Training.Workers) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Training.Workers, s) },
		},
		{
			Section: "Entraînement", Path: "training.seed", Label: "Graine", Kind: kindNumber, Step: 1,
			Help: "Graine fixée = entraînement REPRODUCTIBLE. Elle est archivée avec chaque run.",
			Get:  func(c *config.Config) string { return strconv.FormatInt(c.Training.Seed, 10) },
			Set: func(c *config.Config, s string) error {
				n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
				if err != nil {
					return fmt.Errorf("entier attendu, reçu %q", s)
				}
				c.Training.Seed = n
				return nil
			},
		},

		// --- Interface ---
		{
			Section: "Interface", Path: "ui.theme", Label: "Thème", Kind: kindEnum,
			Help:    "auto : la luminosité du fond est détectée. dark / light la forcent, quand la détection se trompe.",
			Choices: func() []string { return theme.Names },
			Get:     func(c *config.Config) string { return c.UI.Theme },
			Set:     func(c *config.Config, s string) error { c.UI.Theme = s; return nil },
		},
		{
			Section: "Interface", Path: "ui.refresh_millis", Label: "Rafraîchissement (ms)", Kind: kindNumber, Step: 100,
			Help: "Cadence de redessin des écrans temps réel.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.UI.RefreshMillis) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.UI.RefreshMillis, s) },
		},
		{
			Section: "Interface", Path: "ui.chart_timeframe", Label: "Unité du graphique", Kind: kindEnum,
			Help:    "Unité de temps du graphique de l'écran Live au démarrage.",
			Choices: timeframeNames,
			Get:     func(c *config.Config) string { return c.UI.ChartTimeframe },
			Set:     func(c *config.Config, s string) error { c.UI.ChartTimeframe = s; return nil },
		},

		// --- Journal ---
		{
			Section: "Journal", Path: "logging.level", Label: "Niveau", Kind: kindEnum,
			Help:    "DEBUG détaille chaque rejet du risque : précieux pour comprendre un silence, verbeux en continu.",
			Choices: func() []string { return []string{"debug", "info", "warn", "error"} },
			Get:     func(c *config.Config) string { return c.Logging.Level },
			Set:     func(c *config.Config, s string) error { c.Logging.Level = s; return nil },
		},
		{
			Section: "Journal", Path: "logging.buffer_size", Label: "Lignes en mémoire", Kind: kindNumber, Step: 100,
			Help: "Taille du tampon lu par l'écran Journal. Le fichier, lui, garde tout.",
			Get:  func(c *config.Config) string { return strconv.Itoa(c.Logging.BufferSize) },
			Set:  func(c *config.Config, s string) error { return setInt(&c.Logging.BufferSize, s) },
		},
	}
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToUpper(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- Interaction --------------------------------------------------------

func (v *Settings) current() settingField { return v.fields[v.cursor] }

// forcedBy renvoie la variable d'environnement qui écrase ce réglage.
func (v *Settings) forcedBy(path string) string { return config.EnvOverrides()[path] }

func (v *Settings) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return v, nil
	}
	if v.picker != nil {
		done, accepted := v.picker.Update(key)
		if done {
			if accepted {
				v.apply(v.current(), strings.Join(v.picker.Selected(), ", "))
			}
			v.picker = nil
		}
		return v, nil
	}
	if v.editing {
		return v, v.editKey(key)
	}
	switch key.String() {
	case "up", "K":
		v.move(-1)
	case "down", "J":
		v.move(1)
	case "left", "h":
		v.step(-1)
	case "right", "l":
		v.step(1)
	case " ":
		if v.current().Kind == kindBool {
			v.step(1)
		}
	case "enter":
		if v.current().Kind == kindSymbols {
			v.openPicker()
			return v, nil
		}
		v.beginEdit()
	case "d":
		v.restoreDefault()
	case "s":
		v.save()
	case "r":
		v.reload()
	}
	return v, nil
}

// openPicker ouvre le sélecteur de paires sur le réglage courant.
func (v *Settings) openPicker() {
	f := v.current()
	if v.forcedBy(f.Path) != "" {
		v.deps.Status("réglage forcé par l'environnement : non modifiable ici")
		return
	}
	p := NewSymbolPicker(v.deps.Theme, "Paires suivies", splitList(f.Get(&v.draft)))
	// L'annotation est ce qui fait la différence entre choisir et
	// deviner : une paire non dimensionnable verra TOUTES ses entrées
	// refusées, et rien d'autre à l'écran ne le dirait au moment du choix.
	p.Note = v.symbolNote
	v.picker = p
}

// symbolNote annote une paire avec ce qui la rend inutilisable en l'état.
func (v *Settings) symbolNote(symbol string) string {
	th := v.deps.Theme
	if v.draft.Risk.RiskPerTradePct > 0 &&
		!data.ConversionFor(symbol, v.draft.Backtest.AccountCurrency).Exact {
		return th.Warning.Render("non dimensionnable en " + v.draft.Backtest.AccountCurrency)
	}
	return ""
}

func (v *Settings) move(delta int) {
	v.confirm = ""
	v.cursor += delta
	if v.cursor < 0 {
		v.cursor = len(v.fields) - 1
	}
	if v.cursor >= len(v.fields) {
		v.cursor = 0
	}
}

// step fait varier le réglage sélectionné d'un cran.
func (v *Settings) step(delta int) {
	f := v.current()
	if env := v.forcedBy(f.Path); env != "" {
		v.deps.Status(fmt.Sprintf("%s est forcé par %s : le modifier ici ne changerait rien", f.Path, env))
		return
	}
	switch f.Kind {
	case kindEnum:
		v.cycle(f, delta)
	case kindBool:
		v.apply(f, strconv.FormatBool(f.Get(&v.draft) != "true"))
	case kindNumber:
		cur, err := strconv.ParseFloat(strings.ReplaceAll(f.Get(&v.draft), " ", ""), 64)
		if err != nil {
			v.deps.Status("valeur illisible : " + f.Get(&v.draft))
			return
		}
		step := f.Step
		if step == 0 {
			step = 1
		}
		v.apply(f, strconv.FormatFloat(cur+float64(delta)*step, 'f', f.Digits, 64))
	default:
		v.beginEdit()
	}
}

func (v *Settings) cycle(f settingField, delta int) {
	choices := f.Choices()
	if len(choices) == 0 {
		return
	}
	cur := f.Get(&v.draft)
	idx := 0
	for i, c := range choices {
		if c == cur {
			idx = i
		}
	}
	idx = (idx + delta + len(choices)) % len(choices)
	v.apply(f, choices[idx])
}

// apply écrit une valeur dans le brouillon, après confirmation si le
// réglage engage de l'argent réel.
func (v *Settings) apply(f settingField, value string) {
	if f.Danger && value == "live" && v.confirm != f.Path {
		v.confirm = f.Path
		v.deps.Status("passage en LIVE — ARGENT RÉEL : appuyer de nouveau pour confirmer")
		return
	}
	v.confirm = ""
	if err := f.Set(&v.draft, value); err != nil {
		v.deps.Status(f.Path + " : " + err.Error())
		return
	}
	v.dirty = true
	v.revalidate()
}

// revalidate garde le message de la validation sous les yeux : une valeur
// refusée doit se voir AVANT la tentative d'enregistrement.
func (v *Settings) revalidate() {
	if err := v.draft.Validate(); err != nil {
		v.invalid = err.Error()
		return
	}
	v.invalid = ""
}

func (v *Settings) beginEdit() {
	f := v.current()
	if env := v.forcedBy(f.Path); env != "" {
		v.deps.Status(fmt.Sprintf("%s est forcé par %s", f.Path, env))
		return
	}
	v.editing = true
	v.buffer = f.Get(&v.draft)
}

func (v *Settings) editKey(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc:
		v.editing, v.buffer = false, ""
	case tea.KeyEnter:
		f := v.current()
		v.editing = false
		v.apply(f, v.buffer)
		v.buffer = ""
	case tea.KeyBackspace:
		if r := []rune(v.buffer); len(r) > 0 {
			v.buffer = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		v.buffer += key.String()
	}
	return nil
}

// restoreDefault remet le réglage sélectionné à la valeur livrée.
func (v *Settings) restoreDefault() {
	f := v.current()
	def := config.Default()
	v.apply(f, f.Get(&def))
	v.deps.Status(f.Path + " remis à sa valeur par défaut")
}

// save écrit config.yaml — et REFUSE d'écrire un fichier que le programme
// refuserait de relire au démarrage suivant.
func (v *Settings) save() {
	if err := v.draft.Validate(); err != nil {
		v.invalid = err.Error()
		v.deps.Status("configuration refusée, rien n'a été écrit")
		return
	}
	if err := v.draft.Save(); err != nil {
		v.deps.Status("écriture impossible : " + err.Error())
		return
	}
	v.dirty = false
	v.deps.Status("config.yaml enregistré — les réglages prendront effet au prochain démarrage")
}

// reload reprend le fichier tel qu'il est sur le disque, en abandonnant
// les modifications en cours.
func (v *Settings) reload() {
	cfg, err := config.Load(v.deps.App.Config.Paths)
	if err != nil {
		v.deps.Status("rechargement impossible : " + err.Error())
		return
	}
	v.draft, v.dirty, v.invalid = cfg, false, ""
	v.deps.Status("configuration rechargée depuis le disque")
}

// --- Rendu --------------------------------------------------------------

func (v *Settings) Render(width, height int) string {
	th := v.deps.Theme
	if v.picker != nil {
		return v.picker.Render(width, height)
	}
	var sb strings.Builder

	// Les deux panneaux fixes sont dessinés AVANT d'arbitrer la hauteur du
	// tableau : leur taille dépend de la largeur (l'avertissement et
	// l'aide s'enroulent), et la deviner par une constante faisait
	// déborder l'écran de cinq lignes à toutes les tailles.
	header := v.renderHeader(width)
	help := v.renderHelp(width)
	// Petits terminaux : les deux panneaux fixes coûtaient à eux seuls
	// onze lignes, et le tableau des réglages n'en gardait aucune. Ils
	// passent alors en lignes simples, sans cadre ; aucun avertissement
	// ne disparaît.
	if height-lipgloss.Height(header)-lipgloss.Height(help) < settingsMinTable {
		header, help = v.renderHeaderLine(width), v.renderHelpLines(width)
	}

	sb.WriteString(header)
	sb.WriteString("\n")

	cols := []component.Column{
		{Title: "Réglage", Width: 26},
		{Title: "Valeur", Width: 22, Flex: true, Min: 10},
		{Title: "Clé", Width: 30, Priority: 1, Flex: true, Min: 12},
	}
	env := config.EnvOverrides()
	rows := make([][]string, 0, len(v.fields))
	section := ""
	index := make([]int, 0, len(v.fields))
	for i, f := range v.fields {
		if f.Section != section {
			section = f.Section
			rows = append(rows, []string{th.Subtitle.Render(strings.ToUpper(section)), "", ""})
			index = append(index, -1)
		}
		value := f.Get(&v.draft)
		if v.editing && i == v.cursor {
			value = th.Accent.Render(v.buffer + "▏")
		} else if e, forced := env[f.Path]; forced {
			// Un réglage que l'environnement écrasera au démarrage n'est
			// pas modifiable : le dire vaut mieux que le laisser éditer
			// pour rien.
			value = th.Warning.Render(value + " ← " + e)
		}
		rows = append(rows, []string{f.Label, value, th.Muted.Render(f.Path)})
		index = append(index, i)
	}
	cursorRow := 0
	for r, i := range index {
		if i == v.cursor {
			cursorRow = r
		}
	}

	// Le panneau du tableau prend EXACTEMENT ce que les deux autres
	// laissent : deux bordures, la ligne d'entête et la ligne « N lignes »
	// du tableau, le reste en réglages.
	budget := height - lipgloss.Height(header) - lipgloss.Height(help)
	sb.WriteString(component.FitBlock(budget, 2, budget, func(n int) string {
		return component.Panel(th, "Réglages",
			component.Table(th, cols, rows, cursorRow, n, component.PanelContent(width)), width)
	}))
	sb.WriteString("\n")
	sb.WriteString(help)
	return sb.String()
}

func (v *Settings) renderHeader(width int) string {
	th := v.deps.Theme
	state := th.Muted.Render("aucune modification en attente")
	if v.dirty {
		state = th.Warning.Render("modifications NON enregistrées — s pour écrire config.yaml")
	}
	body := state + "\n" + th.Muted.Render(component.Truncate(
		"Fichier : "+v.deps.App.Config.Paths.ConfigFile(), component.PanelContent(width)))
	// Répété à chaque affichage, parce que c'est la seule chose qui
	// pourrait tromper : rien de ce qui est réglé ici ne s'applique à la
	// séance en cours.
	body += "\n" + th.Warning.Render(component.Truncate(
		"⚠ Les réglages sont relus au DÉMARRAGE. Le moteur en cours garde ceux avec lesquels il a été lancé.",
		component.PanelContent(width)))
	if v.invalid != "" {
		body += "\n" + th.Negative.Render(component.Truncate(
			"✗ "+strings.ReplaceAll(v.invalid, "\n", " · "), component.PanelContent(width)))
	}
	return component.Panel(th, "Configuration", body, width)
}

// settingsMinTable : en deçà, le tableau des réglages (cadre, entête, pied
// compris) ne montre plus que deux ou trois lignes.
const settingsMinTable = 8

// renderHeaderLine : l'état du brouillon et l'avertissement, en une ou
// deux lignes sans cadre.
func (v *Settings) renderHeaderLine(width int) string {
	th := v.deps.Theme
	state := th.Muted.Render("aucune modification en attente")
	if v.dirty {
		state = th.Warning.Render("NON enregistré — s écrit config.yaml")
	}
	line := state + th.Warning.Render(" · ⚠ effet au redémarrage")
	out := component.Clip(line, width)
	if v.invalid != "" {
		out += "\n" + th.Negative.Render(component.Truncate(
			"✗ "+strings.ReplaceAll(v.invalid, "\n", " · "), width))
	}
	return out
}

// renderHelpLines : l'aide du réglage courant en deux lignes sans cadre.
func (v *Settings) renderHelpLines(width int) string {
	th := v.deps.Theme
	f := v.current()
	second := th.Muted.Render(component.Truncate(f.Help, width))
	if env := v.forcedBy(f.Path); env != "" {
		second = th.Warning.Render(component.Truncate("⚠ forcé par "+env, width))
	}
	return th.Text.Render(component.Truncate(f.Label+" — "+f.Path, width)) + "\n" + second
}

func (v *Settings) renderHelp(width int) string {
	th := v.deps.Theme
	f := v.current()
	body := th.Text.Render(component.Truncate(f.Label+" — "+f.Path, component.PanelContent(width))) +
		"\n" + th.Muted.Render(component.Truncate(f.Help, component.PanelContent(width)))
	if f.Kind == kindEnum || f.Kind == kindList || f.Kind == kindSymbols {
		if choices := f.Choices(); len(choices) > 0 {
			label := "valeurs : " + strings.Join(choices, " ")
			if f.Kind == kindList || f.Kind == kindSymbols {
				label = fmt.Sprintf("%d paires connues · entrée pour choisir", len(choices))
			}
			body += "\n" + th.Muted.Render(component.Truncate(label, component.PanelContent(width)))
		}
	}
	if env := v.forcedBy(f.Path); env != "" {
		body += "\n" + th.Warning.Render(component.Truncate(
			"⚠ Forcé par la variable "+env+" : l'environnement a le dernier mot au démarrage.",
			component.PanelContent(width)))
	}
	return component.Panel(th, "Ce que ce réglage change", body, width)
}

// CapturesKeys : pendant une saisie libre, l'écran prend TOUTES les
// touches — y compris les chiffres, qui changeraient d'onglet, et « q »,
// qui quitterait le programme au milieu d'un mot.
func (v *Settings) CapturesKeys() bool { return v.editing || v.picker != nil }
