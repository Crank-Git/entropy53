package gateway

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// onDemandSetup returns an Upstreams on a fake clock one hour after the round time of the quicknet vector.
// Every round up to the vector round is therefore published.
func onDemandSetup(t *testing.T, rate int, urls ...string) (*Upstreams, *fakeClock, vector) {
	t.Helper()
	v := vectorOf(t, "quicknet")
	clk := newFakeClock(chain.Quicknet.RoundTime(v.Round).Add(time.Hour))
	u := NewUpstreams(Config{Upstreams: urls, Rate: rate})
	u.clock = clk
	return u, clk, v
}

// blockUntil returns a handler that waits for release to close, and a function that closes it once.
func blockUntil(t *testing.T, v vector) (http.HandlerFunc, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	return func(w http.ResponseWriter, r *http.Request) {
		<-release
		serveRound(v)(w, r)
	}, closeRelease
}

type result struct {
	round chain.Round
	st    Status
}

func asyncRound(ctx context.Context, u *Upstreams, n uint64) <-chan result {
	out := make(chan result, 1)
	go func() {
		r, st := u.Round(ctx, chain.Quicknet, n)
		out <- result{r, st}
	}()
	return out
}

func TestConcurrentRoundsSendOneRequest(t *testing.T) {
	v := vectorOf(t, "quicknet")
	var clk *fakeClock
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		clk.waitTimers(100)
		serveRound(v)(w, r)
	})
	var u *Upstreams
	u, clk, _ = onDemandSetup(t, 1, up.url())

	var wg sync.WaitGroup
	statuses := make([]Status, 100)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, statuses[i] = u.Round(context.Background(), chain.Quicknet, v.Round)
		}()
	}
	wg.Wait()

	for i, st := range statuses {
		if st != Found {
			t.Fatalf("caller %d status = %v, want Found", i, st)
		}
	}
	if got := up.hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

func TestRateOneAllowsAtMostTwoRequestsInOneSecond(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, clk, _ := onDemandSetup(t, 1, up.url())

	for n := uint64(1); n <= 10; n++ {
		u.Round(context.Background(), chain.Quicknet, n)
		clk.Advance(100 * time.Millisecond)
	}

	if got := up.hits.Load(); got < 1 || got > 2 {
		t.Errorf("upstream requests = %d, want 1 or 2", got)
	}
}

func TestRateZeroAllowsNoFetch(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, _, _ := onDemandSetup(t, 0, up.url())

	_, st := u.Round(context.Background(), chain.Quicknet, 5)

	if st != Unavailable || up.hits.Load() != 0 {
		t.Errorf("status = %v, requests = %d, want Unavailable, 0", st, up.hits.Load())
	}
}

func TestBudgetRefillsWithTime(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, clk, _ := onDemandSetup(t, 1, up.url())

	u.Round(context.Background(), chain.Quicknet, 1)
	u.Round(context.Background(), chain.Quicknet, 2)
	clk.Advance(time.Second)
	u.Round(context.Background(), chain.Quicknet, 3)

	if got := up.hits.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2", got)
	}
}

func TestWaitEndsAtTimeoutTimesUpstreamCount(t *testing.T) {
	v := vectorOf(t, "quicknet")
	handler, _ := blockUntil(t, v)
	a := newFakeUpstream(t, handler)
	b := newFakeUpstream(t, handler)
	u, clk, _ := onDemandSetup(t, 1, a.url(), b.url())

	done := asyncRound(context.Background(), u, v.Round)
	clk.waitTimers(1)

	want := clk.Now().Add(2 * requestTimeout)
	if got, ok := clk.nextDeadline(); !ok || !got.Equal(want) {
		t.Fatalf("wait deadline = %v, %v, want %v", got, ok, want)
	}
	clk.Advance(2*requestTimeout - time.Nanosecond)
	select {
	case <-done:
		t.Fatal("wait ended before the timeout")
	default:
	}
	clk.Advance(time.Nanosecond)
	if res := <-done; res.st != Unavailable {
		t.Errorf("status = %v, want Unavailable", res.st)
	}
}

func TestWaitEndsAtCallerDeadline(t *testing.T) {
	v := vectorOf(t, "quicknet")
	handler, _ := blockUntil(t, v)
	up := newFakeUpstream(t, handler)
	u, clk, _ := onDemandSetup(t, 1, up.url())
	ctx, cancel := context.WithCancel(context.Background())

	done := asyncRound(ctx, u, v.Round)
	clk.waitTimers(1)
	cancel()

	if res := <-done; res.st != Unavailable {
		t.Errorf("status = %v, want Unavailable", res.st)
	}
}

