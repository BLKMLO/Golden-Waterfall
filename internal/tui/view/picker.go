package view

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// SymbolPicker : choix MULTIPLE de paires, avec recherche.
//
// Ce que remplaçait l'ancien réglage : un champ de texte où trente et une
// paires s'écrivaient à la virgule. Pour en retirer une, il fallait
// retrouver son nom au milieu d'une ligne de deux cents caractères ; pour
// en ajouter une, se souvenir de son orthographe exacte. Une liste qu'on
// ne peut pas parcourir n'est pas une liste, c'est une chaîne.
//
// Le sélecteur ne DÉCIDE de rien : il rend une liste de symboles à
// l'écran qui l'a ouvert, lequel reste responsable de la valider.
type SymbolPicker struct {
	th       theme.Theme
	title    string
	all      []string
	selected map[string]bool
	// Note annote un symbole (« non dimensionnable », « pas d'historique »).
	// C'est ce qui fait la différence entre choisir et deviner.
	Note func(symbol string) string
	// MinOne : refuser de valider une sélection vide.
	MinOne bool
	// Tradable, s'il est posé, permet de masquer (touche v) les paires
	// qui ne produiraient rien ; OnlyTradable dit si le filtre est actif
	// et TradableLabel le décrit (« dimensionnables en USD »).
	Tradable      func(symbol string) bool
	OnlyTradable  bool
	TradableLabel string

	filter    string
	searching bool
	buffer    string
	cursor    int
	message   string
}

// NewSymbolPicker construit un sélecteur sur tous les symboles connus.
func NewSymbolPicker(th theme.Theme, title string, initial []string) *SymbolPicker {
	p := &SymbolPicker{
		th:       th,
		title:    title,
		all:      data.SupportedSymbols(),
		selected: map[string]bool{},
		MinOne:   true,
	}
	for _, s := range initial {
		p.selected[strings.ToUpper(s)] = true
	}
	// Le curseur démarre sur la première paire sélectionnée : sur une
	// liste de trente et une, arriver sur « AUDCAD » quand on suit
	// EURUSD n'aide personne.
	for i, s := range p.all {
		if p.selected[s] {
			p.cursor = i
			break
		}
	}
	return p
}

// Selected renvoie les symboles cochés, dans l'ordre du catalogue.
func (p *SymbolPicker) Selected() []string {
	out := make([]string, 0, len(p.selected))
	for _, s := range p.all {
		if p.selected[s] {
			out = append(out, s)
		}
	}
	return out
}

// CapturesKeys : le sélecteur est MODAL. Tant qu'il est ouvert, les
// touches lui appartiennent — sans quoi « 3 » changerait d'onglet et
// « q » quitterait le programme en pleine sélection.
func (p *SymbolPicker) CapturesKeys() bool { return true }

// visible renvoie les symboles retenus par le filtre.
func (p *SymbolPicker) visible() []string { return p.matching(true) }

// matching applique le filtre texte, et le masque des paires non
// tradables si mask le demande.
func (p *SymbolPicker) matching(mask bool) []string {
	base := p.all
	if mask && p.OnlyTradable && p.Tradable != nil {
		base = make([]string, 0, len(p.all))
		for _, s := range p.all {
			if p.Tradable(s) {
				base = append(base, s)
			}
		}
	}
	if p.filter == "" {
		return base
	}
	needle := strings.ToUpper(p.filter)
	out := make([]string, 0, len(base))
	for _, s := range base {
		if strings.Contains(s, needle) {
			out = append(out, s)
			continue
		}
		// La classe est aussi une clé de recherche : taper « majeure »
		// ou « métal » est souvent ce qu'on a en tête.
		if inst, err := data.LookupInstrument(s); err == nil &&
			strings.Contains(strings.ToUpper(inst.Class), needle) {
			out = append(out, s)
		}
	}
	return out
}

