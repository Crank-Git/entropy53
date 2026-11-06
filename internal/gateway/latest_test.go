package gateway

import (
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// latestName is the latest name of quicknet in the test zone.
const latestName = "latest.quicknet." + testZone

// latestSource returns a fakeSource whose latest round is r.
func latestSource(r chain.Round) *fakeSource {
	return &fakeSource{latestFn: func(chain.Chain) (chain.Round, bool) { return r, true }}
}

// latestAfter asks for TXT at the latest name when the latest round is quicknet round 1000
// and the clock stands `after` past the time of round 1000.
func latestAfter(t *testing.T, after time.Duration) *dns.Msg {
	t.Helper()
	v := vectorOf(t, "quicknet")
	now := chain.Quicknet.RoundTime(v.Round).Add(after)
	addr := startQuicknetAt(t, now, latestSource(roundOf(t, v)))
	return ask(t, addr, latestName, dns.TypeTXT)
}

// wantLatestTTL fails unless r holds one TXT record with the given TTL.
func wantLatestTTL(t *testing.T, r *dns.Msg, ttl uint32) {
	t.Helper()
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("rcode = %s, answers = %d, want NOERROR and 1", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	txt, ok := r.Answer[0].(*dns.TXT)
	if !ok {
		t.Fatalf("answer = %T, want *dns.TXT", r.Answer[0])
	}
	if txt.Hdr.Ttl != ttl {
		t.Errorf("TTL = %d, want %d", txt.Hdr.Ttl, ttl)
	}
}

func TestLatestOneSecondAfterRoundTimeHasTTL2(t *testing.T) {
	wantLatestTTL(t, latestAfter(t, time.Second), 2)
}

func TestLatestNameReturnsLatestRoundRecord(t *testing.T) {
	r := latestAfter(t, time.Second)

	wantLatestTTL(t, r, 2)
	txt := r.Answer[0].(*dns.TXT)
	if len(txt.Txt) != 1 || txt.Txt[0] != quicknetTXT {
		t.Errorf("TXT = %q, want [%q]", txt.Txt, quicknetTXT)
	}
	if txt.Hdr.Name != latestName {
		t.Errorf("owner = %q, want %q", txt.Hdr.Name, latestName)
	}
	if !r.Authoritative {
		t.Error("AA = false, want true")
	}
}

func TestLatestDefaultRoundHasTwoStrings(t *testing.T) {
	v := vectorOf(t, "default")
	cfg := testConfig(t)
	cfg.Chains = []chain.Chain{chain.Default}
	addr := startRoundHandler(t, cfg, chain.Default.RoundTime(v.Round).Add(time.Second), latestSource(roundOf(t, v)))

	r := ask(t, addr, "latest.default."+testZone, dns.TypeTXT)

	if len(r.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 (rcode %s)", len(r.Answer), dns.RcodeToString[r.Rcode])
	}
	if txt := r.Answer[0].(*dns.TXT); len(txt.Txt) != 2 {
		t.Errorf("TXT holds %d character-strings, want 2", len(txt.Txt))
	}
}

func TestLatestCallsNoRound(t *testing.T) {
	v := vectorOf(t, "quicknet")
	src := latestSource(roundOf(t, v))
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round), src)

	r := ask(t, addr, latestName, dns.TypeTXT)

	if len(r.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 (rcode %s)", len(r.Answer), dns.RcodeToString[r.Rcode])
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.roundCalls != 0 || src.latestCalls != 1 {
		t.Errorf("Round calls = %d, Latest calls = %d, want 0 and 1", src.roundCalls, src.latestCalls)
	}
}

func TestLatestAtRoundTimeHasTTLOfPeriod(t *testing.T) {
	wantLatestTTL(t, latestAfter(t, 0), uint32(chain.Quicknet.Period))
}

func TestLatestPartSecondRoundsUp(t *testing.T) {
	wantLatestTTL(t, latestAfter(t, 500*time.Millisecond), 3)
}

func TestLatestBehindClockHasTTL1(t *testing.T) {
	wantLatestTTL(t, latestAfter(t, 10*time.Second), 1)
}

func TestNoLatestReturnsNoDataWithTTL1(t *testing.T) {
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(2000), &fakeSource{})

	wantNegative(t, ask(t, addr, latestName, dns.TypeTXT), dns.RcodeSuccess, 1)
}

func TestAnyAtLatestNameReturnsTXTRecord(t *testing.T) {
	v := vectorOf(t, "quicknet")
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Second), latestSource(roundOf(t, v)))

	wantLatestTTL(t, ask(t, addr, latestName, dns.TypeANY), 2)
}

func TestMixedCaseLatestNameKeepsQuestionCase(t *testing.T) {
	v := vectorOf(t, "quicknet")
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Second), latestSource(roundOf(t, v)))
	name := "LATEST.QuickNet.Beacon.EXAMPLE."

	r := ask(t, addr, name, dns.TypeTXT)

	if len(r.Question) != 1 || r.Question[0].Name != name {
		t.Errorf("question = %v, want name %q", r.Question, name)
	}
	if len(r.Answer) != 1 || r.Answer[0].Header().Name != name {
		t.Fatalf("answer = %v, want one record owned by %q", r.Answer, name)
	}
}

func TestLatestEncodeErrorReturnsSERVFAIL(t *testing.T) {
	var logs logBuffer
	cfg := testConfig(t)
	cfg.Log = slog.New(slog.NewTextHandler(&logs, nil))
	short := chain.Round{Number: 1000, Sig: []byte{1, 2, 3}}
	addr := startRoundHandler(t, cfg, chain.Quicknet.RoundTime(1000), latestSource(short))

	wantRcode(t, addr, latestName, dns.TypeTXT, dns.RcodeServerFailure)

	if !strings.Contains(logs.String(), "latest record encode failed") {
		t.Errorf("log = %q, want a line about the encode failure", logs.String())
	}
}

// TestLatestAnswerSizeForEachChain measures the wire sizes that the Internet-Draft and the threat model
// state for the reflection gain. The query has no OPT record, as an old stub resolver sends it.
func TestLatestAnswerSizeForEachChain(t *testing.T) {
	cases := []struct {
		chain  chain.Chain
		query  int
		answer int
	}{
		{chain.Quicknet, 43, 132},
		{chain.Default, 42, 324},
		{chain.Evmnet, 41, 154},
	}
	for _, c := range cases {
		t.Run(c.chain.BeaconID, func(t *testing.T) {
			v := vectorOf(t, c.chain.BeaconID)
			cfg := testConfig(t)
			cfg.Zone = "b.example."
			cfg.Chains = []chain.Chain{c.chain}
			addr := startRoundHandler(t, cfg, c.chain.RoundTime(v.Round).Add(time.Second), latestSource(roundOf(t, v)))
			q := new(dns.Msg)
			q.SetQuestion("latest."+c.chain.BeaconID+".b.example.", dns.TypeTXT)
			q.RecursionDesired = false
			wire, err := q.Pack()
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}

			conn, err := net.Dial("udp", addr)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			if err := conn.SetDeadline(time.Now().Add(dnsWait)); err != nil {
				t.Fatalf("SetDeadline: %v", err)
			}
			if _, err := conn.Write(wire); err != nil {
				t.Fatalf("Write: %v", err)
			}
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			if len(wire) != c.query || n != c.answer {
				t.Errorf("query = %d bytes, answer = %d bytes, want %d and %d", len(wire), n, c.query, c.answer)
			}
		})
	}
}
