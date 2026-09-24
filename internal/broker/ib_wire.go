package broker

// Couche « fil » du protocole TWS : trames, champs, versions.
//
// Tout ce fichier et ib_protocol.go sont écrits CONTRE la source du client
// officiel d'Interactive Brokers (API 10.30, client Python : comm.py,
// client.py, decoder.py), pas de mémoire. La preuve n'est pas ce
// commentaire : ce sont les messages de référence de
// testdata/ib/golden_requests.txt, produits par le client officiel
// lui-même (testdata/ib/gen_golden.py), que les tests exigent de
// retrouver octet pour octet.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Plage de versions annoncée à la poignée de main. Le maximum est celui du
// client officiel 10.30 dont on a relu chaque message : annoncer plus
// ferait parler au serveur un dialecte dont on ignore les champs.
//
// Le minimum EXIGÉ du serveur est 163 (TWS 10.10, 2021 — quantités
// décimales partout). En dessous, chaque message gagne des variantes
// qu'aucun test ne couvrirait, pour des versions de TWS qu'IB ne maintient
// plus : on refuse la connexion en le disant.
const (
	ibClientMinVersion = 100
	ibClientMaxVersion = 187
	ibMinServerVersion = 163
)

// Versions de serveur qui changent la forme d'un message utilisé ici
// (valeurs de server_versions.py, API 10.30). Celles qui sont ≤ 163 sont
// toujours satisfaites et n'apparaissent pas.
const (
	ibSvAdvancedOrderReject  = 166
	ibSvManualOrderTime      = 169
	ibSvPegBestPegMidOffsets = 170
	ibSvFAProfileDesupport   = 177
	ibSvPendingPriceRevision = 178
	ibSvCustomerAccount      = 183
	ibSvProfessionalCustomer = 184
	ibSvRFQFields            = 187
)

// Valeurs « non renseignées » du protocole (const.py) : Integer.MAX_VALUE
// et Double.MAX_VALUE côté Java.
const (
	ibUnsetInt = math.MaxInt32
)

var ibUnsetFloat = math.MaxFloat64

// ibMaxMsgLen : taille maximale d'un message (0xFFFFFF, comme le client
// officiel). Au-delà, le flux est désynchronisé : mieux vaut couper que
// d'allouer ce qu'un octet corrompu réclamerait.
const ibMaxMsgLen = 0xFFFFFF

// ibMsg construit le corps d'un message : des champs texte terminés par
// un octet nul.
type ibMsg struct{ b []byte }

func (m *ibMsg) str(s string) *ibMsg {
	m.b = append(m.b, s...)
	m.b = append(m.b, 0)
	return m
}

func (m *ibMsg) int(v int64) *ibMsg { return m.str(strconv.FormatInt(v, 10)) }

// bool : encodé en entier, comme make_field(bool) du client officiel.
func (m *ibMsg) bool(v bool) *ibMsg {
	if v {
		return m.str("1")
	}
	return m.str("0")
}

// float reproduit str(float) de Python, que le client officiel envoie :
// « 0.0 », « 1.08765 », et « 1.7976931348623157e+308 » pour la valeur non
// renseignée. TWS relit ces trois formes ; les reproduire à l'identique
// permet surtout de comparer nos messages OCTET POUR OCTET à ceux du
// client officiel.
func (m *ibMsg) float(v float64) *ibMsg { return m.str(pyFloat(v)) }

// floatMax : make_field_handle_empty — la valeur non renseignée part vide.
func (m *ibMsg) floatMax(v float64) *ibMsg {
	if v == ibUnsetFloat {
		return m.str("")
	}
	return m.float(v)
}

// intMax : même règle pour un entier.
func (m *ibMsg) intMax(v int64) *ibMsg {
	if v == ibUnsetInt {
		return m.str("")
	}
	return m.int(v)
}

// pyFloat : str(float) de Python pour les valeurs que ce programme envoie.
// Entre 1e-4 et 1e16, Python écrit la plus courte décimale exacte, avec
// « .0 » si elle est entière ; au-delà, une notation exponentielle.
func pyFloat(v float64) string {
	if v == ibUnsetFloat {
		return "1.7976931348623157e+308"
	}
	a := math.Abs(v)
	if v == 0 || (a >= 1e-4 && a < 1e16) {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	// Hors de la plage des prix et des quantités : forme exponentielle,
	// que TWS relit aussi. Le cas n'est pas atteint par ce programme.
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// frame préfixe un corps de sa longueur sur quatre octets gros-boutistes.
func ibFrame(body []byte) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

// ibHandshake : « API\0 » puis, en trame, la plage de versions.
func ibHandshake() []byte {
	versions := fmt.Sprintf("v%d..%d", ibClientMinVersion, ibClientMaxVersion)
	return append([]byte("API\x00"), ibFrame([]byte(versions))...)
}

// ibReadFrame lit une trame complète.
func ibReadFrame(r *bufio.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > ibMaxMsgLen {
		return nil, fmt.Errorf("trame TWS de %d octets : flux désynchronisé", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// ibSplit découpe un corps en champs. Chaque champ est TERMINÉ par un nul,
// le dernier découpage (vide) n'en est donc pas un.
func ibSplit(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	parts := strings.Split(string(body), "\x00")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ibFields lit les champs d'un message reçu, dans l'ordre. La première
// erreur est gardée et toutes les lectures suivantes renvoient zéro : le
// décodeur la consulte UNE fois à la fin, au lieu de tester chaque champ.
type ibFields struct {
	f   []string
	i   int
	err error
}

func (r *ibFields) next() string {
	if r.err != nil {
		return ""
	}
	if r.i >= len(r.f) {
		r.err = fmt.Errorf("message TWS tronqué : champ %d absent sur %d", r.i+1, len(r.f))
		return ""
	}
	s := r.f[r.i]
	r.i++
	return s
}

func (r *ibFields) skip(n int) {
	for i := 0; i < n; i++ {
		r.next()
	}
}

func (r *ibFields) str() string { return r.next() }

func (r *ibFields) int() int64 {
	s := r.next()
	if s == "" || r.err != nil {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		r.err = fmt.Errorf("entier TWS illisible %q", s)
	}
	return v
}

// float lit un double. Un champ vide vaut zéro, comme decode(float) du
// client officiel.
func (r *ibFields) float() float64 {
	s := r.next()
	if s == "" || r.err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		r.err = fmt.Errorf("nombre TWS illisible %q", s)
	}
	return v
}

// decimal lit une quantité (Decimal côté IB). Vide ou valeur sentinelle =
// non renseignée, rendue comme zéro avec ok = false.
func (r *ibFields) decimal() (float64, bool) {
	s := r.next()
	switch s {
	case "", "2147483647", "9223372036854775807", "1.7976931348623157E308", "-9223372036854775808":
		return 0, false
	}
	if r.err != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		r.err = fmt.Errorf("quantité TWS illisible %q", s)
		return 0, false
	}
	return v, true
}

func (r *ibFields) bool() bool { return r.int() != 0 }

// ibIsUnset : une valeur double « non renseignée » relue depuis TWS.
func ibIsUnset(v float64) bool { return v >= 1e300 || math.IsInf(v, 0) || math.IsNaN(v) }
