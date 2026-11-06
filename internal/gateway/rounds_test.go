package gateway

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// quicknetTXT is the TXT text of quicknet round 1000, as the feature file prints it.
const quicknetTXT = "AQAAAAAAAAPotEZ5uaWa8uyHaxprGtUuqbFhX8OYKxlXY1D5NEfLESXjQrc6jdK6y+R+S2tj7V45"

// roundOf returns the chain.Round that vector v describes.
func roundOf(t *testing.T, v vector) chain.Round {
	t.Helper()
	sig, err := hex.DecodeString(v.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	prev, err := hex.DecodeString(v.PreviousSignature)
	if err != nil {
		t.Fatalf("decode previous signature: %v", err)
	}
	return chain.Round{Number: v.Round, Sig: sig, PrevSig: prev}
}

// startRoundHandler serves a handler for cfg on a fake clock at now. The source returns r for round r.Number.
func startRoundHandler(t *testing.T, cfg ZoneConfig, now time.Time, src *fakeSource) string {
	t.Helper()
	h := newHandler(cfg, src)
	h.clock = newFakeClock(now)
	return startDNS(t, h)
}

// sourceOf returns a fakeSource that serves r and reports every other round as Unavailable.
func sourceOf(r chain.Round) *fakeSource {
	return &fakeSource{roundFn: func(_ chain.Chain, n uint64) (chain.Round, Status) {
		if n == r.Number {
			return r, Found
		}
		return chain.Round{}, Unavailable
	}}
}

// sourceStatus returns a fakeSource that answers every round with status.
func sourceStatus(status Status) *fakeSource {
	return &fakeSource{roundFn: func(chain.Chain, uint64) (chain.Round, Status) {
		return chain.Round{}, status
	}}
}

// startQuicknetAt serves quicknet from src on a fake clock at now.
func startQuicknetAt(t *testing.T, now time.Time, src *fakeSource) string {
	t.Helper()
	return startRoundHandler(t, testConfig(t), now, src)
}

// roundName returns the round name of quicknet round n in the test zone.
func roundName(n uint64) string {
	return fmt.Sprintf("round.%d.quicknet.%s", n, testZone)
}

// onlySOA returns the one SOA record in the authority section of r.
func onlySOA(t *testing.T, r *dns.Msg) *dns.SOA {
	t.Helper()
	if len(r.Ns) != 1 {
		t.Fatalf("authority section holds %d records, want 1", len(r.Ns))
	}
	soa, ok := r.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("authority record = %T, want *dns.SOA", r.Ns[0])
	}
	return soa
}

// wantNegative fails unless r is NOERROR with no answer and an SOA whose TTL and MINIMUM equal ttl.
func wantNegative(t *testing.T, r *dns.Msg, rcode int, ttl uint32) {
	t.Helper()
	if r.Rcode != rcode {
		t.Errorf("rcode = %s, want %s", dns.RcodeToString[r.Rcode], dns.RcodeToString[rcode])
	}
	if len(r.Answer) != 0 {
		t.Errorf("answer section holds %d records, want 0", len(r.Answer))
	}
	soa := onlySOA(t, r)
	if soa.Hdr.Ttl != ttl || soa.Minttl != ttl {
		t.Errorf("SOA TTL = %d, MINIMUM = %d, want both %d", soa.Hdr.Ttl, soa.Minttl, ttl)
	}
	if soa.Hdr.Name != testZone {
		t.Errorf("SOA owner = %q, want %q", soa.Hdr.Name, testZone)
	}
}

// futureAnswer asks for TXT at a quicknet round that lies ahead by `ahead` on a clock fixed in time.
func futureAnswer(t *testing.T, n uint64, ahead time.Duration) (*dns.Msg, *fakeSource) {
	t.Helper()
	src := sourceStatus(Unavailable)
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(n).Add(-ahead), src)
	return ask(t, addr, roundName(n), dns.TypeTXT), src
}

func TestRoundNameReturnsOneTXTWithTTL86400(t *testing.T) {
	v := vectorOf(t, "quicknet")
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Hour), sourceOf(roundOf(t, v)))

	r := ask(t, addr, roundName(v.Round), dns.TypeTXT)

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("rcode = %s, answers = %d, want NOERROR and 1", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	txt, ok := r.Answer[0].(*dns.TXT)
	if !ok {
		t.Fatalf("answer = %T, want *dns.TXT", r.Answer[0])
	}
	if txt.Hdr.Ttl != 86400 {
		t.Errorf("TTL = %d, want 86400", txt.Hdr.Ttl)
	}
	if len(txt.Txt) != 1 || txt.Txt[0] != quicknetTXT {
		t.Errorf("TXT = %q, want [%q]", txt.Txt, quicknetTXT)
	}
	if !r.Authoritative {
		t.Error("AA = false, want true")
	}
}

