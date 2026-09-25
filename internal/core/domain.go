package core

import "time"

// OrderSide : sens d'un ordre.
type OrderSide string

const (
	Buy  OrderSide = "BUY"
	Sell OrderSide = "SELL"
)

// Opposite renvoie le sens inverse (sortie d'une position).
func (s OrderSide) Opposite() OrderSide {
	if s == Buy {
		return Sell
	}
	return Buy
}

// OrderType : nature de l'ordre transmis au broker.
type OrderType string

const (
	Market OrderType = "MARKET"
	Limit  OrderType = "LIMIT"
	Stop   OrderType = "STOP"
)

// SignalAction : décision brute d'une stratégie, AVANT contrôle du risque.
type SignalAction string

const (
	Hold       SignalAction = "HOLD"
	EnterLong  SignalAction = "ENTER_LONG"
	EnterShort SignalAction = "ENTER_SHORT"
	Exit       SignalAction = "EXIT"
	// ExitLong / ExitShort : sorties ORIENTÉES — ne ferment qu'une
	// position longue (resp. courte). Une stratégie sans état ne sait pas
	// dans quel sens le moteur est positionné ; un stop suiveur, lui, ne
	// vaut que pour un sens : « le prix a reculé de 3 ATR sous son plus
	// haut » condamne une position longue, pas une courte.
	ExitLong  SignalAction = "EXIT_LONG"
	ExitShort SignalAction = "EXIT_SHORT"
)

// IsEntry indique si l'action ouvre une position.
func (a SignalAction) IsEntry() bool { return a == EnterLong || a == EnterShort }

// IsExit indique si l'action ferme une position (orientée ou non).
func (a SignalAction) IsExit() bool { return a == Exit || a == ExitLong || a == ExitShort }

// Closes : l'action ferme-t-elle une position de quantité signée `qty` ?
func (a SignalAction) Closes(qty float64) bool {
	switch a {
	case Exit:
		return qty != 0
	case ExitLong:
		return qty > 0
	case ExitShort:
		return qty < 0
	}
	return false
}

// Tick est un point de marché reçu d'une gateway.
type Tick struct {
	Symbol string
	Bid    float64
	Ask    float64
	Volume float64
	Time   time.Time
}

// Price renvoie le prix de référence (bid), celui sur lequel les bougies
// live sont agrégées — cohérent avec l'historique, lui aussi en bid.
func (t Tick) Price() float64 { return t.Bid }

// Signal est la décision émise par une stratégie, à valider par le risque.
//
// StopLoss / TakeProfit sont les niveaux de PRIX des barrières proposées
// (chez Colibri : à ± k × ATR). Une stratégie sans barrières les laisse à
// zéro ; le RiskManager les reporte tels quels sur l'ordre. La barrière
// VERTICALE, elle, n'est pas portée par le signal : c'est une propriété
// de la stratégie, déclarée une fois (strategy.Description.MaxHold).
type Signal struct {
	Strategy   string
	Symbol     string
	Action     SignalAction
	Confidence float64 // Probabilité brute du modèle, 0 si non applicable.
	// Price : prix de RÉFÉRENCE de la décision (le close de la bougie
	// décidée). Sans lui, le risque ne peut pas mesurer la distance
	// jusqu'au stop, donc pas dimensionner une position au risque : un
	// stop seul ne dit rien tant qu'on ignore d'où l'on part.
	Price      float64
	StopLoss   float64
	TakeProfit float64
	Time       time.Time
}

// OrderRequest est un ordre VALIDÉ par le risque, prêt pour le broker.
// C'est le seul type qu'une gateway accepte : aucun ordre ne peut naître
// ailleurs que dans risk.Manager.
type OrderRequest struct {
	Symbol     string
	Side       OrderSide
	Quantity   float64
	Type       OrderType
	LimitPrice float64
	StopLoss   float64
	TakeProfit float64
}

