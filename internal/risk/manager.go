// Package risk contient le SEUL module autorisé à transformer un signal en
// ordre.
//
// Le flux est strict : Strategy → Signal → risk.Manager → OrderRequest →
// BrokerGateway. Aucun ordre ne part au broker — réel OU simulé en
// backtest — sans passer ici. C'est une règle d'architecture, pas une
// convention : elle garantit qu'il n'existe aucun chemin, même de
// débogage, par lequel une stratégie pourrait trader sans limite.
package risk

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
)

// Decision explique ce que le risque a fait d'un signal. L'explication est
// aussi importante que la décision : une stratégie qui ne trade pas doit
// pouvoir dire POURQUOI, sinon l'utilisateur soupçonne une panne.
type Decision struct {
	Order *core.OrderRequest
	// Reason est vide quand l'ordre est accepté.
	Reason string
	// Capped : la taille calculée a été RAMENÉE au plafond
	// `max_position_size`. L'ordre part quand même, mais il ne risque
	// plus le pourcentage demandé — et sans ce drapeau, rien ne
	// distinguerait un dimensionnement au risque qui fonctionne d'un
	// plafond qui le neutralise à chaque entrée.
	Capped bool
}

// Accepted indique si un ordre a été produit.
func (d Decision) Accepted() bool { return d.Order != nil }

// Motifs de rejet, stables (l'interface les agrège par type).
const (
	ReasonHold           = "signal neutre"
	ReasonNothingToClose = "aucune position à fermer"
	ReasonSymbolFull     = "plafond de positions sur ce symbole atteint"
	ReasonAccountFull    = "plafond de positions sur le compte atteint"
	ReasonDailyLoss      = "perte journalière maximale atteinte"
	ReasonInvalidSize    = "taille de position invalide"
	// Motifs propres au dimensionnement au RISQUE (risk_per_trade_pct > 0).
	// Chacun dit ce qui manque : aucune de ces situations ne doit se
	// résoudre en repliant sur une taille arbitraire, qui risquerait un
	// montant que personne n'a choisi.
	ReasonNoEquity        = "équité inconnue : dimensionnement au risque impossible"
	ReasonNoStop          = "signal sans stop exploitable : dimensionnement au risque impossible"
	ReasonUnconvertible   = "devise non convertible : dimensionnement au risque impossible"
	ReasonRiskBudgetSmall = "budget de risque insuffisant pour une seule unité"
)

// Manager applique les limites de la section `risk` de la configuration.
type Manager struct {
	maxPositionSize       float64
	maxPositionsPerSymbol int
	maxOpenPositions      int
	maxDailyLossPct       float64
	fixedPositionSize     float64
	riskPerTradePct       float64
	accountCurrency       string
	logger                *slog.Logger

	// Compteurs de rejets, pour que l'interface puisse expliquer un
	// silence prolongé sans obliger à lire les journaux.
	//
	// Le verrou n'est PAS décoratif : une seule instance de Manager est
	// câblée dans app.New et servie simultanément aux plis du
	// walk-forward (qui tournent en parallèle) et au moteur live (une
	// goroutine de rejeu par symbole). Sans lui, deux `counts[motif]++`
	// simultanés font tomber le programme sur « fatal error: concurrent
	// map writes » — une panique du runtime, irrattrapable, au beau
	// milieu d'un entraînement ou d'une séance.
	mu     sync.Mutex
	counts map[string]int
	// capped : entrées dont la taille a été ramenée au plafond. Ce n'est
	// pas un refus — l'ordre part — mais ce n'est plus le risque demandé,
	// et le confondre avec un dimensionnement qui fonctionne serait
	// exactement le genre de silence que ce programme s'interdit.
	capped int
	// currencyWarned : l'avertissement de devise divergente n'est donné
	// qu'UNE fois. Répété à chaque bougie, il noierait le journal.
	currencyWarned bool
}