func TestDefaultRoundTXTHasTwoStrings(t *testing.T) {
	v := vectorOf(t, "default")
	cfg := testConfig(t)
	cfg.Chains = []chain.Chain{chain.Default}
	addr := startRoundHandler(t, cfg, chain.Default.RoundTime(v.Round).Add(time.Hour), sourceOf(roundOf(t, v)))

	r := ask(t, addr, "round.1000.default."+testZone, dns.TypeTXT)

	if len(r.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 (rcode %s)", len(r.Answer), dns.RcodeToString[r.Rcode])
	}
	if txt := r.Answer[0].(*dns.TXT); len(txt.Txt) != 2 {
		t.Errorf("TXT holds %d character-strings, want 2", len(txt.Txt))
	}
}

func TestFutureRound1000SecondsAwayHasSOATTL300(t *testing.T) {
	r, _ := futureAnswer(t, 1_000_000, 1000*time.Second)
	wantNegative(t, r, dns.RcodeSuccess, 300)
}

func TestFutureRound100SecondsAwayHasSOATTLAndMinimum100(t *testing.T) {
	r, _ := futureAnswer(t, 1_000_000, 100*time.Second)
	wantNegative(t, r, dns.RcodeSuccess, 100)
}

func TestFutureRoundHalfSecondAwayHasSOATTL1(t *testing.T) {
	r, _ := futureAnswer(t, 1_000_000, 500*time.Millisecond)
	wantNegative(t, r, dns.RcodeSuccess, 1)
}

func TestFutureRoundCallsNoSource(t *testing.T) {
	_, src := futureAnswer(t, 1_000_000, 100*time.Second)
	if got := src.calls(); got != 0 {
		t.Errorf("source calls = %d, want 0", got)
	}
}

func TestUnavailableMissingRoundReturnsSERVFAIL(t *testing.T) {
	v := vectorOf(t, "quicknet")
	src := sourceStatus(Unavailable)
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Hour), src)

	wantRcode(t, addr, roundName(v.Round), dns.TypeTXT, dns.RcodeServerFailure)

	if got := src.calls(); got != 1 {
		t.Errorf("source calls = %d, want 1", got)
	}
}

func TestFutureRoundNeverReturnsNXDOMAIN(t *testing.T) {
	for _, ahead := range []time.Duration{time.Millisecond, time.Second, time.Minute, 24 * time.Hour} {
		r, _ := futureAnswer(t, 5_000_000, ahead)
		if r.Rcode != dns.RcodeSuccess {
			t.Errorf("%s ahead: rcode = %s, want NOERROR", ahead, dns.RcodeToString[r.Rcode])
		}
	}
}

func TestMaxRoundTTLIs300(t *testing.T) {
	r, _ := futureAnswer(t, chain.MaxRound, time.Hour)
	wantNegative(t, r, dns.RcodeSuccess, 300)
}

func TestClampTTLRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want uint32
	}{
		{-time.Second, 1},
		{0, 1},
		{time.Nanosecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{99500 * time.Millisecond, 100},
		{300 * time.Second, 300},
		{300*time.Second + time.Nanosecond, 300},
		{time.Duration(1<<63 - 1), 300},
	} {
		if got := clampTTL(tc.d, 1, 300); got != tc.want {
			t.Errorf("clampTTL(%v, 1, 300) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

func TestRoundAtItsRoundTimeWithNotPublishedHasTTL1(t *testing.T) {
	const n = 1_000_000
	src := sourceStatus(NotPublished)
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(n), src)

	r := ask(t, addr, roundName(n), dns.TypeTXT)

	wantNegative(t, r, dns.RcodeSuccess, 1)
	if got := src.calls(); got != 1 {
		t.Errorf("source calls = %d, want 1", got)
	}
}

func TestDueRoundNotPublishedHasTTL1(t *testing.T) {
	const n = 1_000_000
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(n).Add(2*time.Second), sourceStatus(NotPublished))

	wantNegative(t, ask(t, addr, roundName(n), dns.TypeTXT), dns.RcodeSuccess, 1)
}

func TestDefaultRoundOneReturnsSERVFAIL(t *testing.T) {
	v := vectorOf(t, "default")
	v.Round = 1
	v.PreviousSignature = strings.Repeat("00", 32)
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}, Rate: 10, Log: slog.New(slog.DiscardHandler)})
	now := chain.Default.RoundTime(10)
	u.clock = newFakeClock(now)
	cfg := testConfig(t)
	cfg.Chains = []chain.Chain{chain.Default}
	addr := startRoundHandler(t, cfg, now, &fakeSource{roundFn: func(c chain.Chain, n uint64) (chain.Round, Status) {
		return u.Round(context.Background(), c, n)
	}})

	wantRcode(t, addr, "round.1.default."+testZone, dns.TypeTXT, dns.RcodeServerFailure)

	if got := len(up.requestPaths()); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
	if _, ok := u.store.get("default", 1); ok {
		t.Error("the store holds the discarded round")
	}
}

