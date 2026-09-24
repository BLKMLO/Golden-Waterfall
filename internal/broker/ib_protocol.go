package broker

// Messages du protocole TWS utilisés par la passerelle — et SEULEMENT
// eux. Chaque encodeur reprend, champ pour champ, la fonction homonyme de
// client.py (API 10.30) ; chaque décodeur, la fonction process…Msg de
// decoder.py. Les numéros de message viennent de message.py.

import (
	"fmt"
	"strconv"
	"strings"
)

// Messages sortants (message.py, classe OUT).
const (
	ibOutReqMktData           = 1
	ibOutCancelMktData        = 2
	ibOutPlaceOrder           = 3
	ibOutCancelOrder          = 4
	ibOutReqIDs               = 8
	ibOutReqPositions         = 61
	ibOutReqAccountSummary    = 62
	ibOutCancelAccountSummary = 63
	ibOutCancelPositions      = 64
	ibOutStartAPI             = 71
)

// Messages entrants (message.py, classe IN).
const (
	ibInTickPrice         = 1
	ibInOrderStatus       = 3
	ibInErrMsg            = 4
	ibInNextValidID       = 9
	ibInExecutionData     = 11
	ibInManagedAccounts   = 15
	ibInCommissionReport  = 59
	ibInPositionData      = 61
	ibInPositionEnd       = 62
	ibInAccountSummary    = 63
	ibInAccountSummaryEnd = 64
)

// Types de tick de prix (ticktype.py) : temps réel, puis différé.
const (
	ibTickBid        = 1
	ibTickAsk        = 2
	ibTickDelayedBid = 66
	ibTickDelayedAsk = 67
)

// ibContract : le sous-ensemble d'un Contract IB que ce programme remplit.
// Tous les autres champs partent à leur valeur par défaut, exactement
// comme un Contract() neuf du client officiel.
type ibContract struct {
	Symbol   string // devise de BASE pour le forex (« EUR »)
	SecType  string // « CASH »
	Exchange string // « IDEALPRO »
	Currency string // devise de COTATION (« USD »)
}

// ibOrder : le sous-ensemble d'un Order IB que ce programme remplit. Même
// règle : le reste vaut ce que vaut un Order() neuf.
type ibOrder struct {
	Action    string // BUY / SELL
	Quantity  int64
	OrderType string // MKT / LMT / STP
	LmtPrice  float64
	AuxPrice  float64
	TIF       string
	Account   string
	OrderRef  string
	Transmit  bool
	ParentID  int64
}

// newIBOrder renvoie un ordre aux valeurs par défaut du client officiel :
// prix non renseignés, transmission immédiate.
func newIBOrder() ibOrder {
	return ibOrder{LmtPrice: ibUnsetFloat, AuxPrice: ibUnsetFloat, Transmit: true}
}

// --- Encodeurs --------------------------------------------------------

func ibStartAPI(clientID int64) []byte {
	m := &ibMsg{}
	m.int(ibOutStartAPI).int(2).int(clientID)
	m.str("") // optionalCapabilities (serveur ≥ 72)
	return m.b
}

func ibReqIDs(n int64) []byte {
	m := &ibMsg{}
	m.int(ibOutReqIDs).int(1).int(n)
	return m.b
}

// contractFields : bloc commun à reqMktData et placeOrder (conId, puis les
// champs du contrat, puis tradingClass, serveur ≥ 68).
func (m *ibMsg) contractFields(c ibContract) *ibMsg {
	m.int(0) // conId
	m.str(c.Symbol).str(c.SecType)
	m.str("")  // lastTradeDateOrContractMonth
	m.float(0) // strike
	m.str("")  // right
	m.str("")  // multiplier
	m.str(c.Exchange)
	m.str("") // primaryExchange
	m.str(c.Currency)
	m.str("") // localSymbol
	m.str("") // tradingClass
	return m
}

func ibReqMktData(reqID int64, c ibContract) []byte {
	m := &ibMsg{}
	m.int(ibOutReqMktData).int(11).int(reqID)
	m.contractFields(c)
	m.bool(false) // deltaNeutralContract absent
	m.str("")     // genericTickList
	m.bool(false) // snapshot
	m.bool(false) // regulatorySnapshot (serveur ≥ 114)
	m.str("")     // mktDataOptions (serveur ≥ 70)
	return m.b
}