// New construit un gestionnaire depuis la configuration validée.
//
// `accountCurrency` est la devise dans laquelle le budget de risque est
// exprimé. Elle ne sert qu'au dimensionnement au risque ; sans lui, une
// distance de stop mesurée en yens serait comparée à une équité en
// dollars.
func New(cfg config.RiskConfig, accountCurrency string, logger *slog.Logger) *Manager {
	return &Manager{
		maxPositionSize:       cfg.MaxPositionSize,
		maxPositionsPerSymbol: cfg.MaxPositionsPerSymbol,
		maxOpenPositions:      cfg.MaxOpenPositions,
		maxDailyLossPct:       cfg.MaxDailyLossPct,
		fixedPositionSize:     cfg.FixedPositionSize,
		riskPerTradePct:       cfg.RiskPerTradePct,
		accountCurrency:       accountCurrency,
		logger:                logger,
		counts:                map[string]int{},
	}
}

// MaxPositionSize : PLAFOND nominal d'une entrée, en unités de devise de
// base.
func (m *Manager) MaxPositionSize() float64 { return m.maxPositionSize }

// Fork renvoie un gestionnaire aux MÊMES limites, avec des compteurs de
// rejet NEUFS.
//
// Les compteurs disent « pourquoi ce backtest n'a presque pas tradé » :
// c'est une phrase sur UN run. Partagés entre les runs — un seul Manager
// est câblé dans app.New pour tout le programme — ils cumulaient depuis le
// démarrage, et `AggregateStats` additionnait ensuite ces cumuls
// chevauchants pli par pli. Le nombre publié dans run.json ne mesurait
// alors plus rien et changeait d'une exécution à l'autre au gré de
// l'ordonnancement des plis parallèles.
//
// Les LIMITES, elles, restent celles de la configuration : un signal
// rejeté dans un run forké l'aurait été en live.
// Les champs sont recopiés un à un : un `*m` global copierait le verrou,
// ce que `go vet` refuse à juste titre.
func (m *Manager) Fork() *Manager {
	return &Manager{
		maxPositionSize:       m.maxPositionSize,
		maxPositionsPerSymbol: m.maxPositionsPerSymbol,
		maxOpenPositions:      m.maxOpenPositions,
		maxDailyLossPct:       m.maxDailyLossPct,
		fixedPositionSize:     m.fixedPositionSize,
		riskPerTradePct:       m.riskPerTradePct,
		accountCurrency:       m.accountCurrency,
		logger:                m.logger,
		counts:                map[string]int{},
	}
}

// Rejections renvoie une copie des compteurs de rejet par motif.
func (m *Manager) Rejections() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.counts))
	for k, v := range m.counts {
		out[k] = v
	}
	return out
}

// Evaluate valide un signal et le convertit en ordre, ou le rejette.
//
// `open` = positions actuellement ouvertes (réelles en live, simulées en
// backtest). `account` = état RÉEL du compte chez le broker ; fourni en
// live uniquement, il active la limite de perte journalière.
func (m *Manager) Evaluate(sig core.Signal, open []core.Position, account *core.AccountState) Decision {
	switch sig.Action {
	case core.Hold, "":
		return m.reject(ReasonHold)

	case core.Exit:
		pos := findPosition(open, sig.Symbol)
		if pos == nil {
			return m.reject(ReasonNothingToClose)
		}
		// Une SORTIE n'est JAMAIS bloquée par une limite : quel que soit
		// l'état du compte, on doit toujours pouvoir fermer une position.
		return Decision{Order: &core.OrderRequest{
			Symbol:   sig.Symbol,
			Side:     sideToClose(*pos),
			Quantity: absf(pos.Quantity),
			Type:     core.Market,
		}}
	}

	// --- À partir d'ici : ENTRÉES uniquement ---
	if m.maxPositionSize <= 0 {
		return m.reject(ReasonInvalidSize)
	}
	if m.dailyLossReached(account) {
		return m.reject(ReasonDailyLoss)
	}
	if reason := m.positionRoom(sig.Symbol, open); reason != "" {
		return m.reject(reason)
	}

	quantity := m.fixedPositionSize
	capped := false
	if m.riskPerTradePct > 0 {
		sized, hit, reason := m.sizeByRisk(sig, account)
		if reason != "" {
			return m.reject(reason)
		}
		quantity, capped = sized, hit
	}
	if quantity > m.maxPositionSize {
		quantity, capped = m.maxPositionSize, true
	}
	// Dernier filet : une quantité nulle ou négative n'est pas un ordre.
	// Le contrôle porte sur la taille RÉELLEMENT retenue, pas sur le seul
	// plafond — c'est la seule qui parte au broker.
	if quantity <= 0 {
		return m.reject(ReasonInvalidSize)
	}

	side := core.Buy
	if sig.Action == core.EnterShort {
		side = core.Sell
	}
	// Les barrières proposées par la stratégie sont REPORTÉES sur l'ordre :
	// leur exécution ultérieure découle d'un ordre déjà validé, elle ne
	// contourne pas le contrôle du risque.
	if capped {
		m.countCapped()
	}
	return Decision{Order: &core.OrderRequest{
		Symbol:     sig.Symbol,
		Side:       side,
		Quantity:   quantity,
		Type:       core.Market,
		StopLoss:   sig.StopLoss,
		TakeProfit: sig.TakeProfit,
	}, Capped: capped}
}

