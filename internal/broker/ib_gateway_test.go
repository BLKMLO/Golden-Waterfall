package broker

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// fakeTWS parle le côté SERVEUR du protocole, juste assez pour éprouver la
// passerelle : poignée de main, mise en route, flux, ordres. Les messages
// qu'il envoie ont la forme de ceux de testdata/ib/golden_incoming.json,
// validée par le décodeur officiel.
type fakeTWS struct {
	t        *testing.T
	ln       net.Listener
	sv       int
	accounts string
	currency string
	// position initiale annoncée à reqPositions (0 = aucune)
	position float64

	mu       sync.Mutex
	conn     net.Conn
	messages chan []string // tout ce que le client envoie après startApi
}

func newFakeTWS(t *testing.T) *fakeTWS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTWS{t: t, ln: ln, sv: 187, accounts: "DU1234567", currency: "USD",
		messages: make(chan []string, 256)}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeTWS) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeTWS) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = conn
		f.mu.Unlock()
		go f.session(conn)
	}
}

func (f *fakeTWS) session(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	head := make([]byte, 4)
	if _, err := r.Read(head); err != nil || string(head) != "API\x00" {
		return
	}
	if _, err := ibReadFrame(r); err != nil {
		return
	}
	f.sendTo(conn, strconv.Itoa(f.sv), "20240115 10:00:00 EST")
	for {
		body, err := ibReadFrame(r)
		if err != nil {
			return
		}
		fields := ibSplit(body)
		switch fields[0] {
		case "71": // startApi
			f.sendTo(conn, "9", "1", "42")
			f.sendTo(conn, "15", "1", f.accounts)
		case "62": // reqAccountSummary
			for _, a := range strings.Split(f.accounts, ",") {
				f.sendTo(conn, "63", "1", fields[2], a, "NetLiquidation", "100250.75", f.currency)
				f.sendTo(conn, "63", "1", fields[2], a, "InitMarginReq", "1250.5", f.currency)
			}
			f.sendTo(conn, "64", "1", fields[2])
		case "61": // reqPositions
			if f.position != 0 {
				f.positionUpdate(conn, f.position)
			}
			f.sendTo(conn, "62", "1")
		default:
			f.messages <- fields
		}
	}
}

func (f *fakeTWS) sendTo(conn net.Conn, fields ...string) {
	m := &ibMsg{}
	for _, s := range fields {
		m.str(s)
	}
	_, _ = conn.Write(ibFrame(m.b))
}

func (f *fakeTWS) send(fields ...string) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	f.sendTo(conn, fields...)
}

func (f *fakeTWS) positionUpdate(conn net.Conn, qty float64) {
	f.sendTo(conn, "61", "3", "DU1234567", "12087792", "EUR", "CASH", "", "0.0", "", "", "IDEALPRO",
		"USD", "EUR.USD", "EUR.USD", strconv.FormatFloat(qty, 'f', -1, 64), "1.08761")
}

func (f *fakeTWS) positionNow(qty float64) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	f.positionUpdate(conn, qty)
}

// fill simule l'exécution complète d'un ordre : exécution, statut, puis
// commission — dans l'ordre où TWS les envoie d'ordinaire.
func (f *fakeTWS) fill(orderID int64, side string, qty, price float64, realized string) {
	id := strconv.FormatInt(orderID, 10)
	execID := "exec." + id
	q := strconv.FormatFloat(qty, 'f', -1, 64)
	p := strconv.FormatFloat(price, 'f', -1, 64)
	f.send("11", "-1", id, "12087792", "EUR", "CASH", "", "0.0", "", "", "IDEALPRO", "USD", "EUR.USD",
		"EUR.USD", execID, "20240115  10:30:45 US/Eastern", "DU1234567", "IDEALPRO", side, q, p,
		"1735", "7", "0", q, p, "gw", "", "", "", "2", "0")
	f.send("3", id, "Filled", q, "0", p, "1735", "0", p, "7", "", "0")
	f.send("59", "1", execID, "2.0", "USD", realized, "1.7976931348623157E308", "0")
}