func ibCancelMktData(reqID int64) []byte {
	m := &ibMsg{}
	m.int(ibOutCancelMktData).int(2).int(reqID)
	return m.b
}

func ibReqAccountSummary(reqID int64, group, tags string) []byte {
	m := &ibMsg{}
	m.int(ibOutReqAccountSummary).int(1).int(reqID).str(group).str(tags)
	return m.b
}

func ibCancelAccountSummary(reqID int64) []byte {
	m := &ibMsg{}
	m.int(ibOutCancelAccountSummary).int(1).int(reqID)
	return m.b
}

func ibReqPositions() []byte {
	m := &ibMsg{}
	m.int(ibOutReqPositions).int(1)
	return m.b
}

func ibCancelPositions() []byte {
	m := &ibMsg{}
	m.int(ibOutCancelPositions).int(1)
	return m.b
}

// ibCancelOrder : cancelOrder(orderId, OrderCancel()) de client.py.
func ibCancelOrder(sv int, orderID int64) []byte {
	m := &ibMsg{}
	m.int(ibOutCancelOrder).int(1).int(orderID)
	if sv >= ibSvManualOrderTime {
		m.str("") // manualOrderCancelTime
	}
	if sv >= ibSvRFQFields {
		m.str("")         // extOperator
		m.str("")         // externalUserId
		m.int(ibUnsetInt) // manualOrderIndicator
	}
	return m.b
}

