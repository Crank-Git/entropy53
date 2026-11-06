package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// testClient returns a Client for the quicknet chain that queries resolver.
func testClient(t *testing.T, resolver string, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{Zone: testZone, Chain: chain.Quicknet, Resolver: resolver}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// queryWithin runs c.query and fails the test when the call does not return within testWait.
func queryWithin(t *testing.T, ctx context.Context, c *Client, name string) ([]dns.RR, error) {
	t.Helper()
	type result struct {
		rrs []dns.RR
		err error
	}
	ch := make(chan result, 1)
	go func() {
		rrs, err := c.query(ctx, name)
		ch <- result{rrs, err}
	}()
	select {
	case r := <-ch:
		return r.rrs, r.err
	case <-time.After(testWait):
		t.Fatalf("query did not return within %s", testWait)
		return nil, nil
	}
}

// roundWithin runs c.Round and fails the test when the call does not return within testWait.
func roundWithin(t *testing.T, ctx context.Context, c *Client, n uint64) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() {
		_, err := c.Round(ctx, n)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(testWait):
		t.Fatalf("Round did not return within %s", testWait)
		return nil
	}
}

func TestRoundReturnsErrTransportForServfail(t *testing.T) {
	r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeServerFailure) })
	c := testClient(t, r.addr, nil)
	err := roundWithin(t, context.Background(), c, 1000)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Round error = %v, want ErrTransport", err)
	}
	if got := len(r.queries()); got != 1 {
		t.Fatalf("resolver received %d queries, want 1", got)
	}
}

func TestRoundReturnsErrTransportForRefused(t *testing.T) {
	r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeRefused) })
	c := testClient(t, r.addr, nil)
	err := roundWithin(t, context.Background(), c, 1000)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Round error = %v, want ErrTransport", err)
	}
}

func TestQueryReturnsNoRecordsForNegativeAnswers(t *testing.T) {
	for name, rcode := range map[string]int{"NODATA": dns.RcodeSuccess, "NXDOMAIN": dns.RcodeNameError} {
		t.Run(name, func(t *testing.T) {
			r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, rcode) })
			c := testClient(t, r.addr, nil)
			rrs, err := queryWithin(t, context.Background(), c, "round.1.quicknet.beacon.example.")
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(rrs) != 0 {
				t.Fatalf("query returned %d records, want 0", len(rrs))
			}
		})
	}
}

func TestQueryRetriesOverTCPAfterTC(t *testing.T) {
	want := vectorTXT(t, "default")
	r := startResolver(t, func(network string, req *dns.Msg) *dns.Msg {
		if network == "udp" {
			resp := rcodeReply(req, dns.RcodeSuccess)
			resp.Truncated = true
			return resp
		}
		return txtReply(req, want)
	})
	c := testClient(t, r.addr, nil)
	rrs, err := queryWithin(t, context.Background(), c, "round.1000.default.beacon.example.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if u, tc := r.count("udp"), r.count("tcp"); u != 1 || tc != 1 {
		t.Fatalf("queries: udp %d, tcp %d, want 1 and 1", u, tc)
	}
	if len(rrs) != 1 {
		t.Fatalf("query returned %d records, want 1", len(rrs))
	}
	txt, ok := rrs[0].(*dns.TXT)
	if !ok {
		t.Fatalf("record is %T, want *dns.TXT", rrs[0])
	}
	if !slices.Equal(txt.Txt, want) {
		t.Fatalf("txt = %q, want %q", txt.Txt, want)
	}
}

func TestRoundContactsOnlyConfiguredResolver(t *testing.T) {
	decoy := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeSuccess) })
	want := vectorTXT(t, "quicknet")
	r := startResolver(t, func(network string, req *dns.Msg) *dns.Msg {
		if network == "udp" {
			resp := rcodeReply(req, dns.RcodeSuccess)
			resp.Truncated = true
			return resp
		}
		return txtReply(req, want)
	})
	c := testClient(t, r.addr, nil)
	if _, err := queryWithin(t, context.Background(), c, "round.1000.quicknet.beacon.example."); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := len(r.queries()); got != 2 {
		t.Fatalf("configured resolver received %d queries, want 2", got)
	}
	if got := len(decoy.queries()); got != 0 {
		t.Fatalf("decoy resolver received %d queries, want 0", got)
	}
}

func TestRoundSendsRoundNameWithEDNS1232(t *testing.T) {
	for _, zone := range []string{"beacon.example.", "beacon.example"} {
		t.Run(zone, func(t *testing.T) {
			r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeSuccess) })
			c := testClient(t, r.addr, func(cfg *Config) { cfg.Zone = zone })
			// Verification belongs to a later change. Only the transport result matters here.
			if err := roundWithin(t, context.Background(), c, 1000); errors.Is(err, ErrTransport) {
				t.Fatalf("Round error = %v, want no transport error", err)
			}
			qs := r.queries()
			if len(qs) != 1 {
				t.Fatalf("resolver received %d queries, want 1", len(qs))
			}
			m := qs[0].msg
			if len(m.Question) != 1 {
				t.Fatalf("query has %d questions, want 1", len(m.Question))
			}
			q := m.Question[0]
			if q.Name != "round.1000.quicknet.beacon.example." || q.Qtype != dns.TypeTXT {
				t.Fatalf("question = %v, want TXT round.1000.quicknet.beacon.example.", q)
			}
			opt := m.IsEdns0()
			if opt == nil || opt.UDPSize() != 1232 {
				t.Fatalf("query OPT = %v, want UDP size 1232", opt)
			}
		})
	}
}

