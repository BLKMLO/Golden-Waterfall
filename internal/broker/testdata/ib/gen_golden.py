"""Produit les messages de référence de la passerelle Interactive Brokers.

Ce script fait parler le client OFFICIEL d'Interactive Brokers (API TWS
10.30, client Python) et consigne ce qu'il enverrait, octet pour octet,
ainsi que ce que son décodeur lit dans des messages entrants. Les tests Go
exigent de retrouver exactement les mêmes octets et les mêmes valeurs :
c'est ce qui empêche d'écrire le protocole « à l'aveugle ».

Le client officiel n'est pas redistribuable ; il se télécharge ici :
    https://interactivebrokers.github.io/downloads/twsapi_macunix.1030.01.zip

Usage :
    PYTHONPATH=<zip>/IBJts/source/pythonclient python3 gen_golden.py
Les deux fichiers sont réécrits à côté de ce script.
"""

import json
import os
from decimal import Decimal

from ibapi.client import EClient
from ibapi.contract import Contract
from ibapi.decoder import Decoder
from ibapi.order import Order
from ibapi.order_cancel import OrderCancel
from ibapi.wrapper import EWrapper

HERE = os.path.dirname(os.path.abspath(__file__))


class Capture:
    """Connexion factice : garde ce que le client officiel enverrait."""

    def __init__(self):
        self.sent = []

    def sendMsg(self, msg):
        self.sent.append(msg)

    def isConnected(self):
        return True


def client(sv):
    c = EClient(EWrapper())
    c.conn = Capture()
    c.serverVersion_ = sv
    c.connState = EClient.CONNECTED
    c.clientId = 7
    return c


def fx(base, quote):
    k = Contract()
    k.symbol, k.secType, k.exchange, k.currency = base, "CASH", "IDEALPRO", quote
    return k


def order(action, qty, otype, lmt=None, aux=None, tif="", transmit=True, parent=0):
    o = Order()
    o.action, o.totalQuantity, o.orderType = action, Decimal(qty), otype
    if lmt is not None:
        o.lmtPrice = lmt
    if aux is not None:
        o.auxPrice = aux
    o.tif, o.account, o.orderRef = tif, "DU1234567", "gw"
    o.transmit, o.parentId = transmit, parent
    return o


def requests():
    out = []
    for sv in (163, 176, 187):
        c = client(sv)
        calls = [
            ("startApi", lambda: c.startApi()),
            ("reqIds", lambda: c.reqIds(1)),
            ("reqMktData", lambda: c.reqMktData(1001, fx("EUR", "USD"), "", False, False, [])),
            ("cancelMktData", lambda: c.cancelMktData(1001)),
            ("reqAccountSummary", lambda: c.reqAccountSummary(9001, "All", "NetLiquidation,InitMarginReq")),
            ("cancelAccountSummary", lambda: c.cancelAccountSummary(9001)),
            ("reqPositions", lambda: c.reqPositions()),
            ("cancelPositions", lambda: c.cancelPositions()),
            ("cancelOrder", lambda: c.cancelOrder(42, OrderCancel())),
            ("placeOrder.parent", lambda: c.placeOrder(
                42, fx("EUR", "USD"), order("BUY", 20000, "MKT", tif="DAY", transmit=False))),
            ("placeOrder.takeProfit", lambda: c.placeOrder(
                43, fx("EUR", "USD"), order("SELL", 20000, "LMT", lmt=1.0925, tif="GTC", transmit=False, parent=42))),
            ("placeOrder.stopLoss", lambda: c.placeOrder(
                44, fx("EUR", "USD"), order("SELL", 20000, "STP", aux=1.07655, tif="GTC", transmit=True, parent=42))),
            ("placeOrder.exitJPY", lambda: c.placeOrder(
                45, fx("USD", "JPY"), order("SELL", 15000, "MKT", tif="DAY"))),
        ]
        for name, call in calls:
            c.conn.sent.clear()
            call()
            assert len(c.conn.sent) == 1, name
            out.append(f"{name}\t{sv}\t{c.conn.sent[0].hex()}")
    with open(os.path.join(HERE, "golden_requests.txt"), "w") as f:
        f.write("# nom\tversion_serveur\ttrame (hex) — produit par gen_golden.py, NE PAS ÉDITER\n")
        f.write("\n".join(out) + "\n")


