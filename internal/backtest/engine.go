// Package backtest rejoue un historique à travers le MÊME chemin que le
// live : Strategy → Signal → risk.Manager → OrderRequest → exécution.
//
// Le moteur est écrit sur mesure plutôt qu'emprunté à une bibliothèque
// généraliste, pour trois raisons : la promesse « un seul binaire » interdit
// d'embarquer un moteur tiers, le modèle d'exécution dont le projet a besoin
// tient en quelques centaines de lignes, et chaque hypothèse de remplissage
// devient EXPLICITE au lieu d'être un réglage obscur enfoui dans une
// dépendance.
//
// # Modèle d'exécution
//
//   - ENTRÉE au CLOSE de la bougie de décision. La stratégie décide à ce
//     close, l'ordre est rempli à ce même close.
//   - TRIPLE BARRIÈRE : stop et limite sont liés en OCO dès qu'une bougie
//     SUIVANTE franchit le high/low. Si les deux sont franchies dans la
//     MÊME bougie, le STOP l'emporte — l'ordre intrabar réel est inconnu,
//     on se pénalise.
//   - GAP : un stop est un ordre AU MARCHÉ une fois déclenché. Quand la
//     bougie OUVRE déjà au-delà du stop, il est rempli à l'ouverture, pas
//     au prix demandé, qu'aucun courtier n'aurait servi. La limite, elle,
//     est toujours remplie à son prix exact : un ordre à cours limité ne
//     s'exécute jamais moins bien, et lui accorder le gap serait
//     s'attribuer une chance qu'on ne peut pas prouver.
//   - BARRIÈRE VERTICALE : la position est liquidée au close de la
//     DERNIÈRE bougie commencée dans l'horizon que la stratégie DÉCLARE
//     (`Description.MaxHold`). Le moteur ne connaît pas cet horizon : il
//     le lit, si bien qu'aucune stratégie n'est câblée ici.
//   - CLÔTURE DE FIN DE SEMAINE ISO au close, pour une stratégie qui ne
//     déclare pas porter ses positions le week-end
//     (`Description.HoldsOverWeekend`) : aucun portage de week-end, et
//     donc AUCUNE ENTRÉE sur la dernière bougie de la semaine — elle
//     serait portée tout le week-end, la seule chose que cette règle
//     interdit. Une stratégie qui porte le week-end garde sa position ;
//     un stop franchi par le gap de réouverture est servi à l'ouverture.
//   - SORTIE SUR SIGNAL au close de la bougie de décision : un signal
//     `Exit`, ou une entrée opposée pour une stratégie qui déclare
//     `ExitOnReversal`. La bougie de sortie ne rouvre rien.
//   - LIQUIDATION FINALE au close de la dernière bougie : aucune position
//     résiduelle fantôme.
//   - COÛTS : le spread est MESURÉ dans les données (médiane de
//     ask_close − bid_close) et facturé par côté, si bien qu'un
//     aller-retour paie exactement un spread ; s'y ajoute une commission
//     optionnelle. Sans côté ask dans l'historique, AUCUN coût n'est
//     modélisé et le résultat le SIGNALE (CostsModelled = false) plutôt
//     que d'afficher un zéro trompeur.
//   - LEVIER : une entrée immobilise notionnel / levier. Sans levier, une
//     entrée longue dont la taille dépasse le capital serait refusée alors
//     qu'une vente passerait toujours — un biais directionnel invisible.
//     Tout refus est COMPTÉ (RejectedOrders) : une exécution ratée ne peut
//     pas être présentée comme une abstention du modèle.
package backtest

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/risk"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// MaxEquityPoints borne la courbe renvoyée à l'interface : au-delà, un
// terminal n'affiche pas plus d'information, il consomme juste plus de
// mémoire.
const MaxEquityPoints = 1000

// Request : un backtest sur un symbole.
type Request struct {
	Symbol string
	// Series : contexte de chauffe SUIVI du bloc à évaluer.
	Series core.Series
	// From : premier indice RÉELLEMENT évalué. Les bougies précédentes ne
	// servent qu'à stabiliser features et indicateurs.
	From      int
	Strategy  strategy.Strategy
	Timeframe data.Timeframe
}

