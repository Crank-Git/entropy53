package gateway

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock that moves only when a test calls Advance.
// Later tests in this package reuse it.
type fakeClock struct {
	mu     sync.Mutex
	cond   *sync.Cond
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
}

// newFakeClock returns a fakeClock that reads start.
func newFakeClock(start time.Time) *fakeClock {
	c := &fakeClock{now: start}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{deadline: c.now.Add(d), ch: ch})
	c.cond.Broadcast()
	return ch
}

// Advance moves the clock forward by d and fires each timer that is due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	pending := c.timers[:0]
	for _, t := range c.timers {
		if t.deadline.After(c.now) {
			pending = append(pending, t)
			continue
		}
		t.ch <- c.now
	}
	c.timers = pending
}

// waitTimers blocks until at least n timers are pending.
func (c *fakeClock) waitTimers(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.timers) < n {
		c.cond.Wait()
	}
}

// nextDeadline returns the earliest deadline of a pending timer, and false when none is pending.
func (c *fakeClock) nextDeadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var next time.Time
	for i, t := range c.timers {
		if i == 0 || t.deadline.Before(next) {
			next = t.deadline
		}
	}
	return next, len(c.timers) > 0
}

func TestFakeClockFiresAtDeadline(t *testing.T) {
	start := time.Unix(1000, 0)
	c := newFakeClock(start)
	ch := c.After(5 * time.Second)
	c.waitTimers(1)

	if next, ok := c.nextDeadline(); !ok || !next.Equal(start.Add(5*time.Second)) {
		t.Fatalf("nextDeadline = %v, %v, want %v, true", next, ok, start.Add(5*time.Second))
	}
	c.Advance(4 * time.Second)
	select {
	case <-ch:
		t.Fatal("timer fired before its deadline")
	default:
	}
	c.Advance(time.Second)
	select {
	case got := <-ch:
		if !got.Equal(start.Add(5 * time.Second)) {
			t.Errorf("fired at %v, want %v", got, start.Add(5*time.Second))
		}
	default:
		t.Fatal("timer did not fire at its deadline")
	}
	if _, ok := c.nextDeadline(); ok {
		t.Error("a fired timer is still pending")
	}
}
