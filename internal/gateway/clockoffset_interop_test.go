//go:build interop

package gateway

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// offsetListeners runs a gateway whose clock stands d after the time of round 1000 of quicknet.
// A negative d puts the clock before the round time.
func offsetListeners(t *testing.T, d time.Duration, src Source) *Listeners {
	t.Helper()
	cfg := testConfig(t)
	l, err := NewListeners(cfg, src)
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
	l.SetClockOffset(time.Until(cfg.Chains[0].RoundTime(1000).Add(d)))
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

func TestClockOffsetMakesRoundFuture(t *testing.T) {
	src := &fakeSource{}
	l := offsetListeners(t, -10*time.Second, src)
	r := ask(t, l.Addr(), "round.1000.quicknet."+testZone, dns.TypeTXT)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Fatalf("rcode = %s, answers = %d, want NOERROR and 0", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	soa, ok := r.Ns[0].(*dns.SOA)
	if !ok || soa.Hdr.Ttl < 9 || soa.Hdr.Ttl > 10 {
		t.Errorf("authority = %v, want an SOA with a TTL of 9 or 10 seconds", r.Ns)
	}
	if n := src.calls(); n != 0 {
		t.Errorf("Source calls = %d, want 0 for a future round", n)
	}
}

func TestClockOffsetMovesWhileRunning(t *testing.T) {
	src := &fakeSource{}
	l := offsetListeners(t, -2*time.Minute, src)
	name := "round.1000.quicknet." + testZone
	if r := ask(t, l.Addr(), name, dns.TypeTXT); len(r.Ns) != 1 {
		t.Fatalf("authority = %v, want the SOA of a future round", r.Ns)
	}
	l.SetClockOffset(time.Until(testConfig(t).Chains[0].RoundTime(1000).Add(time.Second)))
	r := ask(t, l.Addr(), name, dns.TypeTXT)
	if soa, ok := firstSOA(r); ok && soa.Hdr.Ttl > 1 {
		t.Errorf("SOA TTL = %d after the clock passed the round time, want a due round", soa.Hdr.Ttl)
	}
	if src.calls() == 0 {
		t.Error("Source calls = 0, want a call once the round is due")
	}
}

func firstSOA(r *dns.Msg) (*dns.SOA, bool) {
	if len(r.Ns) == 0 {
		return nil, false
	}
	soa, ok := r.Ns[0].(*dns.SOA)
	return soa, ok
}

func TestClockOffsetMakesRoundDue(t *testing.T) {
	v := vectorOf(t, "quicknet")
	sig, err := hex.DecodeString(v.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	src := &fakeSource{roundFn: func(chain.Chain, uint64) (chain.Round, Status) {
		return chain.Round{Number: v.Round, Sig: sig}, Found
	}}
	l := offsetListeners(t, time.Second, src)
	r := ask(t, l.Addr(), "round.1000.quicknet."+testZone, dns.TypeTXT)
	if len(r.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 once the clock passes the round time", len(r.Answer))
	}
}