func (f *fakeTWS) cancelled(orderID, parent int64) {
	f.send("3", strconv.FormatInt(orderID, 10), "Cancelled", "0", "20000", "0", "1736",
		strconv.FormatInt(parent, 10), "0", "7", "", "0")
}

// next attend le prochain message client du type demandé, en ignorant
// les autres ; échoue si rien n'arrive.
func (f *fakeTWS) next(msgID string) []string {
	f.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-f.messages:
			if m[0] == msgID {
				return m
			}
		case <-deadline:
			f.t.Fatalf("message %s jamais reçu par le faux TWS", msgID)
			return nil
		}
	}
}

// none vérifie qu'aucun message du type donné n'arrive pendant d.
func (f *fakeTWS) none(msgID string, d time.Duration) {
	f.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-f.messages:
			if m[0] == msgID {
				f.t.Fatalf("message %s inattendu : %q", msgID, m)
			}
		case <-deadline:
			return
		}
	}
}

// Champs d'un placeOrder, positions dans le message (cf. ibPlaceOrder).
const (
	poOrderID  = 1
	poSymbol   = 3
	poCurrency = 11
	poAction   = 16
	poQuantity = 17
	poType     = 18
	poLmt      = 19
	poAux      = 20
	poTIF      = 21
	poAccount  = 23
	poTransmit = 27
	poParent   = 28
)

type ibHarness struct {
	tws     *fakeTWS
	gw      Gateway
	mu      sync.Mutex
	reports []core.ExecutionReport
	ticks   []core.Tick
}

func shortDelays(t *testing.T) {
	old := [4]time.Duration{ibStartupTimeout, ibCancelTimeout, ibSettleDelay, ibProtectionGrace}
	ibStartupTimeout, ibCancelTimeout, ibSettleDelay, ibProtectionGrace =
		2*time.Second, time.Second, 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() {
		ibStartupTimeout, ibCancelTimeout, ibSettleDelay, ibProtectionGrace = old[0], old[1], old[2], old[3]
	})
}

func newIBHarness(t *testing.T, tws *fakeTWS, opts Options) (*ibHarness, error) {
	t.Helper()
	shortDelays(t)
	opts.Host, opts.Port = "127.0.0.1", tws.port()
	if opts.Mode == "" {
		opts.Mode = "paper"
	}
	if opts.ClientID == 0 {
		opts.ClientID = 7
	}
	if opts.AccountCurrency == "" {
		opts.AccountCurrency = "USD"
	}
	gw, err := New(ibName, opts)
	if err != nil {
		t.Fatal(err)
	}
	h := &ibHarness{tws: tws, gw: gw}
	gw.OnExecution(func(r core.ExecutionReport) {
		h.mu.Lock()
		h.reports = append(h.reports, r)
		h.mu.Unlock()
	})
	gw.OnTick(func(tk core.Tick) {
		h.mu.Lock()
		h.ticks = append(h.ticks, tk)
		h.mu.Unlock()
	})
	err = gw.Connect(context.Background())
	t.Cleanup(func() { gw.Disconnect() })
	return h, err
}

func (h *ibHarness) waitReports(t *testing.T, n int) []core.ExecutionReport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		got := append([]core.ExecutionReport(nil), h.reports...)
		h.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d compte(s) rendu(s) attendu(s), reçu(s) %d", n, len(h.reports))
	return nil
}

func TestIBConnectsAndReportsAccount(t *testing.T) {
	tws := newFakeTWS(t)
	tws.position = -20000
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !h.gw.Connected() {
		t.Fatal("connexion vérifiée mais Connected() répond non")
	}
	acc, err := h.gw.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acc.Equity != 100250.75 || acc.Margin != 1250.5 || acc.Currency != "USD" {
		t.Fatalf("compte %+v", acc)
	}
	pos, err := h.gw.Positions(context.Background())
	if err != nil || len(pos) != 1 {
		t.Fatalf("positions %+v (%v)", pos, err)
	}
	if pos[0].Symbol != "EURUSD" || pos[0].Quantity != -20000 || pos[0].UnrealizedKnown {
		t.Fatalf("position %+v : le P&L latent n'est pas rapporté par IB, il ne doit pas être présenté comme connu", pos[0])
	}
	if !h.gw.Info().SupportsBracket || h.gw.Info().Simulated {
		t.Fatal("IB porte les barrières chez le courtier et n'est pas une simulation")
	}
}

