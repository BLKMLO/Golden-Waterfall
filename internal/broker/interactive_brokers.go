package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// ibGateway parle le protocole TWS à un TWS ou à un IB Gateway local.
//
// Ce qu'elle fait :
//   - vérifie, avant de se dire connectée, que le compte est celui qu'on
//     croit : papier si broker.mode vaut « paper », réel s'il vaut
//     « live », et dans la devise de backtest.account_currency ;
//   - soumet chaque entrée en BRACKET : parent au marché, limite et stop
//     attachés, en GTC. Les barrières vivent CHEZ le courtier et survivent
//     à un arrêt du programme ;
//   - rapporte ce que le courtier a fait, à partir de SES messages
//     (exécutions, statuts, commissions), jamais d'une supposition.
//
// Ce qu'elle ne fait pas : trader autre chose que le forex IDEALPRO (les
// métaux et indices sont refusés, pas approximés), ni se reconnecter
// seule après une coupure — l'écran Live le montre, l'utilisateur relance.
type ibGateway struct {
	opts Options
	// Délais figés à la construction : une minuterie armée ne relit
	// jamais une variable du paquet.
	startupTimeout, cancelTimeout, settleDelay, protectionGrace time.Duration

	onTick      atomic.Pointer[func(core.Tick)]
	onExecution atomic.Pointer[func(core.ExecutionReport)]

	// writeMu sérialise les écritures sur le socket : un message TWS
	// entrelacé avec un autre est un flux corrompu.
	writeMu sync.Mutex
	conn    net.Conn
	sv      int

	mu        sync.Mutex
	connected bool
	// linkLost : TWS est joignable mais a perdu SA connexion aux serveurs
	// d'IB (code 1100). Aucun ordre ne partirait : Connected() répond non.
	linkLost    bool
	lostReason  string
	nextID      int64
	accounts    []string
	account     string
	summary     map[string]ibValue
	summaryDone bool
	positions   map[string]ibHolding
	posDone     bool
	subs        map[int64]string // reqId → symbole
	quotes      map[string]*ibQuote
	orders      map[int64]*ibTrack
	execOrder   map[string]int64 // execId → ordre
	commissions map[string]ibCommission
	brackets    map[string]*ibBracket
	startup     chan struct{} // signalé à chaque étape de la mise en route
	startErr    error

	// File de livraison : ticks et comptes rendus partent d'UNE goroutine
	// dédiée, dans l'ordre d'arrivée. Jamais depuis la goroutine de
	// lecture : le moteur peut, depuis un tick, appeler PlaceOrder, qui
	// attend une confirmation que seule la lecture peut apporter.
	qmu   sync.Mutex
	queue []ibDelivery
	qsig  chan struct{}
	stop  chan struct{}
	wg    sync.WaitGroup
}

type ibValue struct {
	value    float64
	currency string
}

type ibHolding struct {
	quantity float64
	avgCost  float64
}

type ibQuote struct{ bid, ask float64 }

type ibDelivery struct {
	tick   *core.Tick
	report *core.ExecutionReport
}

type ibRole int

const (
	ibRoleEntry ibRole = iota // entrée simple ou parent d'un bracket
	ibRoleTakeProfit
	ibRoleStopLoss
	ibRoleExit
)

// ibTrack suit un ordre de sa soumission à son dernier compte rendu.
type ibTrack struct {
	id     int64
	symbol string
	side   core.OrderSide
	qty    float64
	role   ibRole
	reason string

	execIDs      []string
	execQty      float64
	execNotional float64
	execTime     time.Time

	statusFilled bool
	statusQty    float64
	statusAvg    float64
	lastErr      string

	cancelRequested bool
	filled          bool // exécution complète constatée
	finished        bool // plus rien ne sera rapporté
	settling        bool // attente des commissions en cours
	done            chan struct{}
}

// ibBracket : les trois ordres d'une entrée protégée, persistés pour
// survivre à un redémarrage — sans eux, une sortie ultérieure ne saurait
// pas quels ordres attachés annuler, et un stop orphelin pourrait rouvrir
// une position.
type ibBracket struct {
	Symbol     string         `json:"symbol"`
	Side       core.OrderSide `json:"side"`
	Quantity   float64        `json:"quantity"`
	Parent     int64          `json:"parent"`
	TakeProfit int64          `json:"take_profit,omitempty"`
	StopLoss   int64          `json:"stop_loss,omitempty"`
	Entered    bool           `json:"entered"`
	Closing    bool           `json:"closing"`
}

const (
	ibName = "interactive_brokers"
	// Identifiants de requête hors de l'espace des ordres.
	ibSummaryReqID = 9001
	ibMktDataBase  = 1001
)

// Délais du protocole. Des variables et non des constantes pour que les
// tests les raccourcissent ; rien d'autre ne les modifie.
var (
	ibStartupTimeout = 15 * time.Second
	ibCancelTimeout  = 10 * time.Second
	// ibSettleDelay : attente des rapports de commission après la dernière
	// exécution. Passé ce délai, le compte rendu part SANS P&L — et le dit.
	ibSettleDelay = 3 * time.Second
	// ibProtectionGrace : délai avant de conclure qu'une barrière annulée
	// l'a été sans que sa jumelle ne s'exécute. TWS annonce l'annulation
	// de la jumelle parfois AVANT l'exécution qui l'a provoquée.
	ibProtectionGrace = 5 * time.Second
)

func init() {
	Register(Info{
		Name:        ibName,
		Label:       "Interactive Brokers",
		Description: "TWS ou IB Gateway, protocole TWS. Forex IDEALPRO, entrées en bracket chez le courtier.",
		Simulated:   false,
		// Chaque entrée part en bracket : parent au marché, limite et stop
		// attachés (parentId), transmis ensemble — TWS les lie en OCA.
		SupportsBracket: true,
		Requirements: "TWS ou IB Gateway lancé et connecté ; API activée (Configure → API → Settings : " +
			"« Enable ActiveX and Socket Clients », « Read-Only API » décoché) ; port 7497 (TWS papier), " +
			"7496 (TWS réel), 4002/4001 (IB Gateway). Forex uniquement.",
	}, func(opts Options) (Gateway, error) {
		if opts.Host == "" || opts.Port <= 0 {
			return nil, fmt.Errorf("Interactive Brokers : broker.host et broker.port sont requis")
		}
		if opts.Mode != "paper" && opts.Mode != "live" {
			return nil, fmt.Errorf("Interactive Brokers : broker.mode doit valoir paper ou live (reçu %q)", opts.Mode)
		}
		return &ibGateway{opts: opts, startupTimeout: ibStartupTimeout, cancelTimeout: ibCancelTimeout,
			settleDelay: ibSettleDelay, protectionGrace: ibProtectionGrace}, nil
	})
}