func TestEncodeErrorReturnsSERVFAIL(t *testing.T) {
	var logs logBuffer
	cfg := testConfig(t)
	cfg.Log = slog.New(slog.NewTextHandler(&logs, nil))
	short := chain.Round{Number: 1000, Sig: []byte{1, 2, 3}}
	addr := startRoundHandler(t, cfg, chain.Quicknet.RoundTime(2000), sourceOf(short))

	wantRcode(t, addr, roundName(1000), dns.TypeTXT, dns.RcodeServerFailure)

	if !strings.Contains(logs.String(), "round record encode failed") {
		t.Errorf("log = %q, want a line about the encode failure", logs.String())
	}
}

func TestNoDataAndNXDOMAINSOATTLEqualsMinimum(t *testing.T) {
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(10), sourceStatus(Unavailable))

	wantNegative(t, ask(t, addr, "foo."+testZone, dns.TypeTXT), dns.RcodeNameError, 3600)
	wantNegative(t, ask(t, addr, "round.abc.quicknet."+testZone, dns.TypeTXT), dns.RcodeNameError, 3600)
}

func TestOtherTypeAtRoundNameIsNoData60(t *testing.T) {
	src := sourceStatus(Unavailable)
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(2000), src)

	for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeSOA, dns.TypeNS} {
		wantNegative(t, ask(t, addr, roundName(1000), qtype), dns.RcodeSuccess, 60)
	}
	if got := src.calls(); got != 0 {
		t.Errorf("source calls = %d, want 0", got)
	}
}

func TestOtherTypeAtLatestNameIsNoData60(t *testing.T) {
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(2000), sourceStatus(Unavailable))

	wantNegative(t, ask(t, addr, "latest.quicknet."+testZone, dns.TypeA), dns.RcodeSuccess, 60)
}

func TestEmptyNonTerminalIsNoData60(t *testing.T) {
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(2000), sourceStatus(Unavailable))

	for _, name := range []string{"quicknet." + testZone, "1000.quicknet." + testZone, "9007199254740992.quicknet." + testZone} {
		wantNegative(t, ask(t, addr, name, dns.TypeTXT), dns.RcodeSuccess, 60)
	}
}

func TestApexTXTIsNoData60(t *testing.T) {
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(2000), sourceStatus(Unavailable))

	wantNegative(t, ask(t, addr, testZone, dns.TypeTXT), dns.RcodeSuccess, 60)
}

func TestMixedCaseRoundNameReturnsTXTWithOriginalCase(t *testing.T) {
	v := vectorOf(t, "quicknet")
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Hour), sourceOf(roundOf(t, v)))
	name := "ROUND.1000.QuickNet.Beacon.EXAMPLE."

	r := ask(t, addr, name, dns.TypeTXT)

	if len(r.Answer) != 1 || r.Answer[0].Header().Name != name {
		t.Fatalf("answer = %v, want one record owned by %q", r.Answer, name)
	}
}

func TestAnyAtRoundNameReturnsTXTRecord(t *testing.T) {
	v := vectorOf(t, "quicknet")
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(v.Round).Add(time.Hour), sourceOf(roundOf(t, v)))

	r := ask(t, addr, roundName(v.Round), dns.TypeANY)

	if len(r.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 (rcode %s)", len(r.Answer), dns.RcodeToString[r.Rcode])
	}
	if txt, ok := r.Answer[0].(*dns.TXT); !ok || txt.Hdr.Ttl != 86400 {
		t.Errorf("answer = %v, want a TXT with TTL 86400", r.Answer[0])
	}
}

func TestAnyAtFutureRoundNameUsesRoundTTL(t *testing.T) {
	const n = 1_000_000
	addr := startQuicknetAt(t, chain.Quicknet.RoundTime(n).Add(-100*time.Second), sourceStatus(Unavailable))

	wantNegative(t, ask(t, addr, roundName(n), dns.TypeANY), dns.RcodeSuccess, 100)
}

func TestDefaultRoundOnLongZoneSetsTCAndTCPReturnsIt(t *testing.T) {
	v := vectorOf(t, "default")
	label := strings.Repeat("a", 50)
	cfg := testConfig(t)
	cfg.Zone = strings.Join([]string{label, label, label, label, "example."}, ".")
	cfg.Chains = []chain.Chain{chain.Default}
	l := startListeners(t, cfg, sourceOf(roundOf(t, v)))
	name := "round.1000.default." + cfg.Zone

	udp := ask(t, l.Addr(), name, dns.TypeTXT)
	if !udp.Truncated {
		t.Error("UDP answer: TC = false, want true")
	}

	m := new(dns.Msg)
	m.SetQuestion(name, dns.TypeTXT)
	tcp := exchange(t, "tcp", l.Addr(), m)
	if tcp.Truncated || len(tcp.Answer) != 1 {
		t.Errorf("TCP answer: TC = %v, answers = %d, want false and 1", tcp.Truncated, len(tcp.Answer))
	}
}
