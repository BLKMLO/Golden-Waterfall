package data

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Instrument décrit la correspondance entre un symbole du projet et un
// instrument Dukascopy.
type Instrument struct {
	// DukascopyID : identifiant dans les URLs du flux historique.
	DukascopyID string
	// Decimals : les prix sont publiés en ENTIERS ; prix réel = entier / 10^Decimals.
	Decimals int
	// Class sert uniquement à l'affichage et au regroupement.
	Class string
	// Base et Quote : devises de la paire. Le PRIX est exprimé en Quote
	// pour une unité de Base, et le P&L d'un trade naît donc en Quote.
	//
	// Sans ces deux champs, un compte en dollars ne peut pas juger la
	// marge d'une position USDJPY : le notionnel y est en yens, cent
	// cinquante fois plus grand, et toutes les entrées sont refusées —
	// exactement le défaut qu'on corrige ici.
	Base  string
	Quote string
}

// Scale renvoie 10^Decimals. Elle sert à l'ÉCRITURE : les prix sont
// arrondis à la précision réelle de l'instrument avant d'être compressés,
// faute de quoi le bruit de bas de mantisse ferait tripler le fichier.
func (i Instrument) Scale() int32 { return int32(math.Pow10(i.Decimals)) }

// Instruments : ajouter un actif = ajouter une ligne ici.
// Forex : 5 décimales (3 pour les paires JPY). Métaux et indices : 3.
var Instruments = map[string]Instrument{
	// Majeures
	"EURUSD": {"EURUSD", 5, "majeure", "EUR", "USD"},
	"GBPUSD": {"GBPUSD", 5, "majeure", "GBP", "USD"},
	"USDJPY": {"USDJPY", 3, "majeure", "USD", "JPY"},
	"USDCHF": {"USDCHF", 5, "majeure", "USD", "CHF"},
	"USDCAD": {"USDCAD", 5, "majeure", "USD", "CAD"},
	"AUDUSD": {"AUDUSD", 5, "majeure", "AUD", "USD"},
	"NZDUSD": {"NZDUSD", 5, "majeure", "NZD", "USD"},
	// Croisées EUR
	"EURGBP": {"EURGBP", 5, "croisée", "EUR", "GBP"},
	"EURJPY": {"EURJPY", 3, "croisée", "EUR", "JPY"},
	"EURCHF": {"EURCHF", 5, "croisée", "EUR", "CHF"},
	"EURAUD": {"EURAUD", 5, "croisée", "EUR", "AUD"},
	"EURCAD": {"EURCAD", 5, "croisée", "EUR", "CAD"},
	"EURNZD": {"EURNZD", 5, "croisée", "EUR", "NZD"},
	// Croisées GBP
	"GBPJPY": {"GBPJPY", 3, "croisée", "GBP", "JPY"},
	"GBPCHF": {"GBPCHF", 5, "croisée", "GBP", "CHF"},
	"GBPAUD": {"GBPAUD", 5, "croisée", "GBP", "AUD"},
	"GBPCAD": {"GBPCAD", 5, "croisée", "GBP", "CAD"},
	"GBPNZD": {"GBPNZD", 5, "croisée", "GBP", "NZD"},
	// Autres croisées
	"AUDJPY": {"AUDJPY", 3, "croisée", "AUD", "JPY"},
	"AUDCHF": {"AUDCHF", 5, "croisée", "AUD", "CHF"},
	"AUDCAD": {"AUDCAD", 5, "croisée", "AUD", "CAD"},
	"AUDNZD": {"AUDNZD", 5, "croisée", "AUD", "NZD"},
	"NZDJPY": {"NZDJPY", 3, "croisée", "NZD", "JPY"},
	"NZDCHF": {"NZDCHF", 5, "croisée", "NZD", "CHF"},
	"NZDCAD": {"NZDCAD", 5, "croisée", "NZD", "CAD"},
	"CADJPY": {"CADJPY", 3, "croisée", "CAD", "JPY"},
	"CADCHF": {"CADCHF", 5, "croisée", "CAD", "CHF"},
	"CHFJPY": {"CHFJPY", 3, "croisée", "CHF", "JPY"},
	// Métaux
	"XAUUSD": {"XAUUSD", 3, "métal", "XAU", "USD"},
	"XAGUSD": {"XAGUSD", 3, "métal", "XAG", "USD"},
	// Indices (historique Dukascopy souvent plus court que le forex)
	"US500":  {"USA500IDXUSD", 3, "indice", "US500", "USD"},
	"US30":   {"USA30IDXUSD", 3, "indice", "US30", "USD"},
	"NAS100": {"USATECHIDXUSD", 3, "indice", "NAS100", "USD"},
	"DE40":   {"DEUIDXEUR", 3, "indice", "DE40", "EUR"},
	"UK100":  {"GBRIDXGBP", 3, "indice", "UK100", "GBP"},
	"JP225":  {"JPNIDXJPY", 3, "indice", "JP225", "JPY"},
}