// Position telle que RAPPORTÉE par le broker (jamais déduite).
// Quantity est signée : positive = long, négative = short.
type Position struct {
	Symbol        string
	Quantity      float64
	AveragePrice  float64
	UnrealizedPnL float64
	// UnrealizedKnown : le courtier a-t-il RAPPORTÉ le P&L latent ? Le flux
	// de positions d'Interactive Brokers ne le donne pas ; le recalculer
	// ici supposerait une conversion de devise et des frais qui nous
	// échappent. L'interface affiche alors « — », jamais un zéro.
	UnrealizedKnown bool
}

// IsLong indique le sens de la position.
func (p Position) IsLong() bool { return p.Quantity > 0 }

// AccountState : état du compte chez le broker à l'instant d'une décision.
//
// Equity vient du broker (jamais estimée) ; DayStartEquity est le repère
// persisté du début de journée UTC. Ensemble ils donnent la perte du jour,
// seule mesure honnête pour la limite MaxDailyLossPct.
type AccountState struct {
	Equity         float64
	DayStartEquity float64
	Margin         float64
	Currency       string
}

// DayLossPct renvoie la perte du jour en % (positive = perte) et false si
// elle n'est pas calculable (pas de repère de début de journée).
func (a AccountState) DayLossPct() (float64, bool) {
	if a.DayStartEquity == 0 {
		return 0, false
	}
	return (a.DayStartEquity - a.Equity) / a.DayStartEquity * 100.0, true
}

// ExecutionStatus : sort d'un ordre tel que rapporté par le broker.
// Les trois états sont TERMINAUX : l'ordre ne bougera plus, il libère donc
// sa réservation d'« ordre en vol » côté moteur live.
type ExecutionStatus string

const (
	Filled    ExecutionStatus = "FILLED"
	Cancelled ExecutionStatus = "CANCELLED"
	Rejected  ExecutionStatus = "REJECTED"
)

// ExecutionReport referme la boucle du flux live : PlaceOrder ne fait que
// SOUMETTRE, le broker exécute (ou refuse), et la gateway republie ici ce
// qui s'est réellement passé.
//
// Golden Waterfall ne fabrique JAMAIS d'ExecutionReport : il naît d'un
// callback broker. Un broker incapable de rapporter ses exécutions
// n'alimente aucun trade — aucun trade n'est inventé pour combler le vide.
//
// PnL n'est renseigné que sur une SORTIE (le broker le calcule) ; il reste
// à zéro sur une entrée, et Realized le dit explicitement.
type ExecutionReport struct {
	OrderID   string
	Symbol    string
	Side      OrderSide
	Quantity  float64
	Status    ExecutionStatus
	FillPrice float64
	PnL       float64
	Realized  bool // true si PnL est significatif (sortie de position).
	// Closing : l'exécution RÉDUIT une position (sortie, stop, limite).
	// Sans ce drapeau, le moteur ne peut distinguer une sortie dont il n'a
	// jamais vu l'entrée — position ouverte avant le démarrage — d'une
	// nouvelle entrée, et il l'enregistrerait comme telle.
	Closing bool
	Reason  string
	Time    time.Time
}

// Trade est un aller-retour complet, journalisé après coup.
type Trade struct {
	ID         int64
	Symbol     string
	Strategy   string
	Side       OrderSide // sens de l'OUVERTURE
	Quantity   float64
	EntryTime  time.Time
	EntryPrice float64
	ExitTime   time.Time
	ExitPrice  float64
	PnL        float64
	Cost       float64 // spread + commission imputés à l'aller-retour
	ExitReason string  // "tp", "sl", "time", "weekend", "final", "signal"
}

// Duration du maintien de la position.
func (t Trade) Duration() time.Duration { return t.ExitTime.Sub(t.EntryTime) }

// IsWin indique un aller-retour gagnant NET de coûts.
func (t Trade) IsWin() bool { return t.PnL > 0 }