func TestQueryAcceptsAnswerWithNoOPT(t *testing.T) {
	want := vectorTXT(t, "quicknet")
	r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg {
		resp := txtReply(req, want)
		if resp.IsEdns0() != nil {
			t.Errorf("test reply has an OPT record")
		}
		return resp
	})
	c := testClient(t, r.addr, nil)
	rrs, err := queryWithin(t, context.Background(), c, "round.1000.quicknet.beacon.example.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rrs) != 1 {
		t.Fatalf("query returned %d records, want 1", len(rrs))
	}
}

func writeResolvConf(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write resolv.conf: %v", err)
	}
	return p
}

func TestSystemResolverUsesFirstNameserver(t *testing.T) {
	for name, tc := range map[string]struct{ conf, want string }{
		"IPv4": {"search example.\nnameserver 192.0.2.1\nnameserver 192.0.2.2\n", "192.0.2.1:53"},
		"IPv6": {"nameserver 2001:db8::1\nnameserver 192.0.2.2\n", "[2001:db8::1]:53"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := systemResolver(writeResolvConf(t, tc.conf))
			if err != nil {
				t.Fatalf("systemResolver: %v", err)
			}
			if got != tc.want {
				t.Fatalf("systemResolver = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewUsesSystemResolverWhenResolverIsEmpty(t *testing.T) {
	path := writeResolvConf(t, "nameserver 192.0.2.7\n")
	c, err := newClient(Config{Zone: testZone, Chain: chain.Quicknet}, path)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.resolver != "192.0.2.7:53" {
		t.Fatalf("resolver = %q, want 192.0.2.7:53", c.resolver)
	}
}

func TestNewFailsWithoutSystemResolver(t *testing.T) {
	cfg := Config{Zone: testZone, Chain: chain.Quicknet}
	t.Run("missing file", func(t *testing.T) {
		if _, err := newClient(cfg, filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("newClient succeeded with no resolv.conf")
		}
	})
	t.Run("no nameserver line", func(t *testing.T) {
		if _, err := newClient(cfg, writeResolvConf(t, "search example.\n")); err == nil {
			t.Fatal("newClient succeeded with no nameserver line")
		}
	})
}

func TestNewWithResolverIgnoresResolvConf(t *testing.T) {
	cfg := Config{Zone: testZone, Chain: chain.Quicknet, Resolver: "192.0.2.9:5353"}
	c, err := newClient(cfg, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.resolver != "192.0.2.9:5353" {
		t.Fatalf("resolver = %q, want 192.0.2.9:5353", c.resolver)
	}
}

func TestNewRejectsBadZoneAndResolver(t *testing.T) {
	for name, cfg := range map[string]Config{
		"empty zone":         {Zone: "", Chain: chain.Quicknet, Resolver: "127.0.0.1:53"},
		"malformed zone":     {Zone: "bad..zone", Chain: chain.Quicknet, Resolver: "127.0.0.1:53"},
		"resolver, no port":  {Zone: testZone, Chain: chain.Quicknet, Resolver: "127.0.0.1"},
		"resolver, no host":  {Zone: testZone, Chain: chain.Quicknet, Resolver: ":53"},
		"resolver, bad port": {Zone: testZone, Chain: chain.Quicknet, Resolver: "127.0.0.1:"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("New succeeded, want an error")
			}
		})
	}
}

func TestNewAppliesTimeoutAndClockDefaults(t *testing.T) {
	c := testClient(t, "127.0.0.1:53", nil)
	if c.timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", c.timeout)
	}
	if c.clock == nil || time.Since(c.clock()) > time.Minute {
		t.Fatal("default clock is not the system clock")
	}
	fixed := time.Unix(1700000000, 0)
	c = testClient(t, "127.0.0.1:53", func(cfg *Config) {
		cfg.Timeout = time.Second
		cfg.Clock = func() time.Time { return fixed }
	})
	if c.timeout != time.Second || !c.clock().Equal(fixed) {
		t.Fatal("New ignored Config.Timeout or Config.Clock")
	}
}

func TestRoundContextEndWrapsTransportAndContextError(t *testing.T) {
	release := make(chan struct{})
	r := startResolver(t, func(string, *dns.Msg) *dns.Msg {
		<-release
		return nil
	})
	// This cleanup runs before the server shutdown, so blocked handlers end first.
	t.Cleanup(func() { close(release) })
	c := testClient(t, r.addr, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		r.waitQuery(t)
		cancel()
	}()
	err := roundWithin(t, ctx, c, 1000)
	if !errors.Is(err, ErrTransport) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Round error = %v, want ErrTransport and context.Canceled", err)
	}
}

func TestRoundTimeoutReturnsErrTransport(t *testing.T) {
	release := make(chan struct{})
	r := startResolver(t, func(string, *dns.Msg) *dns.Msg {
		<-release
		return nil
	})
	t.Cleanup(func() { close(release) })
	c := testClient(t, r.addr, func(cfg *Config) { cfg.Timeout = 50 * time.Millisecond })
	err := roundWithin(t, context.Background(), c, 1000)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Round error = %v, want ErrTransport", err)
	}
}

func TestRoundReturnsErrTransportWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c := testClient(t, addr, nil)
	if err := roundWithin(t, context.Background(), c, 1000); !errors.Is(err, ErrTransport) {
		t.Fatalf("Round error = %v, want ErrTransport", err)
	}
}