func (g *ibGateway) Info() Info {
	for _, i := range List() {
		if i.Name == ibName {
			return i
		}
	}
	return Info{Name: ibName}
}

func (g *ibGateway) OnTick(f func(core.Tick))                 { g.onTick.Store(&f) }
func (g *ibGateway) OnExecution(f func(core.ExecutionReport)) { g.onExecution.Store(&f) }

// Connected répond oui seulement si le socket est ouvert, la mise en route
// vérifiée ET TWS relié aux serveurs d'IB.
func (g *ibGateway) Connected() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.connected && !g.linkLost
}

// --- Connexion ----------------------------------------------------------

// Connect ouvre le socket, négocie la version, puis ATTEND que TWS ait
// fourni l'identifiant d'ordre, les comptes, le résumé du compte et les
// positions. Chaque vérification qui échoue refuse la connexion en disant
// laquelle.
func (g *ibGateway) Connect(ctx context.Context) error {
	g.mu.Lock()
	if g.conn != nil {
		g.mu.Unlock()
		return fmt.Errorf("passerelle Interactive Brokers déjà connectée")
	}
	g.mu.Unlock()

	addr := net.JoinHostPort(g.opts.Host, strconv.Itoa(g.opts.Port))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("TWS / IB Gateway injoignable sur %s : %v — lancé ? API activée ? bon port ?", addr, err)
	}
	deadline := time.Now().Add(g.startupTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	reader := bufio.NewReader(conn)
	sv, err := ibNegotiate(conn, reader)
	if err != nil {
		conn.Close()
		return err
	}
	_ = conn.SetDeadline(time.Time{})

	g.mu.Lock()
	g.conn, g.sv = conn, sv
	g.connected, g.linkLost, g.lostReason = false, false, ""
	g.nextID, g.accounts, g.account = 0, nil, ""
	g.summary, g.summaryDone = map[string]ibValue{}, false
	g.positions, g.posDone = map[string]ibHolding{}, false
	g.subs, g.quotes = map[int64]string{}, map[string]*ibQuote{}
	g.orders, g.execOrder = map[int64]*ibTrack{}, map[string]int64{}
	g.commissions, g.brackets = map[string]ibCommission{}, map[string]*ibBracket{}
	g.startup, g.startErr = make(chan struct{}, 16), nil
	g.qsig, g.stop, g.queue = make(chan struct{}, 1), make(chan struct{}), nil
	g.mu.Unlock()

	g.wg.Add(2)
	go g.readLoop(conn, reader)
	go g.deliverLoop()

	fail := func(err error) error {
		g.teardown()
		return err
	}
	if err := g.send(ibStartAPI(int64(g.opts.ClientID))); err != nil {
		return fail(err)
	}
	if err := g.awaitStartup(ctx, deadline, func() bool { return g.nextID > 0 && g.accounts != nil },
		"identifiant d'ordre et liste des comptes"); err != nil {
		return fail(err)
	}

	g.mu.Lock()
	account, err := ibPickAccount(g.accounts, g.opts.Account)
	if err == nil {
		err = ibCheckMode(g.opts.Mode, account)
	}
	g.account = account
	g.mu.Unlock()
	if err != nil {
		return fail(err)
	}

	if err := g.send(ibReqAccountSummary(ibSummaryReqID, "All", "NetLiquidation,InitMarginReq")); err != nil {
		return fail(err)
	}
	if err := g.send(ibReqPositions()); err != nil {
		return fail(err)
	}
	if err := g.awaitStartup(ctx, deadline, func() bool { return g.summaryDone && g.posDone },
		"résumé du compte et positions"); err != nil {
		return fail(err)
	}

	g.mu.Lock()
	nl, ok := g.summary["NetLiquidation"]
	switch {
	case !ok:
		err = fmt.Errorf("TWS n'a pas fourni la valeur liquidative du compte %s", account)
	case g.opts.AccountCurrency != "" && nl.currency != g.opts.AccountCurrency:
		err = fmt.Errorf("le compte %s est tenu en %s, backtest.account_currency vaut %s : "+
			"le dimensionnement au risque serait faux — aligner la configuration sur le compte",
			account, nl.currency, g.opts.AccountCurrency)
	}
	g.mu.Unlock()
	if err != nil {
		return fail(err)
	}

	g.loadBrackets()
	g.mu.Lock()
	g.connected = true
	g.mu.Unlock()
	g.logInfo("Interactive Brokers connecté", "adresse", addr, "version_serveur", sv,
		"compte", account, "mode", g.opts.Mode, "devise", nl.currency)
	return nil
}

// ibNegotiate : poignée de main. TWS répond par une trame à deux champs,
// version et heure de connexion — parfois après d'autres messages, d'où
// la boucle du client officiel, reprise ici.
func ibNegotiate(conn net.Conn, r *bufio.Reader) (int, error) {
	if _, err := conn.Write(ibHandshake()); err != nil {
		return 0, fmt.Errorf("poignée de main TWS : %v", err)
	}
	for i := 0; i < 32; i++ {
		body, err := ibReadFrame(r)
		if err != nil {
			return 0, fmt.Errorf("TWS n'a pas répondu à la poignée de main (%v) — "+
				"API activée ? adresse autorisée dans « Trusted IPs » ?", err)
		}
		fields := ibSplit(body)
		if len(fields) != 2 {
			continue
		}
		sv, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, fmt.Errorf("version de serveur TWS illisible %q", fields[0])
		}
		if sv < ibMinServerVersion {
			return 0, fmt.Errorf("TWS trop ancien (protocole %d, minimum %d) : installer TWS ou IB Gateway 10.10 ou plus récent",
				sv, ibMinServerVersion)
		}
		return sv, nil
	}
	return 0, fmt.Errorf("TWS n'a pas envoyé sa version")
}

func (g *ibGateway) awaitStartup(ctx context.Context, deadline time.Time, ready func() bool, what string) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		g.mu.Lock()
		ok, err, ch := ready(), g.startErr, g.startup
		g.mu.Unlock()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("TWS n'a pas fourni %s à temps — compte connecté dans TWS ? "+
				"« Read-Only API » décoché ?", what)
		}
	}
}

