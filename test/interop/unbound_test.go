//go:build interop

package interop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/client"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	// unboundImage is Unbound 1.26.2. The multi-arch digest covers arm64 and amd64.
	unboundImage    = "klutchell/unbound:1.26.2@sha256:50cbeb32b5838a829a7226b19cb151f9535bd7f9f3190f950780f452fe807478"
	unboundConfPath = "/etc/unbound/unbound.conf"
)

// unboundConfig returns a configuration with only the settings that the tests need.
// Every other option keeps its default.
func unboundConfig(s stub, settings []setting) (string, error) {
	var b strings.Builder
	b.WriteString("server:\n  interface: 0.0.0.0@53\n  access-control: 0.0.0.0/0 allow\n")
	for _, st := range settings {
		switch st {
		case settingQNAMEMinimization:
			b.WriteString("  qname-minimisation: yes\n")
		case settingMinNegativeTTL30:
			b.WriteString("  cache-min-negative-ttl: 30\n")
		case settingRandomCase:
			b.WriteString("  use-caps-for-id: yes\n")
		default:
			return "", fmt.Errorf("unknown setting %d", st)
		}
	}
	fmt.Fprintf(&b, "stub-zone:\n  name: %q\n  stub-addr: %s@%d\n", s.zone, s.ip, s.port)
	return b.String(), nil
}

// startUnbound runs Unbound with a stub zone that points at the gateway, and returns the client address.
func startUnbound(t *testing.T, s stub, settings []setting) string {
	t.Helper()
	conf, err := unboundConfig(s, settings)
	if err != nil {
		t.Fatalf("build the Unbound configuration: %v", err)
	}
	port := freePort(t)
	name := startContainer(t, unboundImage,
		[]string{"-p", fmt.Sprintf("127.0.0.1:%d:53/udp", port), "-p", fmt.Sprintf("127.0.0.1:%d:53/tcp", port)},
		map[string]string{unboundConfPath: conf}, "-d", "-c", unboundConfPath)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	waitFor(t, "Unbound to answer the zone apex", name, 30*time.Second, func() (bool, string) {
		m, err := dnsQuery("udp", addr, s.zone, dnsmessage.TypeSOA)
		if err != nil {
			return false, err.Error()
		}
		return m.RCode == dnsmessage.RCodeSuccess && len(m.Answers) == 1,
			fmt.Sprintf("rcode %v with %d answers", m.RCode, len(m.Answers))
	})
	return addr
}

// newClient returns a client library client that queries the resolver of h.
// Its clock is the gateway clock, so that the client classifies a negative answer by the gateway time.
func newClient(t *testing.T, h *harness) (*client.Client, chain.Chain) {
	t.Helper()
	c, err := chain.Lookup(h.beacon)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Config{Zone: zone, Chain: c, Resolver: h.addr, Timeout: queryTimeout, Clock: h.gw.now})
	if err != nil {
		t.Fatalf("create the client: %v", err)
	}
	return cl, c
}

// awaitRound queries the test round every interval until the client receives a verified round.
// It returns the time on the gateway clock at that moment.
func awaitRound(t *testing.T, h *harness, cl *client.Client, interval, timeout time.Duration) time.Time {
	t.Helper()
	var got time.Time
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		r, err := cl.Round(context.Background(), testRound)
		if err == nil {
			checkRandomness(t, h.beacon, r)
			got = h.gw.now()
			return got
		}
		if !errors.Is(err, client.ErrNotPublished) && !errors.Is(err, client.ErrWithheld) {
			t.Fatalf("Round(%d): %v", testRound, err)
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("no verified round after %s: %v\n%s", timeout, err, containerLogs(""))
		}
	}
}

// checkRandomness compares the randomness that the client computed with the value in the vector file.
func checkRandomness(t *testing.T, beacon string, r client.Round) {
	t.Helper()
	for _, v := range loadVectors(t) {
		if v.BeaconID == beacon && v.Round == testRound {
			if got := hex.EncodeToString(r.Randomness[:]); got != v.Randomness {
				t.Errorf("randomness = %s, want %s", got, v.Randomness)
			}
			return
		}
	}
	t.Fatalf("no vector for %s round %d", beacon, testRound)
}

func unbound() resolver { return resolvers[0] }

