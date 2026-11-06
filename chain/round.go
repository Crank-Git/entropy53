package chain

import (
	"errors"
	"fmt"
	"time"
)

var (
	errBeforeGenesis = errors.New("chain: time is before genesis")
	errAboveMaxRound = errors.New("chain: round number is above the maximum")
)

// RoundTime returns the time of round n, which is genesis + (n-1) * period.
// The number n must be a valid round number, from 1 to MaxRound.
func (c Chain) RoundTime(n uint64) time.Time {
	return time.Unix(c.Genesis+int64(n-1)*c.Period, 0)
}

// RoundAt returns the round that is current at t.
// It returns an error if t is before genesis or the round is above MaxRound.
// Use RoundAfter to pin a round. RoundAt returns a round that already exists.
func (c Chain) RoundAt(t time.Time) (uint64, error) {
	elapsed := t.Unix() - c.Genesis
	if elapsed < 0 {
		return 0, errBeforeGenesis
	}
	return checkRound(uint64(elapsed/c.Period) + 1)
}

// RoundAfter returns the lowest round with a round time at or after t.
// It returns an error if t is before genesis or the round is above MaxRound.
func (c Chain) RoundAfter(t time.Time) (uint64, error) {
	elapsed := t.Unix() - c.Genesis
	if elapsed < 0 {
		return 0, errBeforeGenesis
	}
	periods := uint64(elapsed / c.Period)
	// A part period, or a part second, rounds up to the next round.
	if elapsed%c.Period != 0 || t.Nanosecond() != 0 {
		periods++
	}
	return checkRound(periods + 1)
}

// checkRound rejects a round number above MaxRound.
// The arithmetic uses seconds, because time.Time.Sub saturates at about 292 years.
func checkRound(n uint64) (uint64, error) {
	if n > MaxRound {
		return 0, fmt.Errorf("%w: %d", errAboveMaxRound, n)
	}
	return n, nil
}
