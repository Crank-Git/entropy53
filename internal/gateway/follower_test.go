package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

var _ Source = (*Upstreams)(nil)

// followRig runs Follow on a fake clock and stops it when the test ends.
type followRig struct {
	u    *Upstreams
	clk  *fakeClock
	logs *logBuffer
	done chan struct{}
}

// startFollow starts Follow for chains with a clock that reads start.
// The rig has no Rate, so a request that arrives at the upstream cannot come from the on-demand budget.
func startFollow(t *testing.T, up *fakeUpstream, start time.Time, chains ...chain.Chain) *followRig {
	t.Helper()
	logs := &logBuffer{}
	u := NewUpstreams(Config{Upstreams: []string{up.url()}, Log: slog.New(slog.NewTextHandler(logs, nil))})
	rig := &followRig{u: u, clk: newFakeClock(start), logs: logs, done: make(chan struct{})}
	u.clock = rig.clk
	return rig.run(t, chains)
}

func (r *followRig) run(t *testing.T, chains []chain.Chain) *followRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(r.done)
		r.u.Follow(ctx, chains)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	return r
}

// step waits until the follower arms a timer, then moves the clock to the earliest deadline.
func (r *followRig) step() {
	r.clk.waitTimers(1)
	next, _ := r.clk.nextDeadline()
	r.clk.Advance(next.Sub(r.clk.Now()))
}

// quicknetStart returns a start time one second before the follower wakes for round k.
func quicknetStart(k uint64) time.Time {
	return chain.Quicknet.RoundTime(k).Add(-time.Second)
}

// switchable serves the quicknet vector while ok is true and fails otherwise.
func switchable(v vector, ok *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok.Load() {
			serveRound(v)(w, r)
			return
		}
		fail500(w, r)
	}
}

func count(logs *logBuffer, line string) int { return strings.Count(logs.String(), line) }

func TestLatestDoesNotFallWhenUpstreamReturnsOlderRound(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})
	u.store.advanceLatest(chain.Quicknet.BeaconID, chain.Round{Number: 2000})
	rig := &followRig{u: u, clk: newFakeClock(quicknetStart(995)), logs: &logBuffer{}, done: make(chan struct{})}
	u.clock = rig.clk
	rig.run(t, []chain.Chain{chain.Quicknet})

	rig.step()
	rig.clk.waitTimers(1)

	got, ok := u.Latest(chain.Quicknet)
	if !ok || got.Number != 2000 {
		t.Errorf("Latest = %d, %v, want 2000, true", got.Number, ok)
	}
	if up.hits.Load() != 1 {
		t.Errorf("requests = %d, want 1", up.hits.Load())
	}
}

func TestFollowerSendsRequest250MillisecondsAfterRoundTime(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	rig.clk.waitTimers(1)
	next, _ := rig.clk.nextDeadline()
	if want := chain.Quicknet.RoundTime(995).Add(250 * time.Millisecond); !next.Equal(want) {
		t.Fatalf("first deadline = %v, want %v", next, want)
	}
	if up.hits.Load() != 0 {
		t.Fatalf("requests before the deadline = %d, want 0", up.hits.Load())
	}
	rig.step()
	rig.clk.waitTimers(1)

	if up.hits.Load() != 1 {
		t.Errorf("requests after the deadline = %d, want 1", up.hits.Load())
	}
}

func TestFollowerRequestsPublicLatest(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	rig.step()
	rig.clk.waitTimers(1)

	want := "/" + chain.Quicknet.ChainHash + "/public/latest"
	if paths := up.requestPaths(); len(paths) != 1 || paths[0] != want {
		t.Errorf("request paths = %v, want [%s]", paths, want)
	}
}

func TestFollowerSendsRequestsWithZeroRate(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	rig.step()
	rig.clk.waitTimers(1)

	if got, ok := rig.u.Latest(chain.Quicknet); !ok || got.Number != 1000 {
		t.Errorf("Latest = %d, %v, want 1000, true", got.Number, ok)
	}
}

func TestFollowRunsForEachChain(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet, chain.Default)

	rig.clk.waitTimers(2)
	rig.clk.Advance(time.Hour)
	rig.clk.waitTimers(2)

	paths := strings.Join(up.requestPaths(), " ")
	for _, c := range []chain.Chain{chain.Quicknet, chain.Default} {
		if !strings.Contains(paths, c.ChainHash) {
			t.Errorf("no request for chain %s in %s", c.BeaconID, paths)
		}
	}
}

