//go:build interop

package interop

import (
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// txtStrings returns the number of character strings in the first TXT record of m, or -1 when m has none.
func txtStrings(m *dnsmessage.Message) int {
	for _, a := range m.Answers {
		if txt, ok := a.Body.(*dnsmessage.TXTResource); ok {
			return len(txt.TXT)
		}
	}
	return -1
}

func TestResolverDefaultRoundWithTwoStrings(t *testing.T) {
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, r, withBeacon("default"), withLead(-2*time.Second))
			m, err := dnsQuery("udp", h.addr, roundNameFor("default"), dnsmessage.TypeTXT)
			if err != nil {
				t.Fatal(err)
			}
			if got := txtStrings(m); got != 2 {
				t.Errorf("default round: rcode %v with %d TXT strings, want 2", m.RCode, got)
			}
			cl, _ := newClient(t, h)
			awaitRound(t, h, cl, 250*time.Millisecond, 10*time.Second)
		})
	}
}

func TestResolverEvmnetRound(t *testing.T) {
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, r, withBeacon("evmnet"), withLead(-2*time.Second))
			cl, _ := newClient(t, h)
			awaitRound(t, h, cl, 250*time.Millisecond, 10*time.Second)
		})
	}
}

// The gateway never truncates a round answer, because the answer is smaller than 512 bytes.
// The counter returns the TC answer instead, so this test shows the resolver retry over TCP.
func TestResolverRetriesOverTCPForDefaultRound(t *testing.T) {
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, r, withBeacon("default"), withLead(-2*time.Second), withTruncatedAnswers())
			cl, _ := newClient(t, h)
			awaitRound(t, h, cl, 250*time.Millisecond, 10*time.Second)
			var udp, tcp int
			for _, e := range h.counter.exchanges() {
				if e.query != roundNameFor("default") {
					continue
				}
				switch e.network {
				case "udp":
					udp++
				case "tcp":
					tcp++
				}
			}
			if udp == 0 || tcp == 0 {
				t.Errorf("round name queries: %d over UDP and %d over TCP, want at least one of each: %+v", udp, tcp, h.counter.exchanges())
			}
		})
	}
}