// ibPlaceOrder : placeOrder de client.py, pour un contrat simple (pas de
// combo, pas de delta neutre, pas d'algo, pas de condition, ni PEG) —
// seules formes que ce programme envoie. L'ordre des champs est celui du
// client officiel ; les commentaires nomment le champ, pas sa raison.
func ibPlaceOrder(sv int, orderID int64, c ibContract, o ibOrder) []byte {
	m := &ibMsg{}
	m.int(ibOutPlaceOrder)
	// Pas de champ VERSION : serveur ≥ 145 (ORDER_CONTAINER).
	m.int(orderID)
	m.contractFields(c)
	m.str("").str("") // secIdType, secId

	// Champs principaux.
	m.str(o.Action)
	m.int(o.Quantity) // Decimal : une quantité entière s'écrit sans décimale
	m.str(o.OrderType)
	m.floatMax(o.LmtPrice)
	m.floatMax(o.AuxPrice)

	// Champs étendus.
	m.str(o.TIF)
	m.str("") // ocaGroup
	m.str(o.Account)
	m.str("") // openClose
	m.int(0)  // origin = CUSTOMER
	m.str(o.OrderRef)
	m.bool(o.Transmit)
	m.int(o.ParentID)
	m.bool(false) // blockOrder
	m.bool(false) // sweepToFill
	m.int(0)      // displaySize
	m.int(0)      // triggerMethod
	m.bool(false) // outsideRth
	m.bool(false) // hidden

	m.str("")                 // sharesAllocation (obsolète)
	m.int(0)                  // discretionaryAmt
	m.str("")                 // goodAfterTime
	m.str("")                 // goodTillDate
	m.str("").str("").str("") // faGroup, faMethod, faPercentage
	if sv < ibSvFAProfileDesupport {
		m.str("") // faProfile (obsolète)
	}
	m.str("")                 // modelCode
	m.int(0)                  // shortSaleSlot
	m.str("")                 // designatedLocation
	m.int(-1)                 // exemptCode
	m.int(0)                  // ocaType
	m.str("")                 // rule80A
	m.str("")                 // settlingFirm
	m.bool(false)             // allOrNone
	m.intMax(ibUnsetInt)      // minQty
	m.floatMax(ibUnsetFloat)  // percentOffset
	m.bool(false)             // eTradeOnly (obsolète)
	m.bool(false)             // firmQuoteOnly (obsolète)
	m.floatMax(ibUnsetFloat)  // nbboPriceCap (obsolète)
	m.int(0)                  // auctionStrategy
	m.floatMax(ibUnsetFloat)  // startingPrice
	m.floatMax(ibUnsetFloat)  // stockRefPrice
	m.floatMax(ibUnsetFloat)  // delta
	m.floatMax(ibUnsetFloat)  // stockRangeLower
	m.floatMax(ibUnsetFloat)  // stockRangeUpper
	m.bool(false)             // overridePercentageConstraints
	m.floatMax(ibUnsetFloat)  // volatility
	m.intMax(ibUnsetInt)      // volatilityType
	m.str("")                 // deltaNeutralOrderType
	m.floatMax(ibUnsetFloat)  // deltaNeutralAuxPrice
	m.bool(false)             // continuousUpdate
	m.intMax(ibUnsetInt)      // referencePriceType
	m.floatMax(ibUnsetFloat)  // trailStopPrice
	m.floatMax(ibUnsetFloat)  // trailingPercent
	m.intMax(ibUnsetInt)      // scaleInitLevelSize
	m.intMax(ibUnsetInt)      // scaleSubsLevelSize
	m.floatMax(ibUnsetFloat)  // scalePriceIncrement
	m.str("").str("").str("") // scaleTable, activeStartTime, activeStopTime
	m.str("")                 // hedgeType
	m.bool(false)             // optOutSmartRouting
	m.str("").str("")         // clearingAccount, clearingIntent
	m.bool(false)             // notHeld
	m.bool(false)             // deltaNeutralContract absent
	m.str("")                 // algoStrategy
	m.str("")                 // algoId
	m.bool(false)             // whatIf
	m.str("")                 // orderMiscOptions
	m.bool(false)             // solicited
	m.bool(false)             // randomizeSize
	m.bool(false)             // randomizePrice
	m.int(0)                  // nombre de conditions
	m.str("")                 // adjustedOrderType
	m.float(ibUnsetFloat)     // triggerPrice
	m.float(ibUnsetFloat)     // lmtPriceOffset
	m.float(ibUnsetFloat)     // adjustedStopPrice
	m.float(ibUnsetFloat)     // adjustedStopLimitPrice
	m.float(ibUnsetFloat)     // adjustedTrailingAmount
	m.int(0)                  // adjustableTrailingUnit
	m.str("")                 // extOperator
	m.str("").str("")         // softDollarTier name, value
	m.float(ibUnsetFloat)     // cashQty
	m.str("").str("")         // mifid2DecisionMaker, mifid2DecisionAlgo
	m.str("").str("")         // mifid2ExecutionTrader, mifid2ExecutionAlgo
	m.bool(false)             // dontUseAutoPriceForHedge
	m.bool(false)             // isOmsContainer
	m.bool(false)             // discretionaryUpToLimitPrice
	m.intMax(ibUnsetInt)      // usePriceMgmtAlgo
	m.int(ibUnsetInt)         // duration
	m.int(ibUnsetInt)         // postToAts
	m.bool(false)             // autoCancelParent
	if sv >= ibSvAdvancedOrderReject {
		m.str("") // advancedErrorOverride
	}
	if sv >= ibSvManualOrderTime {
		m.str("") // manualOrderTime
	}
	// Serveur ≥ 170 : rien à envoyer hors IBKRATS et ordres PEG.
	if sv >= ibSvCustomerAccount {
		m.str("") // customerAccount
	}
	if sv >= ibSvProfessionalCustomer {
		m.bool(false) // professionalCustomer
	}
	if sv >= ibSvRFQFields {
		m.str("")         // externalUserId
		m.int(ibUnsetInt) // manualOrderIndicator
	}
	return m.b
}

// --- Décodeurs --------------------------------------------------------

type ibTickPrice struct {
	ReqID    int64
	TickType int64
	Price    float64
}

type ibOrderStatus struct {
	OrderID      int64
	Status       string
	Filled       float64
	Remaining    float64
	AvgFillPrice float64
	ParentID     int64
	WhyHeld      string
}

type ibErrorMsg struct {
	ReqID int64
	Code  int64
	Msg   string
}

type ibNextValidID struct{ OrderID int64 }

type ibExecution struct {
	ReqID    int64
	OrderID  int64
	Symbol   string
	SecType  string
	Currency string
	ExecID   string
	Time     string
	Account  string
	Side     string // BOT / SLD
	Shares   float64
	Price    float64
}

type ibManagedAccounts struct{ Accounts []string }

type ibCommission struct {
	ExecID     string
	Commission float64
	Currency   string
	// RealizedPNL n'a de sens que si RealizedKnown : IB envoie la valeur
	// sentinelle sur une exécution qui ne ferme rien.
	RealizedPNL   float64
	RealizedKnown bool
}

type ibPosition struct {
	Account  string
	Symbol   string
	SecType  string
	Currency string
	Position float64
	AvgCost  float64
}

type ibPositionEnd struct{}