// TestIBRefusesAMismatchedAccount : chaque confusion possible entre ce que
// la configuration croit et ce que le courtier est REFUSE la connexion.
func TestIBRefusesAMismatchedAccount(t *testing.T) {
	cases := []struct {
		name, accounts, currency, mode, account, want string
	}{
		{"papier annoncé, compte réel", "U1234567", "USD", "paper", "", "compte RÉEL"},
		{"réel annoncé, compte papier", "DU1234567", "USD", "live", "", "compte PAPIER"},
		{"devise différente", "DU1234567", "EUR", "paper", "", "tenu en EUR"},
		{"plusieurs comptes sans choix", "DU1,DU2", "USD", "paper", "", "plusieurs comptes"},
		{"compte absent", "DU1", "USD", "paper", "DU9", "absent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tws := newFakeTWS(t)
			tws.accounts, tws.currency = c.accounts, c.currency
			h, err := newIBHarness(t, tws, Options{Mode: c.mode, Account: c.account})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("erreur %v, attendue contenant %q", err, c.want)
			}
			if h.gw.Connected() {
				t.Fatal("jamais de connexion optimiste")
			}
			if _, err := h.gw.Account(context.Background()); err == nil {
				t.Fatal("aucune donnée de compte ne doit être fournie après un refus")
			}
		})
	}
	t.Run("choix explicite parmi plusieurs", func(t *testing.T) {
		tws := newFakeTWS(t)
		tws.accounts = "DU1,DU2"
		if _, err := newIBHarness(t, tws, Options{Account: "DU2"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestIBRefusesAnOldServerAndAnAbsentTerminal(t *testing.T) {
	tws := newFakeTWS(t)
	tws.sv = 150
	if _, err := newIBHarness(t, tws, Options{}); err == nil || !strings.Contains(err.Error(), "trop ancien") {
		t.Fatalf("un serveur trop ancien doit être refusé : %v", err)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	gw, _ := New(ibName, Options{Host: "127.0.0.1", Port: port, Mode: "paper"})
	if err := gw.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "injoignable") {
		t.Fatalf("un TWS absent doit être dit : %v", err)
	}
}

func TestIBStreamsQuotesAndRefusesNonForex(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.gw.Subscribe(context.Background(), []string{"XAUUSD"}); err == nil {
		t.Fatal("un métal ne doit pas être traduit au jugé en contrat IB")
	}
	if err := h.gw.Subscribe(context.Background(), []string{"EURUSD", "XAUUSD"}); err != nil {
		t.Fatal(err)
	}
	req := tws.next("1")
	if req[4] != "EUR" || req[5] != "CASH" || req[10] != "IDEALPRO" || req[12] != "USD" {
		t.Fatalf("reqMktData %q", req)
	}
	tws.send("1", "6", req[2], "1", "1.08761", "1000000", "0")
	tws.send("1", "6", req[2], "2", "-1", "0", "0") // pas de cotation : ignoré
	tws.send("1", "6", req[2], "2", "1.08765", "1000000", "0")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := len(h.ticks)
		h.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.ticks) != 1 || h.ticks[0].Bid != 1.08761 || h.ticks[0].Ask != 1.08765 || h.ticks[0].Symbol != "EURUSD" {
		t.Fatalf("ticks %+v", h.ticks)
	}
	if _, err := h.gw.PlaceOrder(context.Background(), core.OrderRequest{Symbol: "XAUUSD", Side: core.Buy, Quantity: 1}); err == nil {
		t.Fatal("aucun ordre sur un instrument non traduit")
	}
}

// enterBracket soumet une entrée protégée et vérifie les trois ordres.
func enterBracket(t *testing.T, h *ibHarness) (parent, tp, sl int64) {
	t.Helper()
	ref, err := h.gw.PlaceOrder(context.Background(), core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 20000.7, Type: core.Market,
		StopLoss: 1.076512, TakeProfit: 1.092537,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, c1, c2 := h.tws.next("3"), h.tws.next("3"), h.tws.next("3")
	if ref != "IB-"+p[poOrderID] {
		t.Fatalf("référence %s, parent %s", ref, p[poOrderID])
	}
	check := func(m []string, action, typ, lmt, aux, tif, transmit, parent string) {
		t.Helper()
		got := []string{m[poAction], m[poQuantity], m[poType], m[poLmt], m[poAux], m[poTIF], m[poTransmit], m[poParent], m[poAccount]}
		want := []string{action, "20000", typ, lmt, aux, tif, transmit, parent, "DU1234567"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("ordre %s\nreçu    %q\nattendu %q", m[poOrderID], got, want)
		}
	}
	// Parent au marché NON transmis ; la limite non plus ; le stop, en
	// dernier, transmet l'ensemble. Prix alignés sur le demi-pip.
	check(p, "BUY", "MKT", "", "", "DAY", "0", "0")
	check(c1, "SELL", "LMT", "1.09255", "", "GTC", "0", p[poOrderID])
	check(c2, "SELL", "STP", "", "1.0765", "GTC", "1", p[poOrderID])
	parent, _ = strconv.ParseInt(p[poOrderID], 10, 64)
	tp, _ = strconv.ParseInt(c1[poOrderID], 10, 64)
	sl, _ = strconv.ParseInt(c2[poOrderID], 10, 64)

	h.tws.fill(parent, "BOT", 20000, 1.08765, "1.7976931348623157E308")
	h.tws.positionNow(20000)
	return parent, tp, sl
}

func TestIBBracketStopReportsBrokerPnL(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	parent, tp, sl := enterBracket(t, h)
	reps := h.waitReports(t, 1)
	if r := reps[0]; r.Status != core.Filled || r.Side != core.Buy || r.Quantity != 20000 ||
		r.FillPrice != 1.08765 || r.Closing || r.OrderID != fmt.Sprintf("IB-%d", parent) {
		t.Fatalf("entrée %+v", r)
	}
	if want := time.Date(2024, 1, 15, 15, 30, 45, 0, time.UTC); !reps[0].Time.Equal(want) {
		t.Fatalf("heure d'exécution %v, attendue %v (heure d'IB, pas de réception)", reps[0].Time, want)
	}

	// TWS annonce l'annulation de la jumelle AVANT l'exécution du stop :
	// ce n'est PAS une perte de protection.
	tws.cancelled(tp, parent)
	tws.fill(sl, "SLD", 20000, 1.0765, "-212.5")
	tws.positionNow(0)
	reps = h.waitReports(t, 2)
	if r := reps[1]; r.Status != core.Filled || !r.Closing || !r.Realized || r.PnL != -212.5 ||
		r.Reason != "stop" || r.Side != core.Sell {
		t.Fatalf("sortie par stop %+v", r)
	}
	tws.none("3", 3*ibProtectionGrace)
}

func TestIBExitCancelsBarriersBeforeSelling(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	parent, tp, sl := enterBracket(t, h)
	h.waitReports(t, 1)

	done := make(chan error, 1)
	var ref string
	go func() {
		var err error
		ref, err = h.gw.PlaceOrder(context.Background(), core.OrderRequest{
			Symbol: "EURUSD", Side: core.Sell, Quantity: 20000, Type: core.Market,
		})
		done <- err
	}()
	// Les deux annulations passent AVANT tout ordre de sortie.
	c1, c2 := tws.next("4"), tws.next("4")
	got := map[string]bool{c1[2]: true, c2[2]: true}
	if !got[strconv.FormatInt(tp, 10)] || !got[strconv.FormatInt(sl, 10)] {
		t.Fatalf("annulations %q %q, attendues %d et %d", c1, c2, tp, sl)
	}
	select {
	case m := <-tws.messages:
		t.Fatalf("rien ne doit partir avant la confirmation des annulations : %q", m)
	case <-time.After(100 * time.Millisecond):
	}
	tws.cancelled(tp, parent)
	tws.cancelled(sl, parent)
	exit := tws.next("3")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if exit[poAction] != "SELL" || exit[poType] != "MKT" || exit[poTransmit] != "1" || exit[poParent] != "0" {
		t.Fatalf("sortie %q", exit)
	}
	id, _ := strconv.ParseInt(exit[poOrderID], 10, 64)
	if ref != fmt.Sprintf("IB-%d", id) {
		t.Fatalf("référence %s", ref)
	}
	tws.fill(id, "SLD", 20000, 1.0901, "48.4")
	reps := h.waitReports(t, 2)
	if r := reps[1]; !r.Closing || r.Reason != "sortie" || r.PnL != 48.4 || !r.Realized {
		t.Fatalf("sortie %+v", r)
	}
	if len(reps) != 2 {
		t.Fatalf("les annulations demandées ne sont pas des comptes rendus : %+v", reps)
	}
}

func TestIBExitAbortsWhenCancellationIsNotConfirmed(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	enterBracket(t, h)
	h.waitReports(t, 1)
	_, err = h.gw.PlaceOrder(context.Background(), core.OrderRequest{Symbol: "EURUSD", Side: core.Sell, Quantity: 20000})
	if err == nil || !strings.Contains(err.Error(), "NON envoyée") {
		t.Fatalf("sans confirmation, la sortie ne doit pas partir : %v", err)
	}
	tws.none("3", 200*time.Millisecond)
}

func TestIBRejectedEntryIsReported(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.gw.PlaceOrder(context.Background(), core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 20000, StopLoss: 1.07, TakeProfit: 1.09,
	}); err != nil {
		t.Fatal(err)
	}
	p := tws.next("3")
	tws.send("4", "2", p[poOrderID], "201", "Order rejected - reason:Insufficient margin", "")
	reps := h.waitReports(t, 1)
	if r := reps[0]; r.Status != core.Rejected || !strings.Contains(r.Reason, "Insufficient margin") {
		t.Fatalf("refus %+v", r)
	}
	// Le bracket refusé n'empêche pas une nouvelle entrée.
	if _, err := h.gw.PlaceOrder(context.Background(), core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 20000, StopLoss: 1.07, TakeProfit: 1.09,
	}); err != nil {
		t.Fatalf("un bracket refusé ne doit pas bloquer le symbole : %v", err)
	}
	if _, err := h.gw.PlaceOrder(context.Background(), core.OrderRequest{
		Symbol: "EURUSD", Side: core.Buy, Quantity: 20000, StopLoss: 1.09, TakeProfit: 1.07,
	}); err == nil {
		t.Fatal("des barrières du mauvais côté doivent être refusées avant d'atteindre IB")
	}
}

// TestIBLostBarrierClosesThePosition : une barrière annulée par IB sans
// que sa jumelle s'exécute laisse une position NUE. Elle est fermée.
func TestIBLostBarrierClosesThePosition(t *testing.T) {
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	parent, tp, sl := enterBracket(t, h)
	h.waitReports(t, 1)
	tws.cancelled(sl, parent)
	cancel := tws.next("4")
	if cancel[2] != strconv.FormatInt(tp, 10) {
		t.Fatalf("la limite restante doit être annulée : %q", cancel)
	}
	exit := tws.next("3")
	if exit[poAction] != "SELL" || exit[poType] != "MKT" || exit[poQuantity] != "20000" {
		t.Fatalf("fermeture %q", exit)
	}
}

func TestIBBracketsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	tws := newFakeTWS(t)
	h, err := newIBHarness(t, tws, Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, tp, sl := enterBracket(t, h)
	h.waitReports(t, 1)
	if _, err := os.Stat(filepath.Join(dir, "ib_brackets.json")); err != nil {
		t.Fatal("les barrières doivent être persistées")
	}
	h.gw.Disconnect()

	tws.position = 20000
	h2, err := newIBHarness(t, tws, Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	go h2.gw.PlaceOrder(context.Background(), core.OrderRequest{Symbol: "EURUSD", Side: core.Sell, Quantity: 20000})
	c1, c2 := tws.next("4"), tws.next("4")
	got := map[string]bool{c1[2]: true, c2[2]: true}
	if !got[strconv.FormatInt(tp, 10)] || !got[strconv.FormatInt(sl, 10)] {
		t.Fatalf("après redémarrage, la sortie doit annuler les barrières de la séance précédente : %q %q", c1, c2)
	}
}
