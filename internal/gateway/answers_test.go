package gateway

import (
	"testing"

	"github.com/miekg/dns"
)

// newTestServer starts a handler with testConfig and returns its address and source.
func newTestServer(t *testing.T) (string, *fakeSource) {
	t.Helper()
	src := &fakeSource{}
	return startDNS(t, newHandler(testConfig(t), src)), src
}

// wantRcode asks for name and TXT, and fails if the RCODE differs or the answer holds a record.
func wantRcode(t *testing.T, addr, name string, qtype uint16, rcode int) {
	t.Helper()
	r := ask(t, addr, name, qtype)
	if r.Rcode != rcode {
		t.Errorf("%s type %d: rcode = %s, want %s", name, qtype, dns.RcodeToString[r.Rcode], dns.RcodeToString[rcode])
	}
	if len(r.Answer) != 0 {
		t.Errorf("%s type %d: answer section holds %d records, want 0", name, qtype, len(r.Answer))
	}
}

func TestInvalidRoundLabelReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.abc.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
}

func TestUnknownChainReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.1000.unknown."+testZone, dns.TypeTXT, dns.RcodeNameError)
	wantRcode(t, addr, "round.1000.default."+testZone, dns.TypeTXT, dns.RcodeNameError)
}

func TestOutsideZoneReturnsREFUSED(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.1000.quicknet.example.org.", dns.TypeTXT, dns.RcodeRefused)
	wantRcode(t, addr, "round.1000.quicknet.xbeacon.example.", dns.TypeTXT, dns.RcodeRefused)
}

func TestEmptyNonTerminalReturnsNoErrorForFutureAndPastRound(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, n := range []string{"1", "1000", "9007199254740992"} {
		wantRcode(t, addr, n+".quicknet."+testZone, dns.TypeTXT, dns.RcodeSuccess)
	}
}

func TestMixedCaseKeepsQuestionCase(t *testing.T) {
	addr, _ := newTestServer(t)
	name := "ROUND.abc.QuickNet.Beacon.EXAMPLE."
	r := ask(t, addr, name, dns.TypeTXT)
	if len(r.Question) != 1 || r.Question[0].Name != name {
		t.Errorf("question = %v, want name %q", r.Question, name)
	}
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("rcode = %s, want NXDOMAIN", dns.RcodeToString[r.Rcode])
	}
}

func TestMixedCaseNamesMatchWithoutRegardToCase(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "1000.QuickNet.BEACON.example.", dns.TypeTXT, dns.RcodeSuccess)
	wantRcode(t, addr, "QUICKNET.beacon.EXAMPLE.", dns.TypeTXT, dns.RcodeSuccess)
}

func TestLeadingZeroRoundReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.01000.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
}

func TestRoundZeroReturnsNXDOMAINAndCallsNoSource(t *testing.T) {
	addr, src := newTestServer(t)
	wantRcode(t, addr, "round.0.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
	if got := src.calls(); got != 0 {
		t.Errorf("source calls = %d, want 0", got)
	}
}

func TestRoundAboveMaxReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.9007199254740993.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
	// 2^53 is valid, so the name passes the parser and reaches the TXT stub.
	wantRcode(t, addr, "round.9007199254740992.quicknet."+testZone, dns.TypeA, dns.RcodeSuccess)
}

func TestUint64OverflowReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.18446744073709551616.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
	wantRcode(t, addr, "round.99999999999999999999999.quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
}

func TestNameShapesOutsideTableReturnNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, name := range []string{
		"foo." + testZone,
		"round.quicknet." + testZone,
		"latest.1000.quicknet." + testZone,
		"round.1000.quicknet.extra." + testZone,
		"x.round.1000.quicknet." + testZone,
		"latest.quicknet.extra." + testZone,
		"latest." + testZone,
		"round." + testZone,
	} {
		wantRcode(t, addr, name, dns.TypeTXT, dns.RcodeNameError)
	}
}

func TestInvalidNonTerminalRoundReturnsNXDOMAIN(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, n := range []string{"0", "01", "abc", "9007199254740993"} {
		wantRcode(t, addr, n+".quicknet."+testZone, dns.TypeTXT, dns.RcodeNameError)
	}
}

func TestChainNameReturnsNoError(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "quicknet."+testZone, dns.TypeTXT, dns.RcodeSuccess)
	wantRcode(t, addr, "quicknet."+testZone, dns.TypeA, dns.RcodeSuccess)
}

func TestApexReturnsNoError(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, testZone, dns.TypeTXT, dns.RcodeSuccess)
}

func TestValidRoundAndLatestNamesReturnNoErrorForOtherTypes(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.1000.quicknet."+testZone, dns.TypeA, dns.RcodeSuccess)
	wantRcode(t, addr, "latest.quicknet."+testZone, dns.TypeA, dns.RcodeSuccess)
}

func TestValidRoundAndLatestNamesReturnSERVFAILForTXT(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, "round.1000.quicknet."+testZone, dns.TypeTXT, dns.RcodeServerFailure)
	wantRcode(t, addr, "latest.quicknet."+testZone, dns.TypeTXT, dns.RcodeServerFailure)
}