// EquityPoint : un point de la courbe de VALEUR DU COMPTE.
type EquityPoint struct {
	Time  time.Time `json:"t"`
	Value float64   `json:"v"`
}

// Stats agrège les métriques d'un backtest. Chaque champ est MESURÉ ;
// aucun n'est estimé ni extrapolé.
type Stats struct {
	Symbol         string  `json:"symbol"`
	Bars           int     `json:"bars"`
	Trades         int     `json:"trades"`
	Wins           int     `json:"wins"`
	Losses         int     `json:"losses"`
	WinRate        float64 `json:"win_rate"`
	NetPnL         float64 `json:"net_pnl"`
	GrossProfit    float64 `json:"gross_profit"`
	GrossLoss      float64 `json:"gross_loss"`
	ProfitFactor   float64 `json:"profit_factor"`
	Costs          float64 `json:"costs"`
	CostsModelled  bool    `json:"costs_modelled"`
	Spread         float64 `json:"spread"`
	InitialCapital float64 `json:"initial_capital"`
	FinalEquity    float64 `json:"final_equity"`
	ReturnPct      float64 `json:"return_pct"`
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	Sharpe         float64 `json:"sharpe"`
	SQN            float64 `json:"sqn"`
	Expectancy     float64 `json:"expectancy"`
	AvgWin         float64 `json:"avg_win"`
	AvgLoss        float64 `json:"avg_loss"`
	RejectedOrders int     `json:"rejected_orders"`
	// SizeCapped : entrées dont la taille a été RAMENÉE au plafond
	// `max_position_size`. L'ordre est parti, mais il ne risquait plus le
	// pourcentage demandé : sans ce compteur, un plafond trop bas
	// neutraliserait le dimensionnement au risque sans que rien ne le
	// dise, et l'écran afficherait « 0,5 % par trade » en toute bonne foi.
	SizeCapped  int            `json:"size_capped"`
	Rejections  map[string]int `json:"rejections,omitempty"`
	ExitReasons map[string]int `json:"exit_reasons,omitempty"`
	Start       time.Time      `json:"start"`
	End         time.Time      `json:"end"`

	// Currency : devise dans laquelle les montants sont exprimés.
	Currency string `json:"currency"`
	// CurrencyExact : false quand la conversion vers la devise du compte
	// aurait exigé un taux TIERS dont le programme ne dispose pas (une
	// croisée EURGBP sur un compte en dollars). Les montants restent alors
	// en devise de cotation — c'est dit, jamais agrégé en silence.
	CurrencyExact bool `json:"currency_exact"`
}

// Result : sortie complète d'un backtest.
type Result struct {
	Stats  Stats
	Trades []core.Trade
	Equity []EquityPoint
}

// Engine exécute les backtests.
type Engine struct {
	cfg  config.Config
	risk *risk.Manager
}

// NewEngine construit un moteur. Le gestionnaire de risque est le MÊME
// type qu'en live : un signal rejeté en backtest l'aurait été en réel.
func NewEngine(cfg config.Config, rm *risk.Manager) *Engine {
	return &Engine{cfg: cfg, risk: rm}
}

// MeasureSpread renvoie le spread MÉDIAN observé (ask_close − bid_close).
//
// Retourne false si la série n'a pas de côté ask : dans ce cas AUCUN
// spread n'est modélisé, et le résultat le dit. La mesure elle-même vit
// dans `core` : une stratégie qui veut étiqueter sa cible NETTE de coûts
// doit facturer exactement le spread que ce moteur facturera.
func MeasureSpread(series core.Series) (float64, bool) { return series.MedianSpread() }

// position : état interne d'une position simulée.
type position struct {
	side       core.OrderSide
	quantity   float64
	entryPrice float64
	entryTime  time.Time
	entryIndex int
	stopLoss   float64
	takeProfit float64
	entryCost  float64
	// deadline : instant au-delà duquel la barrière VERTICALE liquide la
	// position (entrée + horizon déclaré par la stratégie). Zéro = aucune.
	deadline time.Time
}