type ibAccountSummary struct {
	ReqID    int64
	Account  string
	Tag      string
	Value    string
	Currency string
}

type ibAccountSummaryEnd struct{ ReqID int64 }

// ibDecode transforme un message reçu en valeur typée. Un message que ce
// programme n'utilise pas renvoie (nil, nil) : TWS en envoie beaucoup
// (openOrder, tickSize…) et les ignorer n'est pas une erreur.
func ibDecode(sv int, fields []string) (any, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("identifiant de message TWS illisible %q", fields[0])
	}
	r := &ibFields{f: fields, i: 1}
	var out any
	switch id {
	case ibInTickPrice:
		// processTickPriceMsg : version, reqId, tickType, price, size, attrMask.
		r.skip(1)
		t := ibTickPrice{ReqID: r.int(), TickType: r.int(), Price: r.float()}
		out = t
	case ibInOrderStatus:
		// Serveur ≥ 131 : pas de champ version.
		s := ibOrderStatus{OrderID: r.int(), Status: r.str()}
		s.Filled, _ = r.decimal()
		s.Remaining, _ = r.decimal()
		s.AvgFillPrice = r.float()
		r.skip(1) // permId
		s.ParentID = r.int()
		r.skip(2) // lastFillPrice, clientId
		s.WhyHeld = r.str()
		out = s
	case ibInErrMsg:
		r.skip(1) // version
		out = ibErrorMsg{ReqID: r.int(), Code: r.int(), Msg: r.str()}
	case ibInNextValidID:
		r.skip(1)
		out = ibNextValidID{OrderID: r.int()}
	case ibInExecutionData:
		// Serveur ≥ 136 : pas de champ version, la version vaut celle du
		// serveur, donc tous les champs « ver ≥ n » sont présents.
		e := ibExecution{ReqID: r.int(), OrderID: r.int()}
		r.skip(1) // conId
		e.Symbol, e.SecType = r.str(), r.str()
		r.skip(4) // lastTradeDate, strike, right, multiplier
		r.skip(1) // exchange
		e.Currency = r.str()
		r.skip(2) // localSymbol, tradingClass
		e.ExecID, e.Time, e.Account = r.str(), r.str(), r.str()
		r.skip(1) // exchange d'exécution
		e.Side = r.str()
		e.Shares, _ = r.decimal()
		e.Price = r.float()
		// permId, clientId, liquidation, cumQty, avgPrice, orderRef,
		// evRule, evMultiplier, modelCode, lastLiquidity
		r.skip(10)
		if sv >= ibSvPendingPriceRevision {
			r.skip(1)
		}
		out = e
	case ibInManagedAccounts:
		r.skip(1)
		var accounts []string
		for _, a := range strings.Split(r.str(), ",") {
			if a = strings.TrimSpace(a); a != "" {
				accounts = append(accounts, a)
			}
		}
		out = ibManagedAccounts{Accounts: accounts}
	case ibInCommissionReport:
		r.skip(1)
		c := ibCommission{ExecID: r.str(), Commission: r.float(), Currency: r.str()}
		c.RealizedPNL = r.float()
		c.RealizedKnown = !ibIsUnset(c.RealizedPNL)
		r.skip(2) // yield, yieldRedemptionDate
		out = c
	case ibInPositionData:
		version := r.int()
		p := ibPosition{Account: r.str()}
		r.skip(1) // conId
		p.Symbol, p.SecType = r.str(), r.str()
		r.skip(4) // lastTradeDate, strike, right, multiplier
		r.skip(1) // exchange
		p.Currency = r.str()
		r.skip(1) // localSymbol
		if version >= 2 {
			r.skip(1) // tradingClass
		}
		p.Position, _ = r.decimal()
		if version >= 3 {
			p.AvgCost = r.float()
		}
		out = p
	case ibInPositionEnd:
		out = ibPositionEnd{}
	case ibInAccountSummary:
		r.skip(1)
		out = ibAccountSummary{ReqID: r.int(), Account: r.str(), Tag: r.str(), Value: r.str(), Currency: r.str()}
	case ibInAccountSummaryEnd:
		r.skip(1)
		out = ibAccountSummaryEnd{ReqID: r.int()}
	default:
		return nil, nil
	}
	if r.err != nil {
		return nil, fmt.Errorf("message TWS %d : %w", id, r.err)
	}
	return out, nil
}