func TestFollowerRetriesOnceAfter500MillisecondsForAnOlderRound(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	rig := startFollow(t, up, quicknetStart(1001), chain.Quicknet)

	rig.step()
	rig.clk.waitTimers(1)
	retryAt, _ := rig.clk.nextDeadline()
	if want := rig.clk.Now().Add(500 * time.Millisecond); !retryAt.Equal(want) {
		t.Fatalf("retry deadline = %v, want %v", retryAt, want)
	}
	if up.hits.Load() != 1 {
		t.Fatalf("requests before the retry = %d, want 1", up.hits.Load())
	}
	rig.step()
	rig.clk.waitTimers(1)

	if up.hits.Load() != 2 {
		t.Errorf("requests after the retry = %d, want 2", up.hits.Load())
	}
	if got := count(rig.logs, "follower behind"); got != 1 {
		t.Errorf("behind lines = %d, want 1", got)
	}
}

func TestFollowerRetriesOnceAfter500MillisecondsAfterAFailure(t *testing.T) {
	var ok atomic.Bool
	up := newFakeUpstream(t, switchable(vectorOf(t, "quicknet"), &ok))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	rig.step()
	rig.clk.waitTimers(1)
	ok.Store(true)
	rig.step()
	rig.clk.waitTimers(1)

	if up.hits.Load() != 2 {
		t.Errorf("requests = %d, want 2", up.hits.Load())
	}
	if got, found := rig.u.Latest(chain.Quicknet); !found || got.Number != 1000 {
		t.Errorf("Latest = %d, %v, want 1000, true", got.Number, found)
	}
	if got := count(rig.logs, "follower behind"); got != 0 {
		t.Errorf("behind lines = %d, want 0", got)
	}
}

func TestFollowerWritesOneBehindLineAcrossFiveFailedRounds(t *testing.T) {
	var ok atomic.Bool
	up := newFakeUpstream(t, switchable(vectorOf(t, "quicknet"), &ok))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	// Round 995 sends a request and a retry. Rounds 996 to 999 send one request each.
	for range 6 {
		rig.step()
	}
	rig.clk.waitTimers(1)

	if up.hits.Load() != 6 {
		t.Errorf("requests = %d, want 6", up.hits.Load())
	}
	if got := count(rig.logs, "follower behind"); got != 1 {
		t.Errorf("behind lines = %d, want 1", got)
	}
	if got := count(rig.logs, "follower recovered"); got != 0 {
		t.Errorf("recovered lines before recovery = %d, want 0", got)
	}

	ok.Store(true)
	rig.step()
	rig.clk.waitTimers(1)

	if got := count(rig.logs, "follower recovered"); got != 1 {
		t.Errorf("recovered lines = %d, want 1", got)
	}
	if got := count(rig.logs, "follower behind"); got != 1 {
		t.Errorf("behind lines after recovery = %d, want 1", got)
	}
}

func TestBadPublicKeyWritesRepeatedLinesAndNoCrash(t *testing.T) {
	v := vectorOf(t, "quicknet")
	v.Signature = v.Signature[:len(v.Signature)-1] + "0"
	up := newFakeUpstream(t, serveRound(v))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	// Round 995 sends a request and a retry. Round 996 sends one request.
	for range 3 {
		rig.step()
	}
	rig.clk.waitTimers(1)

	if got := count(rig.logs, "round discarded"); got != 3 {
		t.Errorf("round discarded lines = %d, want 3", got)
	}
	if _, ok := rig.u.Latest(chain.Quicknet); ok {
		t.Error("Latest returned a round that did not verify")
	}
}

func TestHaltedChainKeepsLatestRound(t *testing.T) {
	var ok atomic.Bool
	ok.Store(true)
	up := newFakeUpstream(t, switchable(vectorOf(t, "quicknet"), &ok))
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	rig.step()
	rig.clk.waitTimers(1)
	ok.Store(false)
	// Round 996 sends a request and a retry.
	rig.step()
	rig.step()
	rig.clk.waitTimers(1)

	if got := count(rig.logs, "follower behind"); got != 1 {
		t.Errorf("behind lines = %d, want 1", got)
	}
	if got, found := rig.u.Latest(chain.Quicknet); !found || got.Number != 1000 {
		t.Errorf("Latest = %d, %v, want 1000, true", got.Number, found)
	}
}

