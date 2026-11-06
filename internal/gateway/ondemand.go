package gateway

import (
	"context"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// flight is one on-demand fetch that callers share (FR-upstream-12).
// The fields round and status are valid after done closes.
type flight struct {
	done   chan struct{}
	round  chain.Round
	status Status
}

// budget is a token bucket that limits on-demand fetches to rate per second (FR-upstream-13).
// The bucket holds at most rate tokens and starts full. The caller holds a lock.
type budget struct {
	rate   float64
	tokens float64
	last   time.Time
}

// take spends one token at time now and reports whether a token was available.
func (b *budget) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = b.rate
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.rate, b.tokens+elapsed.Seconds()*b.rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// waitLimit is the longest time a caller waits for a fetch: the request timeout of each upstream in turn.
func (u *Upstreams) waitLimit() time.Duration {
	return requestTimeout * time.Duration(len(u.upstreams))
}

// Round returns round n of chain c from the store, or fetches it from the upstreams.
// A round above the current round returns NotPublished without a request.
// Callers for the same chain and round share one fetch.
// A caller returns Unavailable when its context ends, when the wait limit passes, or when the budget is empty.
func (u *Upstreams) Round(ctx context.Context, c chain.Chain, n uint64) (chain.Round, Status) {
	if r, ok := u.store.get(c.BeaconID, n); ok {
		return r, Found
	}
	if n == 0 {
		return chain.Round{}, Unavailable
	}
	current, err := c.RoundAt(u.clock.Now())
	if err != nil {
		return chain.Round{}, Unavailable
	}
	if n > current {
		return chain.Round{}, NotPublished
	}
	f, r, status := u.join(ctx, c, n)
	if f == nil {
		return r, status
	}
	select {
	case <-f.done:
		return f.round, f.status
	case <-ctx.Done():
	case <-u.clock.After(u.waitLimit()):
	}
	return chain.Round{}, Unavailable
}

// join returns the flight for round n, and starts it when none exists and the budget allows.
// It returns a nil flight with the final answer when the store gained the round or the budget is empty.
func (u *Upstreams) join(ctx context.Context, c chain.Chain, n uint64) (*flight, chain.Round, Status) {
	key := storeKey{c.BeaconID, n}
	u.flightMu.Lock()
	defer u.flightMu.Unlock()
	// A fetch can end between the first store check and this lock.
	if r, ok := u.store.get(c.BeaconID, n); ok {
		return nil, r, Found
	}
	if f, ok := u.flights[key]; ok {
		return f, chain.Round{}, Unavailable
	}
	if !u.budget.take(u.clock.Now()) {
		return nil, chain.Round{}, Unavailable
	}
	f := &flight{done: make(chan struct{})}
	u.flights[key] = f
	// The fetch must outlive the first caller, because other callers wait for it.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), u.waitLimit())
	go func() {
		defer cancel()
		r, status := u.fetchRound(fetchCtx, c, n)
		u.flightMu.Lock()
		delete(u.flights, key)
		u.flightMu.Unlock()
		f.round, f.status = r, status
		close(f.done)
	}()
	return f, chain.Round{}, Unavailable
}
