package gateway

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// ctxSource is a Source that blocks each Round call until release closes or the call context ends.
// An ended context gives Unavailable, as Upstreams does.
type ctxSource struct {
	entered chan struct{}
	release chan struct{}
	round   chain.Round
}

func newCtxSource(r chain.Round) *ctxSource {
	return &ctxSource{entered: make(chan struct{}, 8), release: make(chan struct{}), round: r}
}

func (s *ctxSource) Round(ctx context.Context, _ chain.Chain, _ uint64) (chain.Round, Status) {
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return s.round, Found
	case <-ctx.Done():
		return chain.Round{}, Unavailable
	}
}

func (s *ctxSource) Latest(chain.Chain) (chain.Round, bool) { return chain.Round{}, false }

// runUnderClock binds Listeners for src, gives them clk and starts Run.
// The cleanup ends Run. A test reads the Run result from the returned channel.
func runUnderClock(t *testing.T, src Source, clk clock) (*Listeners, context.CancelFunc, chan error) {
	t.Helper()
	l, err := NewListeners(testConfig(t), src)
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
	l.clock = clk
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	return l, cancel, done
}

// askLater sends one TXT question in a goroutine and returns the channel of its answer.
func askLater(t *testing.T, addr, name string) chan *dns.Msg {
	t.Helper()
	got := make(chan *dns.Msg, 1)
	go func() {
		c := &dns.Client{Net: "udp", Timeout: dnsWait}
		// A failed exchange sends nil, and waitAnswer reports it in the test goroutine.
		r, _, _ := c.Exchange(new(dns.Msg).SetQuestion(name, dns.TypeTXT), addr)
		got <- r
	}()
	return got
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(dnsWait):
		t.Fatalf("%s did not happen within %s", what, dnsWait)
	}
}

func waitAnswer(t *testing.T, got <-chan *dns.Msg) *dns.Msg {
	t.Helper()
	select {
	case r := <-got:
		if r == nil {
			t.Fatal("the query got no answer")
		}
		return r
	case <-time.After(dnsWait):
		t.Fatalf("the query got no answer within %s", dnsWait)
		return nil
	}
}

func waitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(dnsWait):
		t.Fatalf("Run did not return within %s", dnsWait)
	}
}

func runIsPending(done <-chan error) bool {
	return len(done) == 0
}

func TestRunAnswersInFlightQueryBeforeItReturns(t *testing.T) {
	v := vectorOf(t, "quicknet")
	src := newCtxSource(roundOf(t, v))
	clk := newFakeClock(chain.Quicknet.RoundTime(v.Round).Add(time.Hour))
	l, cancel, done := runUnderClock(t, src, clk)

	got := askLater(t, l.Addr(), roundName(v.Round))
	waitSignal(t, src.entered, "the query reaching the source")
	cancel()
	if !waitTimersWithin(t, clk, 1) {
		t.FailNow()
	}
	if !runIsPending(done) {
		t.Fatal("Run returned while a query was in flight")
	}
	close(src.release)

	r := waitAnswer(t, got)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Errorf("rcode = %s, answers = %d, want NOERROR and 1", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	waitRun(t, done)
}

func TestRunEndsOnDemandWaitAtDrainLimit(t *testing.T) {
	v := vectorOf(t, "quicknet")
	src := newCtxSource(roundOf(t, v))
	clk := newFakeClock(chain.Quicknet.RoundTime(v.Round).Add(time.Hour))
	l, cancel, done := runUnderClock(t, src, clk)

	got := askLater(t, l.Addr(), roundName(v.Round))
	waitSignal(t, src.entered, "the query reaching the source")
	cancel()
	if !waitTimersWithin(t, clk, 1) {
		t.FailNow()
	}
	clk.Advance(shutdownDrain)

	if r := waitAnswer(t, got); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL", dns.RcodeToString[r.Rcode])
	}
	waitRun(t, done)
}

func TestIdleTCPConnectionDoesNotDelayShutdown(t *testing.T) {
	l, cancel, done := runUnderClock(t, &fakeSource{}, newFakeClock(time.Now()))
	conn, err := net.DialTimeout("tcp", l.Addr(), dnsWait)
	if err != nil {
		t.Fatalf("DialTimeout: %v", err)
	}
	defer func() { _ = conn.Close() }()
	dc := &dns.Conn{Conn: conn}
	if err := dc.WriteMsg(new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}
	if _, err := dc.ReadMsg(); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	cancel()
	waitRun(t, done)
}

func TestRunReleasesBothPorts(t *testing.T) {
	l, cancel, done := runUnderClock(t, &fakeSource{}, newFakeClock(time.Now()))
	addr := l.Addr()
	if r := exchange(t, "tcp", addr, new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)); r.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[r.Rcode])
	}
	cancel()
	waitRun(t, done)

	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("rebind TCP %s: %v", addr, err)
	}
	_ = tcp.Close()
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("rebind UDP %s: %v", addr, err)
	}
	_ = udp.Close()
}

func TestShutdownLimitsSumBelow5Seconds(t *testing.T) {
	if sum := shutdownDrain + shutdownClose; sum >= 5*time.Second {
		t.Fatalf("shutdownDrain + shutdownClose = %s, want less than 5s", sum)
	}
}

func TestRunWithFourStalledUpstreamsEndsWithin5Seconds(t *testing.T) {
	v := vectorOf(t, "quicknet")
	entered := make(chan struct{}, 16)
	var urls []string
	for range 4 {
		block, release := blockUntil(t, v)
		up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			block(w, r)
		})
		t.Cleanup(release)
		urls = append(urls, up.url())
	}
	u := NewUpstreams(Config{Upstreams: urls, Rate: 20})
	l, cancel, done := runUnderClock(t, u, realClock{})

	got := askLater(t, l.Addr(), roundName(v.Round))
	waitSignal(t, entered, "the first fetch reaching an upstream")
	start := time.Now()
	cancel()

	r := waitAnswer(t, got)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5*time.Second - time.Since(start)):
		t.Fatal("Run did not return within 5 seconds of the cancel")
	}
	if r.Rcode != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL", dns.RcodeToString[r.Rcode])
	}
}

func TestChainOutsideChainsFlagReturnsNXDOMAIN(t *testing.T) {
	l := startListeners(t, testConfig(t), &fakeSource{})
	r := ask(t, l.Addr(), "round.1000.default."+testZone, dns.TypeTXT)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("rcode = %s, want NXDOMAIN", dns.RcodeToString[r.Rcode])
	}
}