func TestCanceledFirstCallerDoesNotEndOtherWait(t *testing.T) {
	v := vectorOf(t, "quicknet")
	handler, release := blockUntil(t, v)
	up := newFakeUpstream(t, handler)
	u, clk, _ := onDemandSetup(t, 1, up.url())
	ctx, cancel := context.WithCancel(context.Background())

	first := asyncRound(ctx, u, v.Round)
	clk.waitTimers(1)
	second := asyncRound(context.Background(), u, v.Round)
	clk.waitTimers(2)
	cancel()
	if res := <-first; res.st != Unavailable {
		t.Errorf("first status = %v, want Unavailable", res.st)
	}
	release()

	if res := <-second; res.st != Found {
		t.Errorf("second status = %v, want Found", res.st)
	}
	if got := up.hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

func TestFlightEndsAfterFetch(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u, _, _ := onDemandSetup(t, 1, up.url())

	if _, st := u.Round(context.Background(), chain.Quicknet, v.Round); st != Found {
		t.Fatalf("status = %v, want Found", st)
	}

	u.flightMu.Lock()
	open := len(u.flights)
	u.flightMu.Unlock()
	if open != 0 {
		t.Errorf("open flights = %d, want 0", open)
	}
	if _, st := u.Round(context.Background(), chain.Quicknet, v.Round); st != Found || up.hits.Load() != 1 {
		t.Errorf("second call status = %v, requests = %d, want Found, 1", st, up.hits.Load())
	}
}

func TestAllUpstreamsFailReturnsUnavailable(t *testing.T) {
	a := newFakeUpstream(t, fail500)
	b := newFakeUpstream(t, fail500)
	u, _, v := onDemandSetup(t, 1, a.url(), b.url())

	if _, st := u.Round(context.Background(), chain.Quicknet, v.Round); st != Unavailable {
		t.Errorf("status = %v, want Unavailable", st)
	}
}

func TestStoreServesRoundWhenAllUpstreamsFail(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, _, v := onDemandSetup(t, 1, up.url())
	u.store.put(chain.Quicknet.BeaconID, chain.Round{Number: v.Round})

	got, st := u.Round(context.Background(), chain.Quicknet, v.Round)

	if st != Found || got.Number != v.Round || up.hits.Load() != 0 {
		t.Errorf("status = %v, round = %d, requests = %d, want Found, %d, 0", st, got.Number, up.hits.Load(), v.Round)
	}
}

func TestFloodOfOldRoundsHoldsBudget(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, _, _ := onDemandSetup(t, 5, up.url())

	for n := uint64(1); n <= 50; n++ {
		if _, st := u.Round(context.Background(), chain.Quicknet, n); st != Unavailable {
			t.Fatalf("round %d status = %v, want Unavailable", n, st)
		}
	}

	if got := up.hits.Load(); got != 5 {
		t.Errorf("upstream requests = %d, want 5", got)
	}
}

func TestStoreHitSpendsNoBudget(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, _, v := onDemandSetup(t, 1, up.url())
	u.store.put(chain.Quicknet.BeaconID, chain.Round{Number: v.Round})

	for range 5 {
		u.Round(context.Background(), chain.Quicknet, v.Round)
	}
	u.Round(context.Background(), chain.Quicknet, 1)

	if got := up.hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

func TestJoinSpendsNoBudget(t *testing.T) {
	v := vectorOf(t, "quicknet")
	handler, release := blockUntil(t, v)
	up := newFakeUpstream(t, handler)
	u, clk, _ := onDemandSetup(t, 1, up.url())

	results := []<-chan result{asyncRound(context.Background(), u, v.Round)}
	clk.waitTimers(1)
	for i := 2; i <= 3; i++ {
		results = append(results, asyncRound(context.Background(), u, v.Round))
		clk.waitTimers(i)
	}
	release()

	for i, ch := range results {
		if res := <-ch; res.st != Found {
			t.Errorf("caller %d status = %v, want Found", i, res.st)
		}
	}
	if got := up.hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

func TestRoundAboveCurrentRoundSendsNoRequest(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, clk, _ := onDemandSetup(t, 1, up.url())
	current, err := chain.Quicknet.RoundAt(clk.Now())
	if err != nil {
		t.Fatal(err)
	}

	_, st := u.Round(context.Background(), chain.Quicknet, current+1)

	if st != NotPublished || up.hits.Load() != 0 {
		t.Errorf("status = %v, requests = %d, want NotPublished, 0", st, up.hits.Load())
	}
}

func TestClockBeforeGenesisReturnsUnavailable(t *testing.T) {
	up := newFakeUpstream(t, fail500)
	u, _, _ := onDemandSetup(t, 1, up.url())
	u.clock = newFakeClock(time.Unix(chain.Quicknet.Genesis-1, 0))

	_, st := u.Round(context.Background(), chain.Quicknet, 1)

	if st != Unavailable || up.hits.Load() != 0 {
		t.Errorf("status = %v, requests = %d, want Unavailable, 0", st, up.hits.Load())
	}
}