func (p *position) unrealized(price float64) float64 {
	if p.side == core.Buy {
		return (price - p.entryPrice) * p.quantity
	}
	return (p.entryPrice - price) * p.quantity
}

// Run exécute le backtest.
func (e *Engine) Run(ctx context.Context, req Request) (*Result, error) {
	series := req.Series
	if len(series) == 0 {
		return nil, fmt.Errorf("série vide pour %s", req.Symbol)
	}
	from := req.From
	if from < 0 {
		from = 0
	}
	if from >= len(series) {
		return nil, fmt.Errorf("aucune bougie à évaluer pour %s (contexte %d >= série %d)",
			req.Symbol, from, len(series))
	}

	if err := req.Strategy.Warmup(ctx, strategy.WarmupRequest{
		Symbol:    req.Symbol,
		Series:    series,
		Timeframe: req.Timeframe,
	}); err != nil {
		return nil, fmt.Errorf("chauffe de la stratégie sur %s : %w", req.Symbol, err)
	}

	// Conversion de devise : le prix d'une paire XXXYYY est en YYY, donc
	// le notionnel et le P&L y naissent aussi. Sans cette conversion, un
	// compte en dollars jugeait la marge d'une position USDJPY sur un
	// notionnel en yens — cent cinquante fois trop grand — et refusait
	// TOUTES les entrées de la paire. Le compteur d'ordres refusés l'avait
	// rendu visible ; voici le correctif.
	conv := data.ConversionFor(req.Symbol, e.cfg.Backtest.AccountCurrency)

	spread, costsModelled := MeasureSpread(series)
	// Coût PAR CÔTÉ : la moitié du spread, plus la commission. Un
	// aller-retour paie donc exactement un spread et deux commissions.
	costPerUnitPerSide := e.cfg.Costs.CommissionPerUnit
	if costsModelled {
		costPerUnitPerSide += spread / 2
	}

	// Compteurs de rejet PROPRES à ce run : le Manager câblé dans app.New
	// est partagé par tous les backtests et par le live (cf. Fork).
	rm := e.risk.Fork()

	// Durée nominale d'une bougie : elle sert à reconnaître la DERNIÈRE
	// bougie commencée dans l'horizon, sans jamais lire l'horodatage de la
	// bougie suivante — le moteur live ne l'aurait pas.
	barDuration := req.Timeframe.Duration()
	desc := req.Strategy.Describe()
	maxHold := desc.MaxHold

	// Une stratégie qui porte ses positions le week-end n'a pas de
	// dernière bougie de semaine : ni clôture forcée, ni entrée refusée.
	weekEnd := make([]bool, len(series))
	if !desc.HoldsOverWeekend {
		weekEnd = core.LastBarsOfWeek(series)
	}
	cash := e.cfg.Backtest.InitialCapital
	leverage := e.cfg.Backtest.Leverage
	var pos *position
	var trades []core.Trade
	var equity []EquityPoint
	var totalCosts float64
	rejectedOrders := 0
	exitReasons := map[string]int{}

	for i := from; i < len(series); i++ {
		if i%4096 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
		bar := series[i]

		// 1. Barrières — évaluées sur les bougies SUIVANT l'entrée.
		if pos != nil && i > pos.entryIndex {
			if price, reason, hit := checkBarriers(pos, bar); hit {
				tr, cost := closePosition(pos, req.Symbol, price, bar.Time, reason, costPerUnitPerSide, conv)
				cash += tr.PnL
				totalCosts += cost
				trades = append(trades, tr)
				exitReasons[reason]++
				pos = nil
			}
		}

		// 2. Barrière VERTICALE — testée APRÈS les horizontales, comme à
		// l'étiquetage : sur la bougie d'échéance, un stop ou une limite
		// touchés l'emportent encore.
		if pos != nil && i > pos.entryIndex && core.HoldExpired(bar.Time, barDuration, pos.deadline) {
			tr, cost := closePosition(pos, req.Symbol, bar.Close(), bar.Time, "time", costPerUnitPerSide, conv)
			cash += tr.PnL
			totalCosts += cost
			trades = append(trades, tr)
			exitReasons["time"]++
			pos = nil
		}

		forcedExit := false
		// 3. Clôture de fin de semaine ISO et liquidation finale, au close.
		if pos != nil && (weekEnd[i] || i == len(series)-1) {
			reason := "weekend"
			if i == len(series)-1 {
				reason = "final"
			}
			tr, cost := closePosition(pos, req.Symbol, bar.Close(), bar.Time, reason, costPerUnitPerSide, conv)
			cash += tr.PnL
			totalCosts += cost
			trades = append(trades, tr)
			exitReasons[reason]++
			pos = nil
			forcedExit = true
		}

		// 4. Décision. Une bougie de clôture forcée ne rouvre RIEN : ce
		// serait reprendre immédiatement le risque qu'on vient de couper.
		// La dernière bougie de la semaine non plus, même sans position à
		// fermer : l'entrée serait portée tout le week-end, puisque la
		// clôture de fin de semaine ne se rejoue qu'à la semaine suivante.
		if !forcedExit && !weekEnd[i] && i < len(series)-1 {
			sig, err := req.Strategy.OnBar(ctx, req.Symbol, series, i)
			if err != nil {
				return nil, fmt.Errorf("stratégie sur %s à %s : %w", req.Symbol, bar.Time, err)
			}
			open := currentPositions(pos, req.Symbol)
			held := 0.0
			if len(open) > 0 {
				held = open[0].Quantity
			}
			reversal := sig.Action.IsEntry()
			sig = strategy.ApplyReversal(desc, sig, held)
			reversal = reversal && sig.Action == core.Exit
			// L'équité courante est fournie pour que le dimensionnement au
			// risque marche IDENTIQUEMENT ici et en live. DayStartEquity
			// reste à zéro : la limite de perte journalière exige l'équité
			// réelle du courtier et n'est donc pas modélisée — c'est dit,
			// jamais simulé de travers.
			equity := cash
			if pos != nil {
				equity += conv.ToAccount(pos.unrealized(bar.Close()), bar.Close())
			}
			dec := rm.Evaluate(sig, open, &core.AccountState{
				Equity: equity, Currency: conv.AccountCurrency,
			})
			if dec.Accepted() && sig.Action == core.Exit && pos != nil {
				// Sortie demandée par la stratégie, au close de la bougie
				// de décision — le prix auquel elle a décidé. Le live
				// l'exécute au marché à la clôture de la même bougie.
				reason := "signal"
				if reversal {
					reason = "reversal"
				}
				tr, cost := closePosition(pos, req.Symbol, bar.Close(), bar.Time, reason, costPerUnitPerSide, conv)
				cash += tr.PnL
				totalCosts += cost
				trades = append(trades, tr)
				exitReasons[reason]++
				pos = nil
			} else if dec.Accepted() && sig.Action.IsEntry() && pos == nil {
				entryPrice := bar.Close()
				notional := dec.Order.Quantity * conv.NotionalPerUnit(entryPrice)
				margin := notional / leverage
				accountValue := cash
				if margin > accountValue {
					// Marge insuffisante : refus COMPTÉ, jamais silencieux.
					rejectedOrders++
				} else {
					cost := conv.ToAccount(dec.Order.Quantity*costPerUnitPerSide, entryPrice)
					cash -= cost
					totalCosts += cost
					pos = &position{
						side:       dec.Order.Side,
						quantity:   dec.Order.Quantity,
						entryPrice: entryPrice,
						entryTime:  bar.Time,
						entryIndex: i,
						stopLoss:   dec.Order.StopLoss,
						takeProfit: dec.Order.TakeProfit,
						entryCost:  cost,
						deadline:   core.HoldDeadline(bar.Time, maxHold),
					}
				}
			}
		}

		value := cash
		if pos != nil {
			value += conv.ToAccount(pos.unrealized(bar.Close()), bar.Close())
		}
		equity = append(equity, EquityPoint{Time: bar.Time, Value: value})
	}

	stats := computeStats(req, trades, equity, e.cfg.Backtest.InitialCapital,
		totalCosts, spread, costsModelled, rejectedOrders, exitReasons, rm.Rejections())
	stats.SizeCapped = rm.Capped()
	stats.Currency, stats.CurrencyExact = conv.AccountCurrency, conv.Exact
	if !conv.Exact {
		// Non convertible : on n'invente pas un taux, on dit dans quelle
		// devise le chiffre est réellement exprimé.
		stats.Currency = conv.Quote
	}
	return &Result{Stats: stats, Trades: trades, Equity: Downsample(equity, MaxEquityPoints)}, nil
}