// Update traite une touche. Le second retour dit si la sélection est
// TERMINÉE, le troisième si elle a été validée (par opposition à
// abandonnée).
func (p *SymbolPicker) Update(msg tea.KeyMsg) (done, accepted bool) {
	if p.searching {
		p.searchKey(msg)
		return false, false
	}
	list := p.visible()
	if p.cursor >= len(list) {
		p.cursor = maxInt(len(list)-1, 0)
	}
	switch msg.String() {
	case "/":
		p.searching, p.buffer = true, p.filter
	case "esc":
		if p.filter != "" {
			p.filter, p.cursor = "", 0
			return false, false
		}
		return true, false
	case "enter":
		if p.MinOne && len(p.Selected()) == 0 {
			p.message = "au moins une paire est nécessaire"
			return false, false
		}
		return true, true
	case " ":
		if p.cursor < len(list) {
			p.toggle(list[p.cursor])
		}
	case "up", "K":
		p.cursor--
	case "down", "J":
		p.cursor++
	case "pgup":
		p.cursor -= 10
	case "pgdown":
		p.cursor += 10
	case "home":
		p.cursor = 0
	case "end":
		p.cursor = len(list) - 1
	case "a":
		// « tout » s'entend AU SENS DU FILTRE : c'est ce qui rend la
		// recherche utile (taper « JPY », puis « a »).
		for _, s := range list {
			p.selected[s] = true
		}
	case "n":
		// « aucun » décoche aussi les paires que le masque « tradables »
		// cache : sinon elles resteraient cochées sans qu'on les voie, et
		// la sélection validée en contiendrait vingt de trop.
		for _, s := range p.matching(false) {
			delete(p.selected, s)
		}
	case "i":
		for _, s := range list {
			p.toggle(s)
		}
	case "v":
		if p.Tradable != nil {
			p.OnlyTradable = !p.OnlyTradable
			p.cursor = 0
		}
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(list) {
		p.cursor = maxInt(len(list)-1, 0)
	}
	return false, false
}

func (p *SymbolPicker) toggle(symbol string) {
	if p.selected[symbol] {
		delete(p.selected, symbol)
		return
	}
	p.selected[symbol] = true
}

func (p *SymbolPicker) searchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEnter:
		p.filter, p.searching, p.cursor = strings.TrimSpace(p.buffer), false, 0
	case tea.KeyEsc:
		p.searching, p.buffer = false, ""
	case tea.KeyBackspace:
		if r := []rune(p.buffer); len(r) > 0 {
			p.buffer = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		p.buffer += string(msg.Runes)
	case tea.KeySpace:
		p.buffer += " "
	}
}

// Keys décrit les raccourcis du sélecteur pour la barre d'aide.
func (p *SymbolPicker) Keys() [][2]string {
	return [][2]string{
		{"espace", "cocher / décocher"},
		{"/", "filtrer"},
		{"a / n / i", "tout · aucun · inverser (dans le filtre)"},
		{"v", "tradables seulement / toutes"},
		{"entrée", "valider"},
		{"échap", "annuler"},
	}
}

// Render dessine le sélecteur dans l'espace donné.
func (p *SymbolPicker) Render(width, height int) string {
	th := p.th
	list := p.visible()

	cols := []component.Column{
		{Title: "", Width: 3},
		{Title: "Paire", Width: 8},
		{Title: "Classe", Width: 10, Priority: 2},
		{Title: "Remarque", Width: 30, Flex: true, Min: 10, Priority: 1},
	}
	rows := make([][]string, 0, len(list))
	for _, sym := range list {
		mark := th.Muted.Render("[ ]")
		if p.selected[sym] {
			mark = th.Positive.Render("[×]")
		}
		class := ""
		if inst, err := data.LookupInstrument(sym); err == nil {
			class = inst.Class
		}
		note := ""
		if p.Note != nil {
			note = p.Note(sym)
		}
		rows = append(rows, []string{mark, sym, th.Muted.Render(class), note})
	}

	head := fmt.Sprintf("%d sélectionnée(s) sur %d", len(p.Selected()), len(p.all))
	if p.OnlyTradable && p.Tradable != nil {
		// Une paire cochée puis masquée reste cochée : le dire, sinon la
		// sélection validée contiendrait des paires que l'écran cachait.
		hidden := 0
		for _, s := range p.Selected() {
			if !p.Tradable(s) {
				hidden++
			}
		}
		head += fmt.Sprintf(" · %s seulement (v toutes)", p.TradableLabel)
		if hidden > 0 {
			head += fmt.Sprintf(" · %d cochée(s) masquée(s)", hidden)
		}
	}
	if p.filter != "" {
		head += fmt.Sprintf(" · filtre « %s » : %d affichée(s)", p.filter, len(list))
	}
	lines := []string{th.Muted.Render(head)}
	switch {
	case p.searching:
		lines = append(lines, th.Accent.Render("/"+p.buffer+"▏")+
			th.Muted.Render("  entrée filtre · échap annule"))
	case p.message != "":
		lines = append(lines, th.Negative.Render("✗ "+p.message))
	default:
		lines = append(lines, th.Muted.Render(
			"espace coche · / filtre · a tout · n aucun · i inverse · v tradables · entrée valide · échap annule"))
	}

	inner := component.PanelContent(width)
	head2 := strings.Join(lines, "\n")
	// Hauteur MESURÉE, pas devinée : le nombre de lignes du panneau
	// dépend des enroulements de l'entête, et une soustraction de
	// constantes se trompait de deux lignes.
	return component.FitBlock(height, 3, height, func(n int) string {
		return component.Panel(th, p.title,
			head2+"\n"+component.Table(th, cols, rows, p.cursor, n, inner), width)
	})
}
