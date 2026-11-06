package gateway

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// testZone is the zone of every handler test.
const testZone = "beacon.example."

// dnsWait bounds each wait in the DNS test helpers, so that a hang fails with a message.
const dnsWait = 5 * time.Second

// fakeSource is a Source that a test sets. It counts the calls it receives.
type fakeSource struct {
	mu          sync.Mutex
	roundFn     func(c chain.Chain, n uint64) (chain.Round, Status)
	latestFn    func(c chain.Chain) (chain.Round, bool)
	roundCalls  int
	latestCalls int
}

func (f *fakeSource) Round(_ context.Context, c chain.Chain, n uint64) (chain.Round, Status) {
	f.mu.Lock()
	f.roundCalls++
	fn := f.roundFn
	f.mu.Unlock()
	if fn == nil {
		return chain.Round{}, Unavailable
	}
	return fn(c, n)
}

func (f *fakeSource) Latest(c chain.Chain) (chain.Round, bool) {
	f.mu.Lock()
	f.latestCalls++
	fn := f.latestFn
	f.mu.Unlock()
	if fn == nil {
		return chain.Round{}, false
	}
	return fn(c)
}

// calls returns the number of Round and Latest calls so far.
func (f *fakeSource) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.roundCalls + f.latestCalls
}

// testConfig returns a zone config with quicknet only.
func testConfig(t *testing.T) ZoneConfig {
	t.Helper()
	c, err := chain.Lookup("quicknet")
	if err != nil {
		t.Fatalf("Lookup(quicknet): %v", err)
	}
	return ZoneConfig{
		Zone:     testZone,
		Chains:   []chain.Chain{c},
		NS:       []string{"ns1.example.net.", "ns2.example.net."},
		SOARname: "hostmaster.example.net.",
		Listen:   "127.0.0.1:0",
	}
}

// startDNS serves h over UDP on an address that the OS assigns. It returns the address.
// The cleanup stops the server and waits for it to end.
func startDNS(t *testing.T, h dns.Handler) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	started := make(chan struct{})
	done := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: h, MsgAcceptFunc: acceptQuery, NotifyStartedFunc: func() { close(started) }}
	go func() {
		defer close(done)
		if err := srv.ActivateAndServe(); err != nil {
			t.Errorf("ActivateAndServe: %v", err)
		}
	}()
	select {
	case <-started:
	case <-time.After(dnsWait):
		t.Fatalf("the DNS server did not start within %s", dnsWait)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dnsWait)
		defer cancel()
		if err := srv.ShutdownContext(ctx); err != nil {
			t.Errorf("ShutdownContext: %v", err)
		}
		select {
		case <-done:
		case <-time.After(dnsWait):
			t.Errorf("the DNS server did not stop within %s", dnsWait)
		}
	})
	return pc.LocalAddr().String()
}

// ask sends one question to addr over UDP and returns the answer.
func ask(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(name, qtype)
	return exchange(t, "udp", addr, m)
}

// exchange sends m to addr over network ("udp" or "tcp") and returns the answer.
func exchange(t *testing.T, network, addr string, m *dns.Msg) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: network, Timeout: dnsWait}
	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("Exchange over %s: %v", network, err)
	}
	return r
}

// startListeners binds Listeners for cfg and runs them until the test ends.
// The cleanup ends Run and waits for it with a bound.
func startListeners(t *testing.T, cfg ZoneConfig, src Source) *Listeners {
	t.Helper()
	l, err := NewListeners(cfg, src)
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(dnsWait):
			t.Errorf("Run did not return within %s", dnsWait)
		}
	})
	return l
}