// LookupInstrument renvoie la fiche d'un symbole (insensible à la casse).
func LookupInstrument(symbol string) (Instrument, error) {
	key := strings.ToUpper(strings.TrimSpace(symbol))
	inst, ok := Instruments[key]
	if !ok {
		return Instrument{}, fmt.Errorf("symbole inconnu %q. Supportés : %s",
			symbol, strings.Join(SupportedSymbols(), ", "))
	}
	return inst, nil
}

// SupportedSymbols liste les symboles connus, triés.
func SupportedSymbols() []string {
	out := make([]string, 0, len(Instruments))
	for k := range Instruments {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Conversion décrit comment ramener le notionnel et le P&L d'un symbole
// dans la devise du COMPTE.
//
// Le prix d'une paire XXXYYY s'exprime en YYY pour une unité de XXX. Le
// P&L d'un trade naît donc en YYY, et le notionnel d'une position de
// `q` unités vaut `q × prix` YYY. Trois cas :
//
//   - la devise du compte est la COTATION (USD pour EURUSD) : tout est
//     déjà dans la bonne devise ;
//   - la devise du compte est la BASE (USD pour USDJPY) : le notionnel
//     vaut `q` USD, et un P&L en yens se ramène en dollars en le divisant
//     par le prix courant ;
//   - aucune des deux (EURGBP sur un compte en dollars) : la conversion
//     exigerait un TAUX TIERS que le programme n'a pas. Le résultat reste
//     alors exprimé dans la devise de cotation, et Exact vaut false — ce
//     qui est dit à l'écran plutôt que masqué derrière un chiffre faux.
type Conversion struct {
	// AccountCurrency : devise du compte demandée.
	AccountCurrency string
	// Quote : devise dans laquelle naissent les P&L du symbole.
	Quote string
	// Exact : la conversion est-elle rigoureuse ?
	Exact bool
	// baseIsAccount : le compte est libellé dans la devise de BASE.
	baseIsAccount bool
}

// SplitByConversion partage une liste de symboles entre ceux dont le P&L
// se convertit EXACTEMENT vers la devise du compte et les autres.
//
// La distinction n'est pas cosmétique depuis que le dimensionnement au
// risque est actif par défaut : sur un symbole non convertible, le budget
// de risque n'est pas calculable et TOUTES les entrées sont refusées.
// Mieux vaut le lire au démarrage que le déduire d'une saison sans
// trades.
func SplitByConversion(symbols []string, accountCurrency string) (exact, inexact []string) {
	for _, sym := range symbols {
		if ConversionFor(sym, accountCurrency).Exact {
			exact = append(exact, sym)
		} else {
			inexact = append(inexact, sym)
		}
	}
	return exact, inexact
}

// ConversionFor calcule la conversion d'un symbole vers une devise de compte.
// Un symbole inconnu retombe sur « cotation = devise du compte », signalé
// comme non exact.
func ConversionFor(symbol, accountCurrency string) Conversion {
	acc := strings.ToUpper(strings.TrimSpace(accountCurrency))
	if acc == "" {
		acc = "USD"
	}
	inst, err := LookupInstrument(symbol)
	if err != nil {
		return Conversion{AccountCurrency: acc, Quote: acc, Exact: false}
	}
	switch acc {
	case inst.Quote:
		return Conversion{AccountCurrency: acc, Quote: inst.Quote, Exact: true}
	case inst.Base:
		return Conversion{AccountCurrency: acc, Quote: inst.Quote, Exact: true, baseIsAccount: true}
	default:
		return Conversion{AccountCurrency: acc, Quote: inst.Quote, Exact: false}
	}
}

// NotionalPerUnit : valeur d'UNE unité de position, dans la devise du
// compte, au prix donné.
func (c Conversion) NotionalPerUnit(price float64) float64 {
	if c.baseIsAccount {
		return 1
	}
	return price
}

// ToAccount convertit un montant né en devise de COTATION vers la devise
// du compte, au prix donné.
func (c Conversion) ToAccount(amountInQuote, price float64) float64 {
	if c.baseIsAccount && price > 0 {
		return amountInQuote / price
	}
	return amountInQuote
}
