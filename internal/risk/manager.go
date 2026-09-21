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
	"sync"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Decision explique ce que le risque a fait d'un signal. L'explication est
// aussi importante que la décision : une stratégie qui ne trade pas doit
// pouvoir dire POURQUOI, sinon l'utilisateur soupçonne une panne.
type Decision struct {
	Order *core.OrderRequest
	// Reason est vide quand l'ordre est accepté.
	Reason string
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
)

// Manager applique les limites de la section `risk` de la configuration.
type Manager struct {
	maxPositionSize       float64
	maxPositionsPerSymbol int
	maxOpenPositions      int
	maxDailyLossPct       float64
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
}

// New construit un gestionnaire depuis la configuration validée.
func New(cfg config.RiskConfig, logger *slog.Logger) *Manager {
	return &Manager{
		maxPositionSize:       cfg.MaxPositionSize,
		maxPositionsPerSymbol: cfg.MaxPositionsPerSymbol,
		maxOpenPositions:      cfg.MaxOpenPositions,
		maxDailyLossPct:       cfg.MaxDailyLossPct,
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

	side := core.Buy
	if sig.Action == core.EnterShort {
		side = core.Sell
	}
	// Les barrières proposées par la stratégie sont REPORTÉES sur l'ordre :
	// leur exécution ultérieure découle d'un ordre déjà validé, elle ne
	// contourne pas le contrôle du risque.
	return Decision{Order: &core.OrderRequest{
		Symbol:     sig.Symbol,
		Side:       side,
		Quantity:   m.maxPositionSize,
		Type:       core.Market,
		StopLoss:   sig.StopLoss,
		TakeProfit: sig.TakeProfit,
	}}
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
