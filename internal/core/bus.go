package core

import (
	"sync"
	"time"
)

// Topics standard du système. On n'en déclare que de RÉELLEMENT publiés :
// un topic relayé sans producteur donne l'illusion d'un flux temps réel
// inexistant — du câblage mort qu'on prend pour une fonctionnalité.
const (
	TopicTick      = "market.tick"
	TopicSignal    = "strategy.signal"
	TopicExecution = "broker.execution"
	TopicBroker    = "broker.status"
	TopicJob       = "job.progress"
	TopicLog       = "log.line"
)

// Event est l'enveloppe unique qui circule sur le bus.
type Event struct {
	Topic   string
	Time    time.Time
	Payload any
}

// Bus est un pub/sub en mémoire qui DÉCOUPLE tout : le moteur live, les
// gateways et les jobs publient ; la TUI et la persistance consomment.
// Personne ne connaît personne.
//
// Garantie centrale : **un abonné ne peut jamais bloquer ni tuer un
// producteur**. Chaque abonnement est un canal tamponné ; quand le tampon
// est plein, l'événement le PLUS ANCIEN est jeté au profit du nouveau
// (politique « drop-oldest »). Le producteur d'un tick est la boucle de
// flux de prix d'un broker : la faire attendre un abonné lent, ou la tuer
// sur une panique d'abonné, coûterait le flux de marché du symbole.
// Les pertes sont comptées par abonnement (Dropped) : une TUI en retard se
// voit au lieu de mentir.
type Bus struct {
	mu sync.RWMutex
	// snapshot associe à chaque topic une tranche IMMUABLE d'abonnés.
	//
	// Immuable est le mot important : s'abonner ou se désabonner remplace
	// la tranche au lieu de la modifier. Publish peut donc relâcher le
	// verrou avant de distribuer, sans risquer qu'un désabonnement
	// concurrent réécrive le tableau qu'elle est en train de parcourir.
	snapshot map[string][]*Subscription
	nextID   uint64
}

// Subscription est le point de réception d'un abonné.
type Subscription struct {
	id      uint64
	topic   string
	ch      chan Event
	bus     *Bus
	mu      sync.Mutex
	dropped uint64
	closed  bool
}

// NewBus crée un bus vide.
func NewBus() *Bus { return &Bus{snapshot: make(map[string][]*Subscription)} }

// Subscribe ouvre un abonnement sur un topic. `buffer` dimensionne la
// tolérance au retard de l'abonné (16 est un minimum raisonnable).
func (b *Bus) Subscribe(topic string, buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &Subscription{id: b.nextID, topic: topic, ch: make(chan Event, buffer), bus: b}
	current := b.snapshot[topic]
	updated := make([]*Subscription, len(current), len(current)+1)
	copy(updated, current)
	b.snapshot[topic] = append(updated, sub)
	return sub
}

// Publish diffuse un événement. N'attend JAMAIS un abonné.
func (b *Bus) Publish(topic string, payload any) {
	b.mu.RLock()
	subs := b.snapshot[topic]
	b.mu.RUnlock()
	if len(subs) == 0 {
		return
	}
	evt := Event{Topic: topic, Time: time.Now().UTC(), Payload: payload}
	for _, sub := range subs {
		sub.deliver(evt)
	}
}

// deliver dépose l'événement sans jamais bloquer.
//
// Le verrou est tenu pendant TOUTE l'opération, envoi compris. C'est sûr
// parce que l'envoi est non bloquant (aucun risque d'interblocage), et
// c'est nécessaire : sans lui, un Close() concurrent pouvait fermer le
// canal entre la vérification de `closed` et l'envoi — ce qui provoque une
// panique « send on closed channel », donc l'arrêt brutal du programme.
func (s *Subscription) deliver(evt Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- evt:
		return
	default:
	}
	// Tampon plein : on jette le plus ancien pour garder le plus récent
	// (un prix périmé n'intéresse personne), et on compte.
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- evt:
	default:
	}
	s.dropped++
}

// C est le canal de réception de l'abonnement.
func (s *Subscription) C() <-chan Event { return s.ch }

// Dropped renvoie le nombre d'événements perdus faute de place — un abonné
// honnête affiche ce chiffre plutôt que de faire comme s'il avait tout vu.
func (s *Subscription) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Close retire l'abonnement du bus. Appeler Close deux fois n'est PAS une
// erreur : une séquence d'arrêt jouée deux fois ne doit pas paniquer.
//
// Ordre des opérations, qui compte : on marque l'abonnement fermé (tout
// deliver ultérieur devient un no-op), on le retire du bus, PUIS on ferme
// le canal en tenant le verrou de l'abonnement. À ce moment plus aucun
// deliver ne peut être en cours ni démarrer, donc aucun envoi ne peut
// croiser la fermeture.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.bus.mu.Lock()
	current := s.bus.snapshot[s.topic]
	updated := make([]*Subscription, 0, len(current))
	for _, other := range current {
		if other.id != s.id {
			updated = append(updated, other)
		}
	}
	s.bus.snapshot[s.topic] = updated
	s.bus.mu.Unlock()

	s.mu.Lock()
	close(s.ch)
	s.mu.Unlock()
}

// SubscriberCount sert aux tests et au diagnostic (fuite d'abonnement).
func (b *Bus) SubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.snapshot[topic])
}