func TestLatestReturnsFalseBeforeFirstFollowerSuccess(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	rig := startFollow(t, up, quicknetStart(995), chain.Quicknet)

	if _, ok := rig.u.Latest(chain.Quicknet); ok {
		t.Error("Latest = true before the follower started a request")
	}
	rig.step()
	rig.step()
	rig.clk.waitTimers(1)

	if _, ok := rig.u.Latest(chain.Quicknet); ok {
		t.Error("Latest = true after only failed requests")
	}
}

func TestLatestSurvivesEviction(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})
	u.store = newStore(1)
	rig := &followRig{u: u, clk: newFakeClock(quicknetStart(995)), logs: &logBuffer{}, done: make(chan struct{})}
	u.clock = rig.clk
	rig.run(t, []chain.Chain{chain.Quicknet})

	rig.step()
	rig.clk.waitTimers(1)
	u.store.put("other", chain.Round{Number: 1})

	if _, ok := u.store.get(chain.Quicknet.BeaconID, 1000); ok {
		t.Fatal("round 1000 is still in a store of capacity 1 after another put")
	}
	if got, ok := u.Latest(chain.Quicknet); !ok || got.Number != 1000 {
		t.Errorf("Latest = %d, %v, want 1000, true", got.Number, ok)
	}
}

func TestCanceledContextEndsFollow(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	logs := &logBuffer{}
	u := NewUpstreams(Config{Upstreams: []string{up.url()}, Log: slog.New(slog.NewTextHandler(logs, nil))})
	clk := newFakeClock(quicknetStart(995))
	u.clock = clk
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		u.Follow(ctx, []chain.Chain{chain.Quicknet, chain.Default})
	}()

	clk.waitTimers(2)
	cancel()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow did not return after the context ended")
	}
}

func TestOlderRoundMovesPreferredToNextUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, serveRound(v))
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})
	hash := chain.Quicknet.ChainHash

	if u.followOnce(context.Background(), chain.Quicknet, v.Round+1) {
		t.Fatal("followOnce reached a round above the answer")
	}
	if got := u.start(hash); got != 1 {
		t.Errorf("preferred after the first answer = %d, want 1", got)
	}
	u.followOnce(context.Background(), chain.Quicknet, v.Round+1)
	if got := u.start(hash); got != 0 {
		t.Errorf("preferred after the second answer = %d, want 0", got)
	}
	if got, ok := u.Latest(chain.Quicknet); !ok || got.Number != v.Round {
		t.Errorf("Latest = %d, %v, want %d, true", got.Number, ok, v.Round)
	}
}

// quicknetRound1001Signature is the signature of real round 1001 of quicknet.
// A test needs a second real round, because the vector file holds only round 1000.
const quicknetRound1001Signature = "b33bf3667cbd5a82de3a24b4e0e9fe5513cc1a0e840368c6e31f5fcfa79bea03f73896b25883abf2853d10337fb8fa41"

func TestFollowerRecoversFromUpstreamStuckOnOlderRound(t *testing.T) {
	stale := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	healthy := newFakeUpstream(t, serveRound(vector{BeaconID: "quicknet", Round: 1001, Signature: quicknetRound1001Signature}))
	logs := &logBuffer{}
	u := NewUpstreams(Config{Upstreams: []string{stale.url(), healthy.url()}, Log: slog.New(slog.NewTextHandler(logs, nil))})
	clk := newFakeClock(quicknetStart(1001))
	u.clock = clk
	rig := &followRig{u: u, clk: clk, logs: logs, done: make(chan struct{})}
	rig.run(t, []chain.Chain{chain.Quicknet})

	rig.step()
	rig.clk.waitTimers(1)
	rig.step()
	rig.clk.waitTimers(1)

	if got, ok := u.Latest(chain.Quicknet); !ok || got.Number != 1001 {
		t.Errorf("Latest = %d, %v, want 1001, true", got.Number, ok)
	}
	if got := count(logs, "follower behind"); got != 0 {
		t.Errorf("behind lines = %d, want 0", got)
	}
	if stale.hits.Load() != 1 || healthy.hits.Load() != 1 {
		t.Errorf("hits = %d, %d, want 1, 1", stale.hits.Load(), healthy.hits.Load())
	}
	if got := u.start(chain.Quicknet.ChainHash); got != 1 {
		t.Errorf("preferred = %d, want 1", got)
	}
}