func TestUnboundPinnedRound(t *testing.T) {
	h := newHarness(t, unbound())
	cl, c := newClient(t, h)
	t.Run("returns NODATA before the round time", func(t *testing.T) {
		if !h.gw.now().Before(c.RoundTime(testRound)) {
			t.Fatal("the gateway clock is not before the round time")
		}
		m, err := dnsQuery("udp", h.addr, roundName(), dnsmessage.TypeTXT)
		if err != nil {
			t.Fatal(err)
		}
		if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 0 || len(m.Authorities) != 1 {
			t.Errorf("rcode %v, %d answers, %d authority records, want NOERROR, 0 and 1 SOA",
				m.RCode, len(m.Answers), len(m.Authorities))
		}
	})
	t.Run("returns a record that verifies after the round time", func(t *testing.T) {
		awaitRound(t, h, cl, 250*time.Millisecond, 20*time.Second)
	})
	t.Run("sends no gateway query on the third query", func(t *testing.T) {
		before := h.counter.count()
		if before == 0 {
			t.Fatal("the gateway received no query, so the counter is not in the path")
		}
		if _, err := cl.Round(context.Background(), testRound); err != nil {
			t.Fatalf("Round: %v", err)
		}
		if after := h.counter.count(); after != before {
			t.Errorf("gateway queries rose from %d to %d on a cached answer: %+v", before, after, h.counter.exchanges())
		}
	})
}

func TestQNAMEMinimizationReachesTXT(t *testing.T) {
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, r, withLead(-2*time.Second), withSettings(settingQNAMEMinimization))
			m, err := dnsQuery("udp", h.addr, roundName(), dnsmessage.TypeTXT)
			if err != nil {
				t.Fatal(err)
			}
			if m.RCode != dnsmessage.RCodeSuccess || !hasTXT(m) {
				t.Errorf("round name: rcode %v, TXT %v, want NOERROR and a TXT record", m.RCode, hasTXT(m))
			}
			cl, _ := newClient(t, h)
			awaitRound(t, h, cl, 250*time.Millisecond, 10*time.Second)
			parent := fmt.Sprintf("%d.%s.%s", testRound, testBeacon, zone)
			m, err = dnsQuery("udp", h.addr, parent, dnsmessage.TypeTXT)
			if err != nil {
				t.Fatal(err)
			}
			if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 0 {
				t.Errorf("empty non-terminal: rcode %v with %d answers, want NODATA", m.RCode, len(m.Answers))
			}
			for _, e := range h.counter.exchanges() {
				t.Logf("gateway query over %s: %s", e.network, e.query)
			}
		})
	}
}

func TestClientReceivesRoundWithinFiveSeconds(t *testing.T) {
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, r, withLead(5*time.Second))
			cl, c := newClient(t, h)
			at := awaitRound(t, h, cl, time.Second, 15*time.Second)
			if late := at.Sub(c.RoundTime(testRound)); late > 5*time.Second {
				t.Errorf("the client received the round %s after the round time, want 5 seconds at most", late)
			}
		})
	}
}

func TestUnboundMinNegativeTTLRecordsDelay(t *testing.T) {
	h := newHarness(t, unbound(), withLead(4*time.Second), withSettings(settingMinNegativeTTL30))
	cl, c := newClient(t, h)
	asked := h.gw.now()
	if _, err := cl.Round(context.Background(), testRound); !errors.Is(err, client.ErrNotPublished) {
		t.Fatalf("first query: %v, want ErrNotPublished", err)
	}
	at := awaitRound(t, h, cl, time.Second, 60*time.Second)
	t.Logf("cache-min-negative-ttl 30 (Unbound 1.26.2): first query %s before the round time, record %s after the round time, NODATA held %s",
		c.RoundTime(testRound).Sub(asked).Round(time.Second), at.Sub(c.RoundTime(testRound)).Round(time.Second), at.Sub(asked).Round(time.Second))
}

func TestUnboundRandomCaseKeepsLetterCase(t *testing.T) {
	h := newHarness(t, unbound(), withLead(-2*time.Second), withSettings(settingRandomCase))
	cl, _ := newClient(t, h)
	awaitRound(t, h, cl, 250*time.Millisecond, 10*time.Second)
	upper := regexp.MustCompile(`[A-Z]`)
	mixed := 0
	for _, e := range h.counter.exchanges() {
		if upper.MatchString(e.query) {
			mixed++
		}
		if e.reply != e.query {
			t.Errorf("answer question %q differs from query question %q", e.reply, e.query)
		}
	}
	if mixed == 0 {
		t.Errorf("no gateway query had a capital letter, so Unbound did not randomize the case: %+v", h.counter.exchanges())
	}
}
