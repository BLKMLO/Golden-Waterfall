package broker

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Les messages de référence sont produits par le client OFFICIEL
// d'Interactive Brokers (testdata/ib/gen_golden.py). Ces tests sont ce qui
// sépare « le protocole est implémenté » de « le protocole est deviné » :
// un seul octet de différence avec le client officiel et ils échouent.

func goldenRequests(t *testing.T) map[string][]byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/ib/golden_requests.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("ligne de référence mal formée : %q", line)
		}
		b, err := hex.DecodeString(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		out[parts[0]+"@"+parts[1]] = b
	}
	return out
}

var eurusd = ibContract{Symbol: "EUR", SecType: "CASH", Exchange: "IDEALPRO", Currency: "USD"}

func goldenOrder(action string, qty int64, otype string, tif string, transmit bool, parent int64) ibOrder {
	o := newIBOrder()
	o.Action, o.Quantity, o.OrderType, o.TIF = action, qty, otype, tif
	o.Account, o.OrderRef, o.Transmit, o.ParentID = "DU1234567", "gw", transmit, parent
	return o
}

func TestRequestsMatchOfficialClientByteForByte(t *testing.T) {
	golden := goldenRequests(t)
	for _, sv := range []int{163, 176, 187} {
		tp := goldenOrder("SELL", 20000, "LMT", "GTC", false, 42)
		tp.LmtPrice = 1.0925
		sl := goldenOrder("SELL", 20000, "STP", "GTC", true, 42)
		sl.AuxPrice = 1.07655
		usdjpy := ibContract{Symbol: "USD", SecType: "CASH", Exchange: "IDEALPRO", Currency: "JPY"}
		ours := map[string][]byte{
			"startApi":              ibStartAPI(7),
			"reqIds":                ibReqIDs(1),
			"reqMktData":            ibReqMktData(1001, eurusd),
			"cancelMktData":         ibCancelMktData(1001),
			"reqAccountSummary":     ibReqAccountSummary(9001, "All", "NetLiquidation,InitMarginReq"),
			"cancelAccountSummary":  ibCancelAccountSummary(9001),
			"reqPositions":          ibReqPositions(),
			"cancelPositions":       ibCancelPositions(),
			"cancelOrder":           ibCancelOrder(sv, 42),
			"placeOrder.parent":     ibPlaceOrder(sv, 42, eurusd, goldenOrder("BUY", 20000, "MKT", "DAY", false, 0)),
			"placeOrder.takeProfit": ibPlaceOrder(sv, 43, eurusd, tp),
			"placeOrder.stopLoss":   ibPlaceOrder(sv, 44, eurusd, sl),
			"placeOrder.exitJPY":    ibPlaceOrder(sv, 45, usdjpy, goldenOrder("SELL", 15000, "MKT", "DAY", true, 0)),
		}
		for name, body := range ours {
			key := name + "@" + strconv.Itoa(sv)
			want, ok := golden[key]
			if !ok {
				t.Fatalf("référence absente : %s", key)
			}
			if got := ibFrame(body); !bytes.Equal(got, want) {
				t.Errorf("%s diffère du client officiel\nofficiel : %q\nnôtre    : %q",
					key, ibSplit(want[4:]), ibSplit(got[4:]))
			}
		}
	}
}

// TestDecoderAgreesWithOfficialDecoder : chaque message entrant de
// référence a été lu par le décodeur officiel ; le nôtre doit en tirer
// les mêmes valeurs.
func TestDecoderAgreesWithOfficialDecoder(t *testing.T) {
	raw, err := os.ReadFile("testdata/ib/golden_incoming.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		SV     int                          `json:"sv"`
		Fields []string                     `json:"fields"`
		Calls  []map[string]json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("aucun cas de référence")
	}
	for _, c := range cases {
		got, err := ibDecode(c.SV, c.Fields)
		if err != nil {
			t.Fatalf("%v : %v", c.Fields, err)
		}
		if len(c.Calls) != 1 {
			t.Fatalf("cas de référence inattendu : %v", c.Calls)
		}
		for name, args := range c.Calls[0] {
			var v []any
			if err := json.Unmarshal(args, &v); err != nil {
				t.Fatal(err)
			}
			if want := officialValues(name, v); !sameValues(ourValues(got), want) {
				t.Errorf("sv %d, %s : officiel %v, nôtre %v", c.SV, name, want, ourValues(got))
			}
		}
	}
}

// officialValues normalise ce que le décodeur officiel a produit : les
// nombres JSON deviennent des float64, la liste de comptes est découpée
// comme le fait la passerelle, et la valeur sentinelle d'un P&L non
// réalisé devient « inconnu ».
func officialValues(name string, v []any) []any {
	switch name {
	case "managedAccounts":
		var out []any
		for _, a := range strings.Split(v[0].(string), ",") {
			if a != "" {
				out = append(out, a)
			}
		}
		return out
	case "commissionReport":
		pnl := v[3].(float64)
		if pnl > 1e300 {
			return []any{v[0], v[1], v[2], "inconnu"}
		}
	}
	return v
}

func ourValues(m any) []any {
	switch x := m.(type) {
	case ibTickPrice:
		return []any{float64(x.ReqID), float64(x.TickType), x.Price}
	case ibOrderStatus:
		return []any{float64(x.OrderID), x.Status, x.Filled, x.Remaining, x.AvgFillPrice, float64(x.ParentID), x.WhyHeld}
	case ibErrorMsg:
		return []any{float64(x.ReqID), float64(x.Code), x.Msg}
	case ibNextValidID:
		return []any{float64(x.OrderID)}
	case ibExecution:
		return []any{float64(x.ReqID), float64(x.OrderID), x.Symbol, x.SecType, x.Currency,
			x.ExecID, x.Time, x.Account, x.Side, x.Shares, x.Price}
	case ibManagedAccounts:
		out := make([]any, len(x.Accounts))
		for i, a := range x.Accounts {
			out[i] = a
		}
		return out
	case ibCommission:
		pnl := any(x.RealizedPNL)
		if !x.RealizedKnown {
			pnl = "inconnu"
		}
		return []any{x.ExecID, x.Commission, x.Currency, pnl}
	case ibPosition:
		return []any{x.Account, x.Symbol, x.SecType, x.Currency, x.Position, x.AvgCost}
	case ibPositionEnd:
		return []any{}
	case ibAccountSummary:
		return []any{float64(x.ReqID), x.Account, x.Tag, x.Value, x.Currency}
	case ibAccountSummaryEnd:
		return []any{float64(x.ReqID)}
	}
	return []any{"type inattendu"}
}

func sameValues(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFramingRoundTrip(t *testing.T) {
	body := ibReqPositions()
	r := bufio.NewReader(bytes.NewReader(append(ibFrame(body), ibFrame(ibReqIDs(3))...)))
	for _, want := range [][]byte{body, ibReqIDs(3)} {
		got, err := ibReadFrame(r)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("trame relue %q, attendue %q (%v)", got, want, err)
		}
	}
	if _, err := ibDecode(187, []string{"61", "3", "DU1"}); err == nil {
		t.Fatal("un message tronqué doit être refusé, pas lu avec des zéros")
	}
	if got := string(ibHandshake()); got != "API\x00\x00\x00\x00\x09v100..187" {
		t.Fatalf("poignée de main %q", got)
	}
}
