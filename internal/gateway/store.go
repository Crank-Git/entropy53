package gateway

import (
	"container/list"
	"sync"

	"github.com/Crank-Git/entropy53/chain"
)

// storeCapacity is the number of rounds the store holds (FR-upstream-14).
const storeCapacity = 100000

type storeKey struct {
	beaconID string
	number   uint64
}

type storeEntry struct {
	key   storeKey
	round chain.Round
}

// store holds verified rounds and evicts the least recently used round when it is full (FR-upstream-15).
// Only verified rounds enter it. It is safe for concurrent use.
type store struct {
	mu       sync.Mutex
	capacity int
	order    *list.List // Front is the most recently used entry.
	entries  map[storeKey]*list.Element
	latest   map[string]chain.Round // Highest verified round for each beacon ID, written by followers.
}

func newStore(capacity int) *store {
	return &store{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[storeKey]*list.Element),
		latest:   make(map[string]chain.Round),
	}
}

// put keeps a verified round of the chain with beaconID and marks it as recently used.
func (s *store) put(beaconID string, r chain.Round) {
	key := storeKey{beaconID, r.Number}
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.entries[key]; ok {
		el.Value.(*storeEntry).round = r
		s.order.MoveToFront(el)
		return
	}
	s.entries[key] = s.order.PushFront(&storeEntry{key: key, round: r})
	if s.order.Len() > s.capacity {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		delete(s.entries, oldest.Value.(*storeEntry).key)
	}
}

// advanceLatest records r as the latest round of the chain with beaconID when r is higher than the recorded one.
// The latest round is separate from the LRU list, so an eviction cannot remove it (FR-upstream-21).
func (s *store) advanceLatest(beaconID string, r chain.Round) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.latest[beaconID]; ok && cur.Number >= r.Number {
		return
	}
	s.latest[beaconID] = r
}

// latestRound returns the latest round of the chain with beaconID, and false when none is recorded.
func (s *store) latestRound(beaconID string) (chain.Round, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.latest[beaconID]
	return r, ok
}

// get returns round number of the chain with beaconID and marks it as recently used.
// A read changes the eviction order, so get takes the same lock as put.
func (s *store) get(beaconID string, number uint64) (chain.Round, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.entries[storeKey{beaconID, number}]
	if !ok {
		return chain.Round{}, false
	}
	s.order.MoveToFront(el)
	return el.Value.(*storeEntry).round, true
}