// ibPickAccount : le compte configuré s'il est géré par cette connexion,
// sinon le seul compte géré. Plusieurs comptes sans choix explicite =
// refus : trader sur le mauvais compte n'est pas une erreur qu'on rattrape.
func ibPickAccount(accounts []string, wanted string) (string, error) {
	if wanted != "" {
		for _, a := range accounts {
			if a == wanted {
				return a, nil
			}
		}
		return "", fmt.Errorf("compte %s absent de cette session TWS (comptes : %s)", wanted, strings.Join(accounts, ", "))
	}
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("TWS n'a annoncé aucun compte")
	case 1:
		return accounts[0], nil
	}
	return "", fmt.Errorf("cette session TWS gère plusieurs comptes (%s) : choisir avec broker.account",
		strings.Join(accounts, ", "))
}

// ibCheckMode : un compte papier IB porte un identifiant commençant par
// « D » (DU…, DF…). C'est la seule preuve disponible côté API, et elle
// suffit à empêcher les deux confusions graves : croire tester sur un
// compte réel, ou afficher « ARGENT RÉEL » sur un compte fictif.
func ibCheckMode(mode, account string) error {
	paper := strings.HasPrefix(account, "D")
	switch {
	case mode == "paper" && !paper:
		return fmt.Errorf("broker.mode vaut « paper » mais le compte %s est un compte RÉEL : "+
			"connexion refusée. Se connecter à TWS en mode papier, ou passer broker.mode à « live »", account)
	case mode == "live" && paper:
		return fmt.Errorf("broker.mode vaut « live » mais le compte %s est un compte PAPIER : "+
			"connexion refusée, l'interface afficherait ARGENT RÉEL sur un compte fictif", account)
	}
	return nil
}

// Disconnect ferme le socket. Les ordres attachés restent CHEZ le
// courtier : c'est tout leur intérêt.
func (g *ibGateway) Disconnect() error {
	g.teardown()
	return nil
}

func (g *ibGateway) teardown() {
	g.mu.Lock()
	conn, stop := g.conn, g.stop
	g.conn, g.connected = nil, false
	g.stop = nil
	g.mu.Unlock()
	if conn == nil {
		return
	}
	conn.Close()
	if stop != nil {
		close(stop)
	}
	g.wg.Wait()
}

// send écrit un message complet.
func (g *ibGateway) send(body []byte) error {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("passerelle Interactive Brokers non connectée")
	}
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(ibFrame(body)); err != nil {
		return fmt.Errorf("écriture vers TWS : %v", err)
	}
	return nil
}

// --- Lecture ------------------------------------------------------------

func (g *ibGateway) readLoop(conn net.Conn, r *bufio.Reader) {
	defer g.wg.Done()
	for {
		body, err := ibReadFrame(r)
		if err != nil {
			g.connectionLost(conn, err)
			return
		}
		g.mu.Lock()
		sv := g.sv
		g.mu.Unlock()
		msg, err := ibDecode(sv, ibSplit(body))
		if err != nil {
			g.logWarn("message TWS illisible, ignoré", "erreur", err)
			continue
		}
		if msg != nil {
			g.handle(msg)
		}
	}
}

func (g *ibGateway) connectionLost(conn net.Conn, err error) {
	g.mu.Lock()
	current := g.conn == conn
	wasUp := g.connected
	if current {
		g.connected = false
		if g.startErr == nil {
			g.startErr = fmt.Errorf("TWS a fermé la connexion (%v)", err)
		}
		g.signalStartup()
	}
	g.mu.Unlock()
	if current && wasUp {
		g.logError("connexion à TWS PERDUE : plus aucun ordre ne partira. "+
			"Les stops et limites déjà posés restent actifs chez le courtier.", "erreur", err)
	}
}

// signalStartup réveille awaitStartup. Appelé sous g.mu.
func (g *ibGateway) signalStartup() {
	select {
	case g.startup <- struct{}{}:
	default:
	}
}

// handle applique un message reçu. Les comptes rendus et les messages à
// envoyer sont préparés sous verrou, puis émis après l'avoir relâché.
func (g *ibGateway) handle(msg any) {
	var out ibOutbox
	g.mu.Lock()
	switch m := msg.(type) {
	case ibNextValidID:
		if m.OrderID > g.nextID {
			g.nextID = m.OrderID
		}
		g.signalStartup()
	case ibManagedAccounts:
		g.accounts = append([]string{}, m.Accounts...)
		g.signalStartup()
	case ibAccountSummary:
		if m.ReqID == ibSummaryReqID && (g.account == "" || m.Account == g.account) {
			if v, err := strconv.ParseFloat(m.Value, 64); err == nil {
				g.summary[m.Tag] = ibValue{value: v, currency: m.Currency}
			}
		}
	case ibAccountSummaryEnd:
		g.summaryDone = true
		g.signalStartup()
	case ibPosition:
		if m.Account == g.account || g.account == "" {
			if sym, ok := ibSymbolFor(m.SecType, m.Symbol, m.Currency); ok {
				if m.Position == 0 {
					delete(g.positions, sym)
				} else {
					g.positions[sym] = ibHolding{quantity: m.Position, avgCost: m.AvgCost}
				}
			}
		}
	case ibPositionEnd:
		g.posDone = true
		g.signalStartup()
	case ibTickPrice:
		g.onTickPrice(m, &out)
	case ibErrorMsg:
		g.onError(m, &out)
	case ibOrderStatus:
		g.onOrderStatus(m, &out)
	case ibExecution:
		g.onExecDetails(m, &out)
	case ibCommission:
		g.commissions[m.ExecID] = m
		if id, ok := g.execOrder[m.ExecID]; ok {
			if t := g.orders[id]; t != nil {
				g.trySettle(t, false, &out)
			}
		}
	}
	g.mu.Unlock()
	g.flush(out)
}

// ibOutbox : ce qu'un traitement sous verrou demande d'émettre ensuite.
type ibOutbox struct {
	deliveries []ibDelivery
	sends      [][]byte
	logs       []func()
}

func (o *ibOutbox) report(r core.ExecutionReport) {
	o.deliveries = append(o.deliveries, ibDelivery{report: &r})
}

func (g *ibGateway) flush(out ibOutbox) {
	for _, body := range out.sends {
		if err := g.send(body); err != nil {
			g.logError("message non transmis à TWS", "erreur", err)
		}
	}
	for _, l := range out.logs {
		l()
	}
	if len(out.deliveries) > 0 {
		g.enqueue(out.deliveries...)
	}
}

