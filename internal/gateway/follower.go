package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

const (
	// followDelay is the time after a round time at which a follower starts a fetch (FR-upstream-17).
	followDelay = 250 * time.Millisecond
	// retryDelay is the time a follower waits before it retries a fetch that returned an older round (FR-upstream-18).
	retryDelay = 500 * time.Millisecond
)

// Latest returns the highest verified round of c that a follower recorded (FR-upstream-20).
// It returns false until the first follower success for c.
func (u *Upstreams) Latest(c chain.Chain) (chain.Round, bool) {
	return u.store.latestRound(c.BeaconID)
}

// Follow runs one follower for each chain in chains and blocks until ctx ends.
// A follower spends no on-demand budget and joins no flight (FR-upstream-16).
func (u *Upstreams) Follow(ctx context.Context, chains []chain.Chain) {
	var wg sync.WaitGroup
	for _, c := range chains {
		wg.Go(func() { u.follow(ctx, c) })
	}
	wg.Wait()
}

// follow fetches the latest round of c once for each round time, until ctx ends.
// It writes one log line when the chain becomes behind and one when it recovers (FR-upstream-19).
func (u *Upstreams) follow(ctx context.Context, c chain.Chain) {
	var last uint64
	behind := false
	for {
		now := u.clock.Now()
		// The next round is always above the last one, so a fetch that ends at its own deadline cannot repeat.
		// A slow fetch skips the round times it passed, and queues nothing.
		k, err := c.RoundAfter(now.Add(-followDelay))
		if err != nil {
			u.log.Warn("follower cannot compute the next round", "beacon_id", c.BeaconID, "error", err)
			if !u.wait(ctx, time.Duration(c.Period)*time.Second) {
				return
			}
			continue
		}
		k = max(k, last+1)
		if !u.wait(ctx, c.RoundTime(k).Add(followDelay).Sub(now)) {
			return
		}
		last = k

		reached := u.followOnce(ctx, c, k)
		// A follower that is behind fetches once for each round time, with no retry.
		if !reached && !behind {
			if !u.wait(ctx, retryDelay) {
				return
			}
			reached = u.followOnce(ctx, c, k)
		}
		if ctx.Err() != nil {
			return
		}
		switch {
		case reached && behind:
			u.log.Info("follower recovered", "beacon_id", c.BeaconID, "round", k)
			behind = false
		case !reached && !behind:
			u.log.Warn("follower behind", "beacon_id", c.BeaconID, "round", k)
			behind = true
		}
	}
}

// followOnce fetches the latest round of c and records it.
// It reports whether the fetched round number is at least k.
// NotPublished counts as a failure, because a follower waits until after the round time.
func (u *Upstreams) followOnce(ctx context.Context, c chain.Chain, k uint64) bool {
	r, status := u.fetchPath(ctx, c, "latest", 0)
	if status != Found {
		return false
	}
	u.store.advanceLatest(c.BeaconID, r)
	return r.Number >= k
}

// wait blocks for d and reports false when ctx ends first.
func (u *Upstreams) wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-u.clock.After(d):
		return true
	}
}
