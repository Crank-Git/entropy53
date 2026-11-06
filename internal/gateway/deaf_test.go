package gateway

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// deafSource is a Source that ignores the call context until release closes.
type deafSource struct {
	entered chan struct{}
	release chan struct{}
}

func (s *deafSource) Round(context.Context, chain.Chain, uint64) (chain.Round, Status) {
	s.entered <- struct{}{}
	<-s.release
	return chain.Round{}, Unavailable
}

func (s *deafSource) Latest(chain.Chain) (chain.Round, bool) { return chain.Round{}, false }

func TestRunReturnsNilWhenASourceIgnoresItsContext(t *testing.T) {
	v := vectorOf(t, "quicknet")
	src := &deafSource{entered: make(chan struct{}, 8), release: make(chan struct{})}
	t.Cleanup(func() { close(src.release) })
	var logs logBuffer
	cfg := testConfig(t)
	cfg.Log = slog.New(slog.NewTextHandler(&logs, nil))
	l, err := NewListeners(cfg, src)
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
	clk := newFakeClock(chain.Quicknet.RoundTime(v.Round).Add(time.Hour))
	l.clock = clk
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()

	askLater(t, l.Addr(), roundName(v.Round))
	waitSignal(t, src.entered, "the query reaching the source")
	cancel()
	if !waitTimersWithin(t, clk, 1) {
		t.FailNow()
	}
	clk.Advance(shutdownDrain)
	if !waitTimersWithin(t, clk, 1) {
		t.FailNow()
	}
	clk.Advance(shutdownClose)

	waitRun(t, done)
	if n := strings.Count(logs.String(), "did not finish within the close limit"); n != 1 {
		t.Errorf("%d log lines about unfinished servers, want 1: %q", n, logs.String())
	}
	if !strings.Contains(logs.String(), "udp") {
		t.Errorf("log %q does not name the unfinished server", logs.String())
	}
}