// sizeByRisk calcule la taille pour que la distance jusqu'au stop coûte
// exactement `riskPerTradePct` % de l'équité :
//
//	budget   = équité × riskPerTradePct / 100          (devise du compte)
//	unitaire = |prix − stop| converti en devise du compte
//	quantité = plancher(budget / unitaire), plafonnée par maxPositionSize
//
// À taille fixe, la perte au stop suit l'ATR : elle double quand la
// volatilité double, sans que personne ne l'ait décidé. Ici c'est la
// taille qui bouge et la perte qui reste constante.
//
// Toute donnée manquante REFUSE l'entrée au lieu de se rabattre sur la
// taille maximale : un repli silencieux risquerait un montant que
// l'utilisateur n'a pas choisi, précisément le jour où la mesure a
// échoué.
func (m *Manager) sizeByRisk(sig core.Signal, account *core.AccountState) (float64, bool, string) {
	if account == nil || account.Equity <= 0 {
		return 0, false, ReasonNoEquity
	}
	if sig.Price <= 0 || sig.StopLoss <= 0 {
		return 0, false, ReasonNoStop
	}
	distance := math.Abs(sig.Price - sig.StopLoss)
	if distance <= 0 {
		return 0, false, ReasonNoStop
	}
	// La distance naît dans la devise de COTATION de la paire ; le budget
	// vit dans celle du compte. Sans taux tiers, on ne convertit pas et on
	// ne devine pas.
	m.warnCurrencyMismatch(account.Currency)
	conv := data.ConversionFor(sig.Symbol, m.accountCurrency)
	if !conv.Exact {
		return 0, false, ReasonUnconvertible
	}
	perUnit := conv.ToAccount(distance, sig.Price)
	if perUnit <= 0 {
		return 0, false, ReasonUnconvertible
	}
	// Plancher — jamais plus que le budget — après absorption de l'erreur
	// de représentation binaire : 1,1000 − 1,0900 vaut en flottant
	// 0,010000000000000009, ce qui ferait tomber 10 000 unités exactes à
	// 9 999. La tolérance est relative et minuscule (1e-9) : elle rattrape
	// l'arrondi de la machine, jamais un vrai dépassement de budget.
	raw := account.Equity * m.riskPerTradePct / 100 / perUnit
	quantity := math.Floor(raw * (1 + 1e-9))
	if quantity < 1 {
		return 0, false, ReasonRiskBudgetSmall
	}
	// Le plafond est appliqué par l'appelant, qui le compte : une taille
	// rabotée n'est pas un refus, mais ce n'est plus le risque demandé.
	return quantity, quantity > m.maxPositionSize, ""
}

// warnCurrencyMismatch : le courtier annonce-t-il la devise que la
// configuration prétend ?
//
// Le budget de risque est calculé dans la devise CONFIGURÉE
// (`backtest.account_currency`). Si le compte réel est libellé autrement,
// le montant risqué n'est pas celui qu'on croit — et rien d'autre ne le
// dirait.
func (m *Manager) warnCurrencyMismatch(broker string) {
	if broker == "" || strings.EqualFold(broker, m.accountCurrency) {
		return
	}
	m.mu.Lock()
	first := !m.currencyWarned
	m.currencyWarned = true
	m.mu.Unlock()
	if first && m.logger != nil {
		m.logger.Warn("DEVISE DIVERGENTE : le dimensionnement au risque utilise la devise configurée",
			"courtier", broker, "configuree", m.accountCurrency)
	}
}

