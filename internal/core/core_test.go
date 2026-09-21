package core

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestBusDeliversToAllSubscribers(t *testing.T) {
	bus := NewBus()
	a := bus.Subscribe(TopicTick, 4)
	b := bus.Subscribe(TopicTick, 4)
	defer a.Close()
	defer b.Close()

	bus.Publish(TopicTick, Tick{Symbol: "EURUSD"})
	for _, sub := range []*Subscription{a, b} {
		select {
		case evt := <-sub.C():
			if evt.Payload.(Tick).Symbol != "EURUSD" {
				t.Fatal("charge utile altérée")
			}
		case <-time.After(time.Second):
			t.Fatal("abonné non servi")
		}
	}
}

// TestBusNeverBlocksProducer est la garantie centrale du bus : un abonné
// lent ne doit jamais faire attendre — ni tuer — la boucle de flux de prix
// d'un broker.
func TestBusNeverBlocksProducer(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(TopicTick, 2)
	defer sub.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			bus.Publish(TopicTick, i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("le producteur a été bloqué par un abonné qui ne lit pas")
	}
	if sub.Dropped() == 0 {
		t.Fatal("les pertes doivent être COMPTÉES, pas masquées")
	}
}

func TestBusDropsOldestKeepsNewest(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(TopicTick, 2)
	defer sub.Close()
	for i := 0; i < 5; i++ {
		bus.Publish(TopicTick, i)
	}
	// Un prix périmé n'intéresse personne : c'est le plus RÉCENT qu'on
	// garde.
	var last int
	for {
		select {
		case evt := <-sub.C():
			last = evt.Payload.(int)
			continue
		default:
		}
		break
	}
	if last != 4 {
		t.Fatalf("dernier événement reçu %d, attendu 4 (le plus récent)", last)
	}
}

func TestCloseTwiceIsSafe(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(TopicSignal, 1)
	sub.Close()
	sub.Close() // une séquence d'arrêt jouée deux fois ne doit pas paniquer
	if bus.SubscriberCount(TopicSignal) != 0 {
		t.Fatal("l'abonnement doit être retiré du bus")
	}
}

func TestPublishWithoutSubscriberIsNoop(t *testing.T) {
	bus := NewBus()
	bus.Publish(TopicTick, 1) // ne doit rien faire, surtout pas paniquer
}

func TestBusConcurrentSubscribeAndPublish(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := bus.Subscribe(TopicTick, 8)
			defer sub.Close()
			for j := 0; j < 50; j++ {
				bus.Publish(TopicTick, j)
			}
		}()
	}
	wg.Wait()
}

func TestLogBufferEvictsOldest(t *testing.T) {
	b := NewLogBuffer(3)
	for i := 0; i < 5; i++ {
		b.Append(LogLine{Message: string(rune('a' + i))})
	}
	lines := b.Lines()
	if len(lines) != 3 {
		t.Fatalf("%d lignes conservées, 3 attendues", len(lines))
	}
	if lines[0].Message != "c" || lines[2].Message != "e" {
		t.Fatalf("mauvaise éviction : %v", lines)
	}
	if b.Seq() != 5 {
		t.Fatalf("le compteur total doit valoir 5, reçu %d", b.Seq())
	}
}

func TestSetupLoggingWritesToBuffer(t *testing.T) {
	bus := NewBus()
	lg, err := SetupLogging("", slog.LevelInfo, 10, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	lg.Logger.Info("essai", "cle", "valeur")
	lines := lg.Buffer.Lines()
	if len(lines) != 1 || lines[0].Message != "essai" {
		t.Fatalf("la ligne n'est pas arrivée au tampon : %v", lines)
	}
	if lines[0].Attrs != "cle=valeur" {
		t.Fatalf("attributs perdus : %q", lines[0].Attrs)
	}
}

func TestParseLevelRejectsUnknown(t *testing.T) {
	if _, err := ParseLevel("bavard"); err == nil {
		t.Fatal("un niveau inconnu doit faire échouer le démarrage, pas retomber en silence sur info")
	}
	if lvl, err := ParseLevel("WARN"); err != nil || lvl != slog.LevelWarn {
		t.Fatalf("la casse doit être tolérée : %v / %v", lvl, err)
	}
}

func TestSeriesIndexAtOrAfter(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	s := make(Series, 10)
	for i := range s {
		s[i] = Bar{Time: start.Add(time.Duration(i) * time.Hour)}
	}
	if got := s.IndexAtOrAfter(start.Add(3 * time.Hour)); got != 3 {
		t.Fatalf("indice %d, attendu 3", got)
	}
	if got := s.IndexAtOrAfter(start.Add(3*time.Hour + time.Minute)); got != 4 {
		t.Fatalf("un instant entre deux bougies doit donner la SUIVANTE, reçu %d", got)
	}
	if got := s.IndexAtOrAfter(start.Add(100 * time.Hour)); got != 10 {
		t.Fatalf("au-delà de la série : len(s) attendu, reçu %d", got)
	}
}

func TestDayLossPct(t *testing.T) {
	a := AccountState{Equity: 950, DayStartEquity: 1000}
	pct, ok := a.DayLossPct()
	if !ok || pct != 5 {
		t.Fatalf("perte du jour %v (%v), attendu 5 %%", pct, ok)
	}
	if _, ok := (AccountState{Equity: 100}).DayLossPct(); ok {
		t.Fatal("sans repère de début de journée, la perte n'est PAS calculable")
	}
}

func TestHasAskSide(t *testing.T) {
	if (Series{{BidClose: 1}}).HasAskSide() {
		t.Fatal("une série sans ask ne doit pas prétendre en avoir un")
	}
	if !(Series{{BidClose: 1}, {BidClose: 1, AskClose: 1.1}}).HasAskSide() {
		t.Fatal("une seule bougie avec ask suffit")
	}
}

// TestBusCloseDuringPublishIsSafe reproduit la panique « send on closed
// channel » : un abonné qui se ferme pendant qu'un producteur publie.
// Sans verrou tenu pendant l'envoi, le canal pouvait être fermé entre la
// vérification de l'état et l'envoi — et le programme s'arrêtait net.
func TestBusCloseDuringPublishIsSafe(t *testing.T) {
	for round := 0; round < 200; round++ {
		bus := NewBus()
		sub := bus.Subscribe(TopicTick, 1)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				bus.Publish(TopicTick, i)
			}
		}()
		go func() {
			defer wg.Done()
			sub.Close()
		}()
		wg.Wait()
	}
}

// TestBusUnsubscribeDuringPublishIsSafe : le désabonnement ne doit pas
// réécrire le tableau d'abonnés que Publish est en train de parcourir.
func TestBusUnsubscribeDuringPublishIsSafe(t *testing.T) {
	bus := NewBus()
	subs := make([]*Subscription, 0, 16)
	for i := 0; i < 16; i++ {
		subs = append(subs, bus.Subscribe(TopicTick, 4))
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			bus.Publish(TopicTick, i)
		}
	}()
	go func() {
		defer wg.Done()
		for _, s := range subs {
			s.Close()
		}
	}()
	wg.Wait()

	if n := bus.SubscriberCount(TopicTick); n != 0 {
		t.Fatalf("%d abonnement(s) restant(s) après fermeture", n)
	}
}
