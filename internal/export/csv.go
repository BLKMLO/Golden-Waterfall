// Package export écrit les données du programme dans un format que
// d'autres outils savent lire.
//
// Il ne calcule RIEN. Un export qui recalculerait une colonne — un P&L
// net, un cumul — pourrait présenter un chiffre différent de celui de
// l'écran ; le seul export honnête recopie ce que le programme a déjà
// mesuré, champ pour champ.
//
// Convention de fichier, assumée : séparateur « ; », décimale « , »,
// UTF-8 précédé d'une marque d'ordre des octets. C'est ce qu'un tableur
// francophone ouvre d'un double-clic, sans boîte de dialogue d'import et
// sans transformer « 1.25 » en date. Pour pandas :
// read_csv(path, sep=";", decimal=",").
package export

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/backtest"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// bom : marque d'ordre des octets UTF-8. Sans elle, un tableur
// francophone lit « Entrée » comme « EntrÃ©e ».
const bom = "\ufeff"

// timeLayout : date lisible ET triable, en UTC comme tout le reste du
// programme.
const timeLayout = "2006-01-02 15:04:05"

// Name compose un nom de fichier horodaté, sans caractère qui fâche un
// système de fichiers.
func Name(kind string, at time.Time, parts ...string) string {
	fields := append([]string{kind}, parts...)
	clean := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			clean = append(clean, strings.Map(func(r rune) rune {
				if r == '/' || r == '\\' || r == ':' || r == ' ' {
					return '_'
				}
				return r
			}, f))
		}
	}
	return strings.Join(clean, "_") + "_" + at.UTC().Format("20060102-150405") + ".csv"
}