// SizingReasons : les motifs de refus qui signalent que l'entrée n'a pas
// pu être DIMENSIONNÉE, par opposition à ceux qui traduisent une règle
// normale (signal neutre, plafond de positions atteint).
//
// La distinction compte depuis que le dimensionnement au risque est actif
// par défaut : sur une paire croisée non convertible vers la devise du
// compte, TOUTES les entrées sont refusées. C'est le comportement voulu —
// on ne dimensionne pas au jugé — mais il doit se voir, sinon la
// stratégie paraît simplement muette.
var SizingReasons = []string{
	ReasonNoEquity, ReasonNoStop, ReasonUnconvertible, ReasonRiskBudgetSmall,
}

// SizingRefusals compte, parmi des motifs de refus, ceux qui viennent du
// dimensionnement, et les décrit du plus fréquent au moins fréquent.
func SizingRefusals(counts map[string]int) (int, string) {
	type entry struct {
		reason string
		n      int
	}
	var found []entry
	total := 0
	for _, reason := range SizingReasons {
		if n := counts[reason]; n > 0 {
			found = append(found, entry{reason, n})
			total += n
		}
	}
	if total == 0 {
		return 0, ""
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n > found[j].n })
	parts := make([]string, 0, len(found))
	for _, e := range found {
		parts = append(parts, fmt.Sprintf("%d × %s", e.n, e.reason))
	}
	return total, strings.Join(parts, " · ")
}

// Capped : nombre d'entrées dont la taille a touché le plafond.
func (m *Manager) Capped() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capped
}

func (m *Manager) countCapped() {
	m.mu.Lock()
	m.capped++
	m.mu.Unlock()
}

func (m *Manager) reject(reason string) Decision {
	m.mu.Lock()
	m.counts[reason]++
	m.mu.Unlock()
	return Decision{Reason: reason}
}

// positionRoom vérifie les DEUX plafonds, qui sont distincts.
//
// Les confondre rendrait une seule position ouverte bloquante pour TOUTES
// les autres paires : un plafond global de 1 gèlerait 30 paires sur 31.
func (m *Manager) positionRoom(symbol string, open []core.Position) string {
	onSymbol := 0
	for _, p := range open {
		if p.Symbol == symbol {
			onSymbol++
		}
	}
	if onSymbol >= m.maxPositionsPerSymbol {
		// Rejet ROUTINIER, pas un événement : une stratégie qui réaffirme
		// son biais à chaque bougie produit un rejet par bougie tant que
		// la position est ouverte. En niveau info, ce serait des milliers
		// de lignes par backtest.
		m.debug("signal rejeté : positions déjà ouvertes sur le symbole",
			"symbole", symbol, "ouvertes", onSymbol, "max", m.maxPositionsPerSymbol)
		return ReasonSymbolFull
	}
	if len(open) >= m.maxOpenPositions {
		m.debug("signal rejeté : plafond de positions du compte",
			"symbole", symbol, "ouvertes", len(open), "max", m.maxOpenPositions)
		return ReasonAccountFull
	}
	return ""
}

// dailyLossReached : la perte du jour a-t-elle atteint le plafond ?
//
// Sans état de compte (backtest) ou sans plafond configuré, la règle ne
// s'applique pas — et on ne fait SURTOUT pas semblant de l'appliquer. Le
// backtest est donc, sur ce seul point, un peu optimiste par rapport au
// live : c'est documenté plutôt que simulé de travers.
func (m *Manager) dailyLossReached(account *core.AccountState) bool {
	if account == nil || m.maxDailyLossPct <= 0 {
		return false
	}
	loss, ok := account.DayLossPct()
	if !ok || loss < m.maxDailyLossPct {
		return false
	}
	if m.logger != nil {
		m.logger.Warn("PERTE JOURNALIÈRE ATTEINTE — plus aucune entrée aujourd'hui (les sorties restent autorisées)",
			"perte_pct", round2(loss), "plafond_pct", m.maxDailyLossPct,
			"equite", round2(account.Equity), "equite_debut_jour", round2(account.DayStartEquity))
	}
	return true
}

func (m *Manager) debug(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Debug(msg, args...)
	}
}

func findPosition(open []core.Position, symbol string) *core.Position {
	for i := range open {
		if open[i].Symbol == symbol && open[i].Quantity != 0 {
			return &open[i]
		}
	}
	return nil
}

func sideToClose(p core.Position) core.OrderSide {
	if p.Quantity > 0 {
		return core.Sell
	}
	return core.Buy
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func round2(v float64) string { return fmt.Sprintf("%.2f", v) }