class Recorder(EWrapper):
    def __init__(self):
        super().__init__()
        self.calls = []

    def tickPrice(self, reqId, tickType, price, attrib):
        self.calls.append({"tickPrice": [reqId, tickType, price]})

    def tickSize(self, reqId, tickType, size):
        pass

    def orderStatus(self, orderId, status, filled, remaining, avgFillPrice, permId,
                    parentId, lastFillPrice, clientId, whyHeld, mktCapPrice):
        self.calls.append({"orderStatus": [orderId, status, float(filled), float(remaining),
                                           avgFillPrice, parentId, whyHeld]})

    def error(self, reqId, errorCode, errorString, advancedOrderRejectJson=""):
        self.calls.append({"error": [reqId, errorCode, errorString]})

    def nextValidId(self, orderId):
        self.calls.append({"nextValidId": [orderId]})

    def execDetails(self, reqId, contract, execution):
        self.calls.append({"execDetails": [
            reqId, execution.orderId, contract.symbol, contract.secType, contract.currency,
            execution.execId, execution.time, execution.acctNumber, execution.side,
            float(execution.shares), execution.price]})

    def managedAccounts(self, accountsList):
        self.calls.append({"managedAccounts": [accountsList]})

    def commissionReport(self, r):
        self.calls.append({"commissionReport": [r.execId, r.commission, r.currency, r.realizedPNL]})

    def position(self, account, contract, position, avgCost):
        self.calls.append({"position": [account, contract.symbol, contract.secType,
                                        contract.currency, float(position), avgCost]})

    def positionEnd(self):
        self.calls.append({"positionEnd": []})

    def accountSummary(self, reqId, account, tag, value, currency):
        self.calls.append({"accountSummary": [reqId, account, tag, value, currency]})

    def accountSummaryEnd(self, reqId):
        self.calls.append({"accountSummaryEnd": [reqId]})


UNSET = "1.7976931348623157E308"


def incoming(sv):
    """Messages entrants, écrits d'après decoder.py, lus par le décodeur officiel."""
    execution = ["11", "-1", "44", "12087792", "EUR", "CASH", "", "0.0", "", "", "IDEALPRO",
                 "USD", "EUR.USD", "EUR.USD", "0000e0d5.65f1a3b2.01.01", "20240115  10:30:45 US/Eastern",
                 "DU1234567", "IDEALPRO", "SLD", "20000", "1.07655", "1735", "7", "0", "20000",
                 "1.07655", "gw", "", "", "", "2"]
    if sv >= 178:
        execution.append("0")
    return [
        ["1", "6", "1001", "1", "1.08761", "1000000", "0"],
        ["1", "6", "1001", "2", "1.08765", "2000000", "0"],
        ["1", "6", "1002", "66", "151.203", "0", "0"],
        ["3", "44", "Filled", "20000", "0", "1.07655", "1735", "42", "1.07655", "7", "", "0"],
        ["3", "43", "Cancelled", "0", "20000", "0", "1736", "42", "0", "7", "", "0"],
        ["4", "2", "43", "202", "Order Canceled - reason:", ""],
        ["4", "2", "-1", "2104", "Market data farm connection is OK:usfarm", ""],
        ["9", "1", "42"],
        execution,
        ["15", "1", "DU1234567,DU7654321,"],
        ["59", "1", "0000e0d5.65f1a3b2.01.01", "2.0", "USD", "-212.5", UNSET, "0"],
        ["59", "1", "0000e0d5.65f1a3b1.01.01", "2.0", "USD", UNSET, UNSET, "0"],
        ["61", "3", "DU1234567", "12087792", "EUR", "CASH", "", "0.0", "", "", "IDEALPRO", "USD",
         "EUR.USD", "EUR.USD", "-20000", "1.08761"],
        ["62", "1"],
        ["63", "1", "9001", "DU1234567", "NetLiquidation", "100250.75", "USD"],
        ["64", "1", "9001"],
    ]


def decoded():
    out = []
    for sv in (176, 187):
        for fields in incoming(sv):
            rec = Recorder()
            d = Decoder(rec, sv)
            d.interpret([f.encode() for f in fields])
            out.append({"sv": sv, "fields": fields, "calls": rec.calls})
    with open(os.path.join(HERE, "golden_incoming.json"), "w") as f:
        json.dump(out, f, indent=1, ensure_ascii=False)
        f.write("\n")


if __name__ == "__main__":
    requests()
    decoded()
