package chain

import (
	"testing"
	"time"
)

var allChains = []Chain{Quicknet, Default, Evmnet}

var sampleRounds = []uint64{1, 2, 1000, 34448212, 1 << 32, (1 << 53) - 1}

// The inversion tests also cover MaxRound itself.
func withMax() []uint64 {
	return append(append([]uint64{}, sampleRounds...), MaxRound)
}

func TestRoundTimeOfRoundOneIsGenesis(t *testing.T) {
	for _, c := range allChains {
		if got := c.RoundTime(1); !got.Equal(time.Unix(c.Genesis, 0)) {
			t.Fatalf("%s: RoundTime(1) = %v", c.BeaconID, got)
		}
	}
}

func TestRoundTimeAtMaxRoundDoesNotOverflow(t *testing.T) {
	for _, c := range allChains {
		want := time.Unix(c.Genesis+int64(MaxRound-1)*c.Period, 0)
		if got := c.RoundTime(MaxRound); !got.Equal(want) {
			t.Fatalf("%s: RoundTime = %v, want %v", c.BeaconID, got, want)
		}
	}
}

func TestRoundAtInvertsRoundTime(t *testing.T) {
	for _, c := range allChains {
		for _, n := range withMax() {
			got, err := c.RoundAt(c.RoundTime(n))
			if err != nil || got != n {
				t.Fatalf("%s: RoundAt(RoundTime(%d)) = %d, %v", c.BeaconID, n, got, err)
			}
		}
	}
}

func TestRoundAtRoundsDownInsidePeriod(t *testing.T) {
	for _, c := range allChains {
		for _, n := range sampleRounds {
			at := c.RoundTime(n).Add(time.Duration(c.Period)*time.Second - time.Nanosecond)
			got, err := c.RoundAt(at)
			if err != nil || got != n {
				t.Fatalf("%s: RoundAt(%d+period-1ns) = %d, %v", c.BeaconID, n, got, err)
			}
		}
	}
}

func TestRoundAtRejectsBeforeGenesis(t *testing.T) {
	for _, c := range allChains {
		if _, err := c.RoundAt(time.Unix(c.Genesis, 0).Add(-time.Nanosecond)); err == nil {
			t.Fatalf("%s: no error before genesis", c.BeaconID)
		}
	}
}

func TestRoundAtRejectsAboveMaxRound(t *testing.T) {
	for _, c := range allChains {
		at := c.RoundTime(MaxRound).Add(time.Duration(c.Period) * time.Second)
		if _, err := c.RoundAt(at); err == nil {
			t.Fatalf("%s: no error above MaxRound", c.BeaconID)
		}
	}
}

func TestRoundAfterInvertsRoundTime(t *testing.T) {
	for _, c := range allChains {
		for _, n := range withMax() {
			got, err := c.RoundAfter(c.RoundTime(n))
			if err != nil || got != n {
				t.Fatalf("%s: RoundAfter(RoundTime(%d)) = %d, %v", c.BeaconID, n, got, err)
			}
		}
	}
}

func TestRoundAfterOneSecondLaterIsNextRound(t *testing.T) {
	for _, c := range allChains {
		for _, n := range sampleRounds {
			got, err := c.RoundAfter(c.RoundTime(n).Add(time.Second))
			if err != nil || got != n+1 {
				t.Fatalf("%s: RoundAfter(RoundTime(%d)+1s) = %d, %v", c.BeaconID, n, got, err)
			}
		}
	}
}

func TestRoundAfterRoundsUpPartSecond(t *testing.T) {
	for _, c := range allChains {
		got, err := c.RoundAfter(time.Unix(c.Genesis, 1))
		if err != nil || got != 2 {
			t.Fatalf("%s: RoundAfter(genesis+1ns) = %d, %v", c.BeaconID, got, err)
		}
	}
}

func TestRoundAfterAtGenesisIsRoundOne(t *testing.T) {
	for _, c := range allChains {
		got, err := c.RoundAfter(time.Unix(c.Genesis, 0))
		if err != nil || got != 1 {
			t.Fatalf("%s: RoundAfter(genesis) = %d, %v", c.BeaconID, got, err)
		}
	}
}

func TestRoundAfterRejectsBeforeGenesis(t *testing.T) {
	for _, c := range allChains {
		if _, err := c.RoundAfter(time.Unix(c.Genesis, 0).Add(-time.Nanosecond)); err == nil {
			t.Fatalf("%s: no error before genesis", c.BeaconID)
		}
	}
}

func TestRoundAfterRejectsAboveMaxRound(t *testing.T) {
	for _, c := range allChains {
		if _, err := c.RoundAfter(c.RoundTime(MaxRound).Add(time.Nanosecond)); err == nil {
			t.Fatalf("%s: no error above MaxRound", c.BeaconID)
		}
	}
}

func TestRoundAfterRejectsFarFuture(t *testing.T) {
	for _, c := range allChains {
		if _, err := c.RoundAfter(time.Unix(1<<62, 0)); err == nil {
			t.Fatalf("%s: no error for a far future time", c.BeaconID)
		}
	}
}

func TestRoundAfterQuicknetDrawTime(t *testing.T) {
	at := time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)
	got, err := Quicknet.RoundAfter(at)
	if err != nil || got != 34448212 {
		t.Fatalf("RoundAfter(%v) = %d, %v; want 34448212", at, got, err)
	}
}