func (g *ibGateway) onTickPrice(m ibTickPrice, out *ibOutbox) {
	sym, ok := g.subs[m.ReqID]
	if !ok || m.Price <= 0 {
		// IB envoie -1 quand il n'a pas de cotation : ce n'est pas un prix.
		return
	}
	q := g.quotes[sym]
	if q == nil {
		q = &ibQuote{}
		g.quotes[sym] = q
	}
	switch m.TickType {
	case ibTickBid, ibTickDelayedBid:
		q.bid = m.Price
	case ibTickAsk, ibTickDelayedAsk:
		q.ask = m.Price
	default:
		return
	}
	if q.bid <= 0 || q.ask <= 0 {
		return
	}
	// Horodaté à la RÉCEPTION : IB ne date pas ses ticks de prix, et c'est
	// bien l'instant où le programme apprend le prix.
	t := core.Tick{Symbol: sym, Bid: q.bid, Ask: q.ask, Time: time.Now().UTC()}
	out.deliveries = append(out.deliveries, ibDelivery{tick: &t})
}

// Codes d'erreur TWS qui ne sont que des avertissements sur un ordre.
func ibIsWarning(code int64) bool {
	return code == 399 || code >= 2100 || code == 10167
}

func (g *ibGateway) onError(m ibErrorMsg, out *ibOutbox) {
	code, text := m.Code, m.Msg
	switch code {
	case 1100, 2110:
		g.linkLost, g.lostReason = true, text
		out.logs = append(out.logs, func() {
			g.logError("TWS a perdu sa connexion aux serveurs d'IB : aucun ordre ne peut partir", "code", code, "message", text)
		})
		return
	case 1101, 1102:
		wasLost := g.linkLost
		g.linkLost, g.lostReason = false, ""
		if code == 1101 {
			// Données de marché perdues pendant la coupure : on se réabonne.
			for reqID, sym := range g.subs {
				if c, ok := ibContractFor(sym); ok {
					out.sends = append(out.sends, ibReqMktData(reqID, c))
				}
			}
		}
		if wasLost {
			out.logs = append(out.logs, func() { g.logInfo("TWS relié de nouveau aux serveurs d'IB", "code", code) })
		}
		return
	}
	if t := g.orders[m.ReqID]; t != nil && m.ReqID > 0 {
		g.onOrderError(t, code, text, out)
		return
	}
	if sym, ok := g.subs[m.ReqID]; ok {
		out.logs = append(out.logs, func() {
			g.logWarn("données de marché refusées par IB", "symbole", sym, "code", code, "message", text)
		})
		return
	}
	if m.ReqID == -1 && code >= 2100 {
		// « Market data farm connection is OK » et consorts.
		out.logs = append(out.logs, func() { g.logDebug("information TWS", "code", code, "message", text) })
		return
	}
	if !g.connected && g.startErr == nil {
		// Pendant la mise en route, une erreur générale (326 : clientId
		// déjà utilisé, 502, 504…) est la raison de l'échec.
		g.startErr = fmt.Errorf("TWS a refusé la connexion : %s (code %d)", text, code)
		g.signalStartup()
		return
	}
	out.logs = append(out.logs, func() { g.logWarn("message TWS", "requete", m.ReqID, "code", code, "message", text) })
}

func (g *ibGateway) onOrderError(t *ibTrack, code int64, text string, out *ibOutbox) {
	id := t.id
	switch {
	case ibIsWarning(code):
		out.logs = append(out.logs, func() { g.logWarn("avertissement TWS sur un ordre", "ordre", id, "code", code, "message", text) })
	case code == 202:
		// « Order Canceled » : le statut Cancelled suit, c'est lui qui tranche.
		t.lastErr = text
	case code == 10147 || code == 10148 || code == 10149:
		// Annulation d'un ordre introuvable ou déjà terminé. S'il a été
		// exécuté, l'exécution l'a déjà dit ; sinon il n'existe plus.
		if t.cancelRequested && !t.filled {
			g.finish(t, core.Cancelled, text, out)
		}
	default:
		t.lastErr = fmt.Sprintf("%s (code %d)", text, code)
		if !t.filled && t.execQty == 0 {
			g.finish(t, core.Rejected, t.lastErr, out)
		}
	}
}

func (g *ibGateway) onOrderStatus(m ibOrderStatus, out *ibOutbox) {
	t := g.orders[m.OrderID]
	if t == nil {
		return
	}
	if m.Filled > 0 && (t.role == ibRoleTakeProfit || t.role == ibRoleStopLoss) {
		g.markClosing(t.symbol)
	}
	switch m.Status {
	case "Filled":
		if m.Remaining == 0 {
			t.statusFilled, t.statusQty, t.statusAvg = true, m.Filled, m.AvgFillPrice
			g.trySettle(t, false, out)
			if !t.settling && !t.finished {
				// Statut « rempli » sans les exécutions : on les attend un
				// peu, puis on rapporte ce que le statut affirme.
				t.settling = true
				g.after(g.settleDelay+2*time.Second, t)
			}
		}
	case "Cancelled", "ApiCancelled":
		if t.execQty > 0 {
			// Annulé après une exécution partielle : ce qui a été exécuté
			// l'a été. Le rapporter comme tel, et le dire fort.
			qty := t.execQty
			out.logs = append(out.logs, func() {
				g.logError("ordre annulé après exécution PARTIELLE", "ordre", m.OrderID, "execute", qty)
			})
			t.qty = t.execQty
			g.trySettle(t, false, out)
			return
		}
		reason := t.lastErr
		if reason == "" {
			reason = "annulé"
		}
		g.finish(t, core.Cancelled, reason, out)
	case "Inactive":
		reason := t.lastErr
		if reason == "" {
			reason = "ordre inactif chez IB (refusé ou hors séance)"
		}
		g.finish(t, core.Rejected, reason, out)
	}
}

func (g *ibGateway) onExecDetails(m ibExecution, out *ibOutbox) {
	t := g.orders[m.OrderID]
	if t == nil {
		sym, _ := ibSymbolFor(m.SecType, m.Symbol, m.Currency)
		out.logs = append(out.logs, func() {
			g.logWarn("exécution d'un ordre inconnu de cette séance, non rapportée", "ordre", m.OrderID, "symbole", sym)
		})
		return
	}
	if _, seen := g.execOrder[m.ExecID]; seen {
		return // TWS peut répéter une exécution
	}
	g.execOrder[m.ExecID] = m.OrderID
	t.execIDs = append(t.execIDs, m.ExecID)
	t.execQty += m.Shares
	t.execNotional += m.Shares * m.Price
	if ts, ok := ibParseTime(m.Time); ok {
		t.execTime = ts
	}
	if t.role == ibRoleTakeProfit || t.role == ibRoleStopLoss {
		g.markClosing(t.symbol)
	}
	g.trySettle(t, false, out)
}

