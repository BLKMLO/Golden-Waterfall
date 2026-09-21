// Package storage persiste ce qui doit survivre à un redémarrage : le
// journal des trades RÉELLEMENT exécutés, l'interrupteur de trading par
// paire, et le repère d'équité de début de journée.
//
// Le moteur est bbolt : une base embarquée, en Go PUR, dans un seul
// fichier. SQLite aurait imposé cgo (et donc une chaîne C à la
// compilation) ou une transcription générant plusieurs dizaines de
// mégaoctets. Pour trois collections de documents lues par clé, une base
// clé/valeur transactionnelle fait exactement le travail sans rien coûter à
// la promesse « un seul binaire ».
//
// Règle d'honnêteté : ce paquet n'écrit QUE des faits rapportés par un
// broker. Aucun trade n'est jamais fabriqué pour remplir un écran.
package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

var (
	bucketTrades  = []byte("trades")
	bucketTrading = []byte("trading_state")
	bucketEquity  = []byte("daily_equity")
	bucketMeta    = []byte("meta")
)

// Store est la base embarquée.
type Store struct {
	db *bolt.DB

	// Cache mémoire de l'interrupteur par paire : il est lu à CHAQUE
	// bougie du moteur live. La base reste la source de vérité ; le cache
	// n'est qu'une copie rafraîchie à l'écriture.
	mu      sync.RWMutex
	trading map[string]bool
}

// Open ouvre (ou crée) la base.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("base %s inaccessible (déjà ouverte par une autre instance ?) : %w", path, err)
	}
	s := &Store{db: db, trading: map[string]bool{}}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketTrades, bucketTrading, bucketEquity, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := s.loadTradingCache(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close referme la base.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// --- Journal des trades ----------------------------------------------------

// AppendTrade inscrit un aller-retour RÉELLEMENT exécuté.
func (s *Store) AppendTrade(t core.Trade) (int64, error) {
	var id int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTrades)
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		id = int64(seq)
		t.ID = id
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return b.Put(itob(uint64(seq)), raw)
	})
	return id, err
}

// Trades renvoie les derniers trades, du plus récent au plus ancien.
// limit <= 0 = tout.
func (s *Store) Trades(limit int) ([]core.Trade, error) {
	var out []core.Trade
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketTrades).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var t core.Trade
			if err := json.Unmarshal(v, &t); err != nil {
				continue // enregistrement illisible : ignoré, jamais deviné
			}
			out = append(out, t)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	return out, err
}

// TradeCount renvoie le nombre de trades journalisés.
func (s *Store) TradeCount() (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketTrades).Stats().KeyN
		return nil
	})
	return n, err
}

// --- Interrupteur de trading par paire -------------------------------------

func (s *Store) loadTradingCache() error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTrading).ForEach(func(k, v []byte) error {
			s.trading[string(k)] = len(v) > 0 && v[0] == 1
			return nil
		})
	})
}

// SetTrading arme ou désarme une paire (persisté).
func (s *Store) SetTrading(symbol string, on bool) error {
	val := []byte{0}
	if on {
		val = []byte{1}
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTrading).Put([]byte(symbol), val)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.trading[symbol] = on
	s.mu.Unlock()
	return nil
}

// Trading indique si une paire est armée. Défaut : NON — une paire ne
// trade jamais parce qu'on a oublié de la désarmer.
func (s *Store) Trading(symbol string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trading[symbol]
}

// AllTrading renvoie une copie de l'état de toutes les paires connues.
func (s *Store) AllTrading() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.trading))
	for k, v := range s.trading {
		out[k] = v
	}
	return out
}

// ArmedSymbols renvoie les paires armées, triées.
func (s *Store) ArmedSymbols() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k, v := range s.trading {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// --- Équité de début de journée --------------------------------------------

// DayStartEquity renvoie le repère du jour UTC, et false s'il n'a pas
// encore été posé.
//
// Persister ce repère est ce qui empêche un redémarrage de remettre le
// compteur de perte journalière à zéro — et donc d'autoriser à nouveau des
// entrées après une mauvaise journée.
func (s *Store) DayStartEquity(day time.Time) (float64, bool, error) {
	key := []byte(day.UTC().Format("2006-01-02"))
	var value float64
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketEquity).Get(key)
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &value)
	})
	return value, found, err
}

// SetDayStartEquity pose le repère du jour s'il n'existe pas déjà.
// Ne l'écrase JAMAIS : le repère du jour est fixé une fois, au premier
// relevé d'équité de la journée.
func (s *Store) SetDayStartEquity(day time.Time, equity float64) error {
	key := []byte(day.UTC().Format("2006-01-02"))
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketEquity)
		if b.Get(key) != nil {
			return nil
		}
		raw, err := json.Marshal(equity)
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
}

// --- Métadonnées libres ----------------------------------------------------

// PutMeta écrit une valeur JSON sous une clé.
func (s *Store) PutMeta(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put([]byte(key), raw)
	})
}

// GetMeta lit une valeur JSON. Renvoie false si la clé est absente.
func (s *Store) GetMeta(key string, dst any) (bool, error) {
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketMeta).Get([]byte(key))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, dst)
	})
	return found, err
}

func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
