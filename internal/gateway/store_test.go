package gateway

import (
	"sync"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
)

func roundN(n uint64) chain.Round { return chain.Round{Number: n, Sig: []byte{byte(n)}} }

func TestStoreEvictsLeastRecentlyUsed(t *testing.T) {
	s := newStore(2)
	s.put("quicknet", roundN(1))
	s.put("quicknet", roundN(2))
	s.put("quicknet", roundN(3))

	if _, ok := s.get("quicknet", 1); ok {
		t.Error("round 1 is still in the store")
	}
	for _, n := range []uint64{2, 3} {
		if _, ok := s.get("quicknet", n); !ok {
			t.Errorf("round %d is missing", n)
		}
	}
}

func TestStoreGetMarksRoundAsRecentlyUsed(t *testing.T) {
	s := newStore(2)
	s.put("quicknet", roundN(1))
	s.put("quicknet", roundN(2))
	s.get("quicknet", 1)
	s.put("quicknet", roundN(3))

	if _, ok := s.get("quicknet", 2); ok {
		t.Error("round 2 is still in the store")
	}
	if _, ok := s.get("quicknet", 1); !ok {
		t.Error("round 1 was evicted after a read")
	}
}

func TestStoreKeysRoundsByBeaconID(t *testing.T) {
	s := newStore(4)
	s.put("quicknet", roundN(1))

	if _, ok := s.get("default", 1); ok {
		t.Error("a round of quicknet answers for default")
	}
}

func TestStoreCapacityIs100000(t *testing.T) {
	if storeCapacity != 100000 {
		t.Errorf("storeCapacity = %d, want 100000", storeCapacity)
	}
}

func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	s := newStore(8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := uint64(1); n <= 200; n++ {
				s.put("quicknet", roundN(n))
				s.get("quicknet", n/2+1)
			}
		}()
	}
	wg.Wait()
}