// markClosing : une barrière s'exécute, la position se ferme. L'annulation
// de sa jumelle qui va suivre est ATTENDUE, pas une perte de protection.
func (g *ibGateway) markClosing(symbol string) {
	if b := g.brackets[symbol]; b != nil && !b.Closing {
		b.Closing = true
		g.saveBracketsLocked()
	}
}

// trySettle rapporte l'exécution d'un ordre quand elle est complète et
// que ses commissions sont arrivées, ou quand force le demande.
func (g *ibGateway) trySettle(t *ibTrack, force bool, out *ibOutbox) {
	if t.finished {
		return
	}
	complete := t.execQty > 0 && t.execQty >= t.qty-1e-9
	if !complete {
		if force && t.statusFilled {
			g.settleFilled(t, t.statusQty, t.statusAvg, false, 0, out)
		}
		return
	}
	pnl, known, missing := 0.0, true, false
	currency := g.summary["NetLiquidation"].currency
	for _, e := range t.execIDs {
		c, ok := g.commissions[e]
		if !ok {
			missing = true
			known = false
			continue
		}
		if !c.RealizedKnown || (currency != "" && c.Currency != currency) {
			known = false
			continue
		}
		pnl += c.RealizedPNL
	}
	if missing && !force {
		if !t.settling {
			t.settling = true
			g.after(g.settleDelay, t)
		}
		return
	}
	g.settleFilled(t, t.execQty, t.execNotional/t.execQty, known, pnl, out)
}

// after rappelle trySettle(force) après un délai, sous verrou.
func (g *ibGateway) after(d time.Duration, t *ibTrack) {
	time.AfterFunc(d, func() {
		var out ibOutbox
		g.mu.Lock()
		g.trySettle(t, true, &out)
		g.mu.Unlock()
		g.flush(out)
	})
}

func (g *ibGateway) settleFilled(t *ibTrack, qty, price float64, pnlKnown bool, pnl float64, out *ibOutbox) {
	t.filled, t.finished = true, true
	close(t.done)
	when := t.execTime
	if when.IsZero() {
		when = time.Now().UTC()
	}
	rep := core.ExecutionReport{
		OrderID: ibRef(t.id), Symbol: t.symbol, Side: t.side, Quantity: qty,
		Status: core.Filled, FillPrice: price, Time: when,
	}
	switch t.role {
	case ibRoleEntry:
		rep.Reason = "entrée"
		if b := g.brackets[t.symbol]; b != nil && b.Parent == t.id {
			b.Entered = true
			g.saveBracketsLocked()
		}
	case ibRoleTakeProfit, ibRoleStopLoss, ibRoleExit:
		rep.Closing = true
		rep.Reason = t.reason
		// P&L tel que RAPPORTÉ par IB (commissions), dans la devise du
		// compte ; sinon non rapporté, et le moteur le dit.
		rep.Realized, rep.PnL = pnlKnown, pnl
		if b := g.brackets[t.symbol]; b != nil {
			delete(g.brackets, t.symbol)
			g.saveBracketsLocked()
		}
	}
	out.report(rep)
}

// finish clôt un ordre qui ne s'est PAS exécuté.
func (g *ibGateway) finish(t *ibTrack, status core.ExecutionStatus, reason string, out *ibOutbox) {
	if t.finished {
		return
	}
	t.finished = true
	close(t.done)
	b := g.brackets[t.symbol]
	switch t.role {
	case ibRoleEntry:
		if b != nil && b.Parent == t.id {
			delete(g.brackets, t.symbol)
			g.saveBracketsLocked()
		}
		out.report(core.ExecutionReport{OrderID: ibRef(t.id), Symbol: t.symbol, Side: t.side,
			Quantity: t.qty, Status: status, Reason: reason, Time: time.Now().UTC()})
	case ibRoleExit:
		// La sortie n'est pas partie : la position reste ouverte, et ses
		// barrières ont peut-être déjà été annulées. C'est le pire état
		// possible ; on le crie.
		id := t.id
		out.logs = append(out.logs, func() {
			g.logError("SORTIE NON EXÉCUTÉE : la position reste ouverte, peut-être sans barrières",
				"symbole", t.symbol, "ordre", id, "motif", reason)
		})
		out.report(core.ExecutionReport{OrderID: ibRef(t.id), Symbol: t.symbol, Side: t.side,
			Quantity: t.qty, Status: status, Reason: reason, Time: time.Now().UTC()})
	case ibRoleTakeProfit, ibRoleStopLoss:
		if b == nil || !b.Entered || b.Closing || t.cancelRequested {
			return // annulation attendue : jumelle exécutée, parent refusé, ou sortie demandée
		}
		// Une barrière a disparu sans que sa jumelle ne s'exécute. On
		// laisse passer le délai de grâce avant de conclure.
		sym, id := t.symbol, t.id
		out.logs = append(out.logs, func() {
			g.logWarn("barrière annulée par IB, vérification de la protection", "symbole", sym, "ordre", id, "motif", reason)
		})
		time.AfterFunc(g.protectionGrace, func() { g.checkProtection(sym, id) })
	}
}

// checkProtection ferme au marché une position dont une barrière a
// disparu. Une position « protégée » à l'écran et nue chez le courtier est
// exactement ce que SupportsBracket promet d'empêcher.
func (g *ibGateway) checkProtection(symbol string, lostID int64) {
	var out ibOutbox
	g.mu.Lock()
	b := g.brackets[symbol]
	holding, open := g.positions[symbol]
	if b == nil || !b.Entered || b.Closing || !open || holding.quantity == 0 ||
		(b.TakeProfit != lostID && b.StopLoss != lostID) {
		g.mu.Unlock()
		return
	}
	b.Closing = true
	g.saveBracketsLocked()
	for _, child := range []int64{b.TakeProfit, b.StopLoss} {
		if t := g.orders[child]; t != nil && !t.finished && child != lostID {
			t.cancelRequested = true
			out.sends = append(out.sends, ibCancelOrder(g.sv, child))
		}
	}
	c, _ := ibContractFor(symbol)
	id := g.nextID
	g.nextID++
	side := b.Side.Opposite()
	t := g.track(id, symbol, side, math.Abs(holding.quantity), ibRoleExit, "protection perdue")
	o := newIBOrder()
	o.Action, o.Quantity, o.OrderType, o.TIF = ibAction(side), int64(t.qty), "MKT", "DAY"
	o.Account, o.OrderRef = g.account, "gw"
	out.sends = append(out.sends, ibPlaceOrder(g.sv, id, c, o))
	g.mu.Unlock()
	g.logError("barrière perdue chez IB : position FERMÉE au marché", "symbole", symbol, "ordre", id)
	g.flush(out)
}