func currentPositions(pos *position, symbol string) []core.Position {
	if pos == nil {
		return nil
	}
	qty := pos.quantity
	if pos.side == core.Sell {
		qty = -qty
	}
	return []core.Position{{Symbol: symbol, Quantity: qty, AveragePrice: pos.entryPrice}}
}

// checkBarriers teste le franchissement intrabar.
//
// Ordre de test VOLONTAIRE : le stop d'abord. Quand une bougie franchit
// les deux barrières, la séquence intrabar réelle est inconnue ; retenir
// le stop pénalise le résultat, ce qui est la seule hypothèse honnête.
// C'est aussi exactement la convention du labeling, si bien que le modèle
// apprend la cible que l'exécution délivre.
func checkBarriers(pos *position, bar core.Bar) (price float64, reason string, hit bool) {
	high, low, open := bar.High(), bar.Low(), bar.Open()
	if pos.side == core.Buy {
		if pos.stopLoss > 0 && low <= pos.stopLoss {
			// Gap : le marché a ouvert SOUS le stop. Le déclenchement
			// donne un ordre au marché, servi à l'ouverture — pas au prix
			// demandé, que personne n'offrait plus.
			return math.Min(pos.stopLoss, open), "sl", true
		}
		if pos.takeProfit > 0 && high >= pos.takeProfit {
			return pos.takeProfit, "tp", true
		}
		return 0, "", false
	}
	if pos.stopLoss > 0 && high >= pos.stopLoss {
		return math.Max(pos.stopLoss, open), "sl", true
	}
	if pos.takeProfit > 0 && low <= pos.takeProfit {
		return pos.takeProfit, "tp", true
	}
	return 0, "", false
}