// num formate un nombre à la française. Une valeur non finie devient une
// cellule VIDE : dans un tableur, une cellule vide se voit, un zéro se
// confond avec une mesure.
func num(v float64, decimals int) string {
	if v != v || v > 1e308 || v < -1e308 {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', decimals, 64), ".", ",", 1)
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// write ouvre le fichier, y verse les lignes et le referme. L'écriture
// passe par un fichier temporaire renommé : un export interrompu ne
// laisse pas un CSV à moitié écrit qui passerait pour complet.
func write(path string, rows [][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(bom); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	if err := w.WriteAll(rows); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// tradeHeader et tradeRow sont la MÊME liste, dans le même ordre : les
// tenir côte à côte est la seule façon de ne pas décaler une colonne.
var tradeHeader = []string{
	"id", "paire", "strategie", "sens", "quantite",
	"entree", "prix_entree", "sortie", "prix_sortie",
	"duree_heures", "pnl", "cout", "motif_sortie",
}

func tradeRow(t core.Trade) []string {
	return []string{
		strconv.FormatInt(t.ID, 10), t.Symbol, t.Strategy, string(t.Side), num(t.Quantity, 2),
		stamp(t.EntryTime), num(t.EntryPrice, 5), stamp(t.ExitTime), num(t.ExitPrice, 5),
		num(t.Duration().Hours(), 2), num(t.PnL, 2), num(t.Cost, 2), t.ExitReason,
	}
}

// Trades écrit un journal de trades.
func Trades(path string, trades []core.Trade) error {
	rows := make([][]string, 0, len(trades)+1)
	rows = append(rows, tradeHeader)
	for _, t := range trades {
		rows = append(rows, tradeRow(t))
	}
	return write(path, rows)
}

// Equity écrit une courbe de valeur, un point par ligne.
func Equity(path string, points []backtest.EquityPoint) error {
	rows := make([][]string, 0, len(points)+1)
	rows = append(rows, []string{"horodatage", "valeur"})
	for _, p := range points {
		rows = append(rows, []string{stamp(p.Time), num(p.Value, 2)})
	}
	return write(path, rows)
}

// Stats écrit les métriques d'un backtest en deux colonnes.
//
// Deux colonnes et non une ligne : c'est la forme qu'on relit, et elle
// accepte qu'une métrique NON MESURÉE reste vide sans décaler le reste.
func Stats(path string, s backtest.Stats) error {
	rows := [][]string{{"metrique", "valeur"}}
	add := func(label, value string) { rows = append(rows, []string{label, value}) }

	add("paire", s.Symbol)
	add("devise", s.Currency)
	add("debut", stamp(s.Start))
	add("fin", stamp(s.End))
	add("bougies", strconv.Itoa(s.Bars))
	add("trades", strconv.Itoa(s.Trades))
	add("gagnants", strconv.Itoa(s.Wins))
	add("perdants", strconv.Itoa(s.Losses))
	add("taux_de_gain_pct", num(s.WinRate, 2))
	add("capital_initial", num(s.InitialCapital, 2))
	add("equite_finale", num(s.FinalEquity, 2))
	add("rendement_pct", num(s.ReturnPct, 4))
	add("pnl_net", num(s.NetPnL, 2))
	add("profit_brut", num(s.GrossProfit, 2))
	add("perte_brute", num(s.GrossLoss, 2))
	add("couts", num(s.Costs, 2))
	add("spread_median", num(s.Spread, 6))
	add("profit_factor", num(s.ProfitFactor, 4))
	add("esperance", num(s.Expectancy, 4))
	add("gain_moyen", num(s.AvgWin, 2))
	add("perte_moyenne", num(s.AvgLoss, 2))
	add("sqn", num(s.SQN, 4))
	add("drawdown_max_pct", num(s.MaxDrawdownPct, 4))
	add("sharpe", num(s.Sharpe, 4))
	add("ordres_refuses", strconv.Itoa(s.RejectedOrders))
	add("tailles_plafonnees", strconv.Itoa(s.SizeCapped))
	for _, k := range sortedKeys(s.Rejections) {
		add("refus_"+k, strconv.Itoa(s.Rejections[k]))
	}
	for _, k := range sortedKeys(s.ExitReasons) {
		add("sortie_"+k, strconv.Itoa(s.ExitReasons[k]))
	}
	// Les trois drapeaux d'honnêteté voyagent AVEC les chiffres : sortis
	// de l'écran qui les affiche, ils sont la seule chose qui dise si ces
	// chiffres peuvent être additionnés.
	add("couts_modelises", boolean(s.CostsModelled))
	add("devise_exacte", boolean(s.CurrencyExact))
	// Filtre d'actualités : ses compteurs n'ont de sens que s'il était
	// actif. Inactif, les deux cellules restent VIDES — « 0 écartée »
	// dirait qu'il a cherché et rien trouvé.
	add("filtre_actualites", boolean(s.NewsFilter))
	blocked, uncovered := "", ""
	if s.NewsFilter {
		blocked, uncovered = strconv.Itoa(s.NewsBlocked), strconv.Itoa(s.NewsUncovered)
	}
	add("actualites_ecartees", blocked)
	add("actualites_hors_calendrier", uncovered)
	return write(path, rows)
}

// sortedKeys : un export doit être REPRODUCTIBLE. L'ordre de parcours
// d'une map Go est volontairement aléatoire ; deux exports du même run
// donneraient deux fichiers différents, donc indiffables.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolean(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

// Backtest écrit les trois fichiers d'un résultat de backtest et renvoie
// les chemins écrits, dans l'ordre.
func Backtest(dir, symbol, timeframe string, res *backtest.Result, at time.Time) ([]string, error) {
	if res == nil {
		return nil, fmt.Errorf("aucun résultat à exporter")
	}
	base := []struct {
		kind  string
		write func(string) error
	}{
		{"backtest-trades", func(p string) error { return Trades(p, res.Trades) }},
		{"backtest-equity", func(p string) error { return Equity(p, res.Equity) }},
		{"backtest-stats", func(p string) error { return Stats(p, res.Stats) }},
	}
	out := make([]string, 0, len(base))
	for _, b := range base {
		path := filepath.Join(dir, Name(b.kind, at, symbol, timeframe))
		if err := b.write(path); err != nil {
			return out, err
		}
		out = append(out, path)
	}
	return out, nil
}