// --- Compte, positions, flux -------------------------------------------

func (g *ibGateway) Account(ctx context.Context) (core.AccountState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.connected {
		return core.AccountState{}, fmt.Errorf("passerelle Interactive Brokers non connectée")
	}
	nl, ok := g.summary["NetLiquidation"]
	if !ok {
		return core.AccountState{}, fmt.Errorf("valeur liquidative non encore reçue de TWS")
	}
	return core.AccountState{
		Equity:   nl.value,
		Margin:   g.summary["InitMarginReq"].value,
		Currency: nl.currency,
	}, nil
}

func (g *ibGateway) Positions(ctx context.Context) ([]core.Position, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.connected {
		return nil, fmt.Errorf("passerelle Interactive Brokers non connectée")
	}
	out := make([]core.Position, 0, len(g.positions))
	for sym, h := range g.positions {
		// Pour une position de change, avgCost est le prix moyen.
		out = append(out, core.Position{Symbol: sym, Quantity: h.quantity, AveragePrice: h.avgCost})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

// Subscribe demande le flux de prix. Un symbole que la passerelle ne sait
// pas traduire en contrat IB est écarté EN LE DISANT ; les autres suivent.
func (g *ibGateway) Subscribe(ctx context.Context, symbols []string) error {
	if !g.Connected() {
		return fmt.Errorf("passerelle Interactive Brokers non connectée")
	}
	var sends [][]byte
	var refused []string
	g.mu.Lock()
	for _, sym := range symbols {
		c, ok := ibContractFor(sym)
		if !ok {
			refused = append(refused, sym)
			continue
		}
		reqID := int64(ibMktDataBase + len(g.subs))
		g.subs[reqID] = sym
		sends = append(sends, ibReqMktData(reqID, c))
	}
	g.mu.Unlock()
	if len(refused) > 0 {
		g.logWarn("instruments non pris en charge par la passerelle Interactive Brokers (forex uniquement)",
			"instruments", strings.Join(refused, " "))
	}
	if len(sends) == 0 {
		return fmt.Errorf("aucun instrument négociable chez Interactive Brokers parmi %s", strings.Join(symbols, " "))
	}
	for _, body := range sends {
		if err := g.send(body); err != nil {
			return err
		}
	}
	return nil
}

// --- Ordres -------------------------------------------------------------

// PlaceOrder soumet un ordre. Trois cas :
//   - une SORTIE (sens opposé à la position ou au bracket suivi) : les
//     barrières attachées sont d'abord annulées ET confirmées, puis
//     l'ordre au marché part — sinon un stop orphelin rouvrirait une
//     position dès son déclenchement ;
//   - une entrée avec stop et/ou limite : un bracket ;
//   - une entrée sans barrière : un ordre au marché simple.
func (g *ibGateway) PlaceOrder(ctx context.Context, req core.OrderRequest) (string, error) {
	if !g.Connected() {
		return "", fmt.Errorf("passerelle Interactive Brokers non connectée")
	}
	c, ok := ibContractFor(req.Symbol)
	if !ok {
		return "", fmt.Errorf("%s n'est pas négociable par cette passerelle (forex IDEALPRO uniquement)", req.Symbol)
	}
	qty := math.Floor(req.Quantity + 1e-9)
	if qty < 1 {
		return "", fmt.Errorf("quantité %g < 1 unité : IB n'exécute que des unités entières", req.Quantity)
	}
	if req.Type != "" && req.Type != core.Market {
		return "", fmt.Errorf("type d'ordre %s non pris en charge", req.Type)
	}

	g.mu.Lock()
	b := g.brackets[req.Symbol]
	h, held := g.positions[req.Symbol]
	closing := (b != nil && req.Side == b.Side.Opposite()) ||
		(held && h.quantity != 0 && (h.quantity > 0) != (req.Side == core.Buy))
	g.mu.Unlock()

	if closing {
		return g.placeExit(ctx, req, c, qty)
	}
	if b != nil {
		return "", fmt.Errorf("un bracket est déjà suivi sur %s (ordre %d) : aucune nouvelle entrée", req.Symbol, b.Parent)
	}
	if req.StopLoss != 0 || req.TakeProfit != 0 {
		return g.placeBracket(req, c, qty)
	}
	return g.placeSimple(req.Symbol, req.Side, c, qty, ibRoleEntry, "entrée")
}

func (g *ibGateway) placeSimple(symbol string, side core.OrderSide, c ibContract, qty float64, role ibRole, reason string) (string, error) {
	g.mu.Lock()
	id := g.nextID
	g.nextID++
	t := g.track(id, symbol, side, qty, role, reason)
	o := newIBOrder()
	o.Action, o.Quantity, o.OrderType, o.TIF = ibAction(side), int64(qty), "MKT", "DAY"
	o.Account, o.OrderRef = g.account, "gw"
	body := ibPlaceOrder(g.sv, id, c, o)
	g.mu.Unlock()
	if err := g.send(body); err != nil {
		g.mu.Lock()
		delete(g.orders, t.id)
		g.mu.Unlock()
		return "", err
	}
	return ibRef(id), nil
}

func (g *ibGateway) placeBracket(req core.OrderRequest, c ibContract, qty float64) (string, error) {
	sl, tp := ibRoundPrice(req.Symbol, req.StopLoss), ibRoundPrice(req.Symbol, req.TakeProfit)
	// Barrières du mauvais côté : IB les exécuterait aussitôt.
	if req.Side == core.Buy && sl > 0 && tp > 0 && sl >= tp ||
		req.Side == core.Sell && sl > 0 && tp > 0 && sl <= tp {
		return "", fmt.Errorf("barrières incohérentes pour un %s : stop %g, limite %g", req.Side, sl, tp)
	}
	exit := req.Side.Opposite()

	g.mu.Lock()
	parent := g.nextID
	g.nextID += 3
	b := &ibBracket{Symbol: req.Symbol, Side: req.Side, Quantity: qty, Parent: parent}
	type leg struct {
		id    int64
		order ibOrder
	}
	legs := []leg{}
	po := newIBOrder()
	po.Action, po.Quantity, po.OrderType, po.TIF = ibAction(req.Side), int64(qty), "MKT", "DAY"
	po.Account, po.OrderRef, po.Transmit = g.account, "gw", false
	legs = append(legs, leg{parent, po})
	g.track(parent, req.Symbol, req.Side, qty, ibRoleEntry, "entrée")
	if tp > 0 {
		b.TakeProfit = parent + 1
		o := newIBOrder()
		o.Action, o.Quantity, o.OrderType, o.LmtPrice, o.TIF = ibAction(exit), int64(qty), "LMT", tp, "GTC"
		o.Account, o.OrderRef, o.Transmit, o.ParentID = g.account, "gw", false, parent
		legs = append(legs, leg{b.TakeProfit, o})
		g.track(b.TakeProfit, req.Symbol, exit, qty, ibRoleTakeProfit, "limite")
	}
	if sl > 0 {
		b.StopLoss = parent + 2
		o := newIBOrder()
		o.Action, o.Quantity, o.OrderType, o.AuxPrice, o.TIF = ibAction(exit), int64(qty), "STP", sl, "GTC"
		o.Account, o.OrderRef, o.Transmit, o.ParentID = g.account, "gw", false, parent
		legs = append(legs, leg{b.StopLoss, o})
		g.track(b.StopLoss, req.Symbol, exit, qty, ibRoleStopLoss, "stop")
	}
	// Le DERNIER ordre transmet l'ensemble. Transmis plus tôt, le parent
	// partirait seul, sans ses barrières.
	legs[len(legs)-1].order.Transmit = true
	// Persisté AVANT l'envoi : un arrêt entre les deux laisserait sinon
	// chez le courtier des ordres dont le programme ignore l'existence.
	g.brackets[req.Symbol] = b
	g.saveBracketsLocked()
	sv := g.sv
	g.mu.Unlock()

	for _, l := range legs {
		if err := g.send(ibPlaceOrder(sv, l.id, c, l.order)); err != nil {
			// Rien n'est transmis tant que le dernier ordre ne l'est pas.
			g.mu.Lock()
			delete(g.brackets, req.Symbol)
			for _, x := range legs {
				delete(g.orders, x.id)
			}
			g.saveBracketsLocked()
			g.mu.Unlock()
			return "", err
		}
	}
	return ibRef(parent), nil
}

func (g *ibGateway) placeExit(ctx context.Context, req core.OrderRequest, c ibContract, qty float64) (string, error) {
	g.mu.Lock()
	b := g.brackets[req.Symbol]
	var waits []*ibTrack
	var cancels [][]byte
	if b != nil {
		b.Closing = true
		g.saveBracketsLocked()
		for _, child := range []int64{b.TakeProfit, b.StopLoss} {
			if child == 0 {
				continue
			}
			t := g.orders[child]
			if t == nil {
				// Barrière d'une séance précédente : suivie à partir d'ici.
				role := ibRoleStopLoss
				if child == b.TakeProfit {
					role = ibRoleTakeProfit
				}
				reason := "stop"
				if role == ibRoleTakeProfit {
					reason = "limite"
				}
				t = g.track(child, b.Symbol, b.Side.Opposite(), b.Quantity, role, reason)
			}
			if t.finished {
				if t.filled {
					g.mu.Unlock()
					// La barrière a déjà fermé la position.
					return ibRef(child), nil
				}
				continue
			}
			t.cancelRequested = true
			waits = append(waits, t)
			cancels = append(cancels, ibCancelOrder(g.sv, child))
		}
	}
	g.mu.Unlock()

	for _, body := range cancels {
		if err := g.send(body); err != nil {
			return "", fmt.Errorf("annulation des barrières impossible, sortie NON envoyée : %v", err)
		}
	}
	timeout := time.NewTimer(g.cancelTimeout)
	defer timeout.Stop()
	for _, t := range waits {
		select {
		case <-t.done:
		case <-ctx.Done():
			return "", fmt.Errorf("sortie abandonnée : %v", ctx.Err())
		case <-timeout.C:
			return "", fmt.Errorf("annulation des barrières de %s non confirmée en %s : sortie NON envoyée, "+
				"la position reste protégée", req.Symbol, g.cancelTimeout)
		}
	}
	g.mu.Lock()
	for _, t := range waits {
		if t.filled {
			g.mu.Unlock()
			// Course perdue par la sortie : la barrière s'est exécutée
			// pendant l'annulation. La position est fermée ; un ordre au
			// marché en ouvrirait une nouvelle.
			return ibRef(t.id), nil
		}
	}
	h, held := g.positions[req.Symbol]
	g.mu.Unlock()
	if !held || h.quantity == 0 {
		g.mu.Lock()
		delete(g.brackets, req.Symbol)
		g.saveBracketsLocked()
		g.mu.Unlock()
		return "", fmt.Errorf("aucune position %s chez le courtier : rien à fermer", req.Symbol)
	}
	return g.placeSimple(req.Symbol, req.Side, c, qty, ibRoleExit, "sortie")
}

// track enregistre un ordre à suivre. Appelé sous g.mu.
func (g *ibGateway) track(id int64, symbol string, side core.OrderSide, qty float64, role ibRole, reason string) *ibTrack {
	t := &ibTrack{id: id, symbol: symbol, side: side, qty: qty, role: role, reason: reason, done: make(chan struct{})}
	g.orders[id] = t
	return t
}

// --- Livraison ------------------------------------------------------------

func (g *ibGateway) enqueue(items ...ibDelivery) {
	g.qmu.Lock()
	g.queue = append(g.queue, items...)
	g.qmu.Unlock()
	g.mu.Lock()
	sig := g.qsig
	g.mu.Unlock()
	select {
	case sig <- struct{}{}:
	default:
	}
}

func (g *ibGateway) deliverLoop() {
	defer g.wg.Done()
	g.mu.Lock()
	sig, stop := g.qsig, g.stop
	g.mu.Unlock()
	for {
		select {
		case <-sig:
		case <-stop:
			// Un compte rendu ne se perd jamais, même à l'arrêt.
			g.drain(true)
			return
		}
		g.drain(false)
	}
}

func (g *ibGateway) drain(reportsOnly bool) {
	for {
		g.qmu.Lock()
		items := g.queue
		g.queue = nil
		g.qmu.Unlock()
		if len(items) == 0 {
			return
		}
		for _, it := range items {
			switch {
			case it.report != nil:
				if fn := g.onExecution.Load(); fn != nil {
					(*fn)(*it.report)
				}
			case it.tick != nil && !reportsOnly:
				if fn := g.onTick.Load(); fn != nil {
					(*fn)(*it.tick)
				}
			}
		}
	}
}

// --- Persistance des brackets --------------------------------------------

type ibBracketFile struct {
	ClientID int                   `json:"client_id"`
	Account  string                `json:"account"`
	Brackets map[string]*ibBracket `json:"brackets"`
}

func (g *ibGateway) bracketPath() string {
	if g.opts.StateDir == "" {
		return ""
	}
	return filepath.Join(g.opts.StateDir, "ib_brackets.json")
}

// loadBrackets relit les brackets d'une séance précédente. Les
// identifiants d'ordre n'ont de sens que pour le même clientId et le même
// compte : sinon le fichier est ignoré, et on le dit.
func (g *ibGateway) loadBrackets() {
	path := g.bracketPath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		g.logWarn("brackets de la séance précédente illisibles", "fichier", path, "erreur", err)
		return
	}
	var f ibBracketFile
	if err := json.Unmarshal(raw, &f); err != nil {
		g.logWarn("brackets de la séance précédente illisibles", "fichier", path, "erreur", err)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if f.ClientID != g.opts.ClientID || f.Account != g.account {
		g.logWarn("brackets d'une autre connexion IB ignorés", "fichier", path,
			"client_id", f.ClientID, "compte", f.Account)
		return
	}
	for sym, b := range f.Brackets {
		if b == nil || b.Symbol != sym {
			continue
		}
		g.brackets[sym] = b
		// Les barrières d'une position ouverte avant l'arrêt sont suivies
		// dès maintenant : si l'une se déclenche, sa sortie est rapportée
		// comme telle au lieu d'être ignorée comme « ordre inconnu ».
		if b.Entered {
			if b.TakeProfit != 0 {
				g.track(b.TakeProfit, sym, b.Side.Opposite(), b.Quantity, ibRoleTakeProfit, "limite")
			}
			if b.StopLoss != 0 {
				g.track(b.StopLoss, sym, b.Side.Opposite(), b.Quantity, ibRoleStopLoss, "stop")
			}
		}
	}
	if len(g.brackets) > 0 {
		g.logInfo("brackets repris de la séance précédente", "nombre", len(g.brackets))
	}
}

// saveBracketsLocked écrit l'état des brackets. Appelé sous g.mu.
func (g *ibGateway) saveBracketsLocked() {
	path := g.bracketPath()
	if path == "" {
		return
	}
	raw, err := json.MarshalIndent(ibBracketFile{ClientID: g.opts.ClientID, Account: g.account, Brackets: g.brackets}, "", "  ")
	if err == nil {
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, raw, 0o600); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		g.logError("brackets non persistés : un redémarrage ignorerait les ordres attachés", "erreur", err)
	}
}

// --- Correspondance des instruments ---------------------------------------

// ibContractFor traduit un symbole du projet en contrat IB. Seul le
// forex est traduit : pour les métaux et les indices, la nature du
// contrat IB (CFD, CMDTY, future) change la quantité, le prix et la marge,
// et une correspondance approximative enverrait un ordre faux.
func ibContractFor(symbol string) (ibContract, bool) {
	inst, ok := data.Instruments[symbol]
	if !ok || (inst.Class != "majeure" && inst.Class != "croisée") {
		return ibContract{}, false
	}
	return ibContract{Symbol: inst.Base, SecType: "CASH", Exchange: "IDEALPRO", Currency: inst.Quote}, true
}

// ibSymbolFor : le chemin inverse, pour les positions et les exécutions.
func ibSymbolFor(secType, symbol, currency string) (string, bool) {
	if secType != "CASH" {
		return "", false
	}
	sym := symbol + currency
	if _, ok := ibContractFor(sym); !ok {
		return "", false
	}
	return sym, true
}

// ibRoundPrice aligne un prix sur l'échelon IDEALPRO : un demi-pip
// (0,00005, ou 0,005 pour le yen). Un prix hors échelon est refusé par IB
// (code 110) ; arrondi au plus proche, la barrière bouge d'un
// demi-pip au plus.
func ibRoundPrice(symbol string, price float64) float64 {
	if price <= 0 {
		return 0
	}
	tick, decimals := 0.00005, 5
	if strings.HasSuffix(symbol, "JPY") {
		tick, decimals = 0.005, 3
	}
	v, _ := strconv.ParseFloat(strconv.FormatFloat(math.Round(price/tick)*tick, 'f', decimals, 64), 64)
	return v
}

func ibAction(side core.OrderSide) string {
	if side == core.Buy {
		return "BUY"
	}
	return "SELL"
}

func ibRef(id int64) string { return "IB-" + strconv.FormatInt(id, 10) }

// ibParseTime lit l'heure d'une exécution : « 20240115  10:30:45
// US/Eastern » ou « 20240115-10:30:45 » (UTC). Illisible = heure de
// réception, qui n'en diffère que de la latence.
func ibParseTime(s string) (time.Time, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if t, err := time.Parse("20060102-15:04:05", s); err == nil {
		return t.UTC(), true
	}
	parts := strings.SplitN(s, " ", 3)
	if len(parts) == 3 {
		if loc, err := time.LoadLocation(parts[2]); err == nil {
			if t, err := time.ParseInLocation("20060102 15:04:05", parts[0]+" "+parts[1], loc); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// --- Journal ---------------------------------------------------------------

func (g *ibGateway) logInfo(msg string, args ...any) {
	if g.opts.Logger != nil {
		g.opts.Logger.Info(msg, args...)
	}
}
func (g *ibGateway) logWarn(msg string, args ...any) {
	if g.opts.Logger != nil {
		g.opts.Logger.Warn(msg, args...)
	}
}
func (g *ibGateway) logError(msg string, args ...any) {
	if g.opts.Logger != nil {
		g.opts.Logger.Error(msg, args...)
	}
}
func (g *ibGateway) logDebug(msg string, args ...any) {
	if g.opts.Logger != nil {
		g.opts.Logger.Debug(msg, args...)
	}
}