func closePosition(pos *position, symbol string, price float64, when time.Time,
	reason string, costPerUnitPerSide float64, conv data.Conversion) (core.Trade, float64) {

	exitCost := conv.ToAccount(pos.quantity*costPerUnitPerSide, price)
	gross := conv.ToAccount(pos.unrealized(price), price)
	return core.Trade{
		Symbol:     symbol,
		Side:       pos.side,
		Quantity:   pos.quantity,
		EntryTime:  pos.entryTime,
		EntryPrice: pos.entryPrice,
		ExitTime:   when,
		ExitPrice:  price,
		// PnL NET : le coût d'entrée a déjà été retiré de la trésorerie à
		// l'ouverture, on ne retire ici que celui de la sortie. Le trade
		// porte en revanche le coût COMPLET de l'aller-retour, pour que
		// « gagnant » veuille dire gagnant net.
		PnL:        gross - exitCost - pos.entryCost,
		Cost:       exitCost + pos.entryCost,
		ExitReason: reason,
	}, exitCost
}

// Downsample réduit une courbe à au plus `max` points en conservant le
// premier, le dernier et un échantillonnage régulier entre les deux.
func Downsample(points []EquityPoint, max int) []EquityPoint {
	if max < 2 || len(points) <= max {
		return points
	}
	out := make([]EquityPoint, 0, max)
	step := float64(len(points)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx >= len(points) {
			idx = len(points) - 1
		}
		out = append(out, points[idx])
	}
	return out
}
