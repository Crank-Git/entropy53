package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestApexSOAHasConfiguredFields(t *testing.T) {
	addr, _ := newTestServer(t)
	r := ask(t, addr, testZone, dns.TypeSOA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("rcode = %s, answers = %d, want NOERROR and 1", dns.RcodeToString[r.Rcode], len(r.Answer))
	}
	soa, ok := r.Answer[0].(*dns.SOA)
	if !ok {
		t.Fatalf("answer = %T, want *dns.SOA", r.Answer[0])
	}
	want := dns.SOA{Ns: "ns1.example.net.", Mbox: "hostmaster.example.net.", Serial: 1, Refresh: 3600, Retry: 600, Expire: 604800, Minttl: 60}
	got := *soa
	got.Hdr, want.Hdr = dns.RR_Header{}, dns.RR_Header{}
	if got != want {
		t.Errorf("SOA = %+v, want %+v", got, want)
	}
	if soa.Hdr.Name != testZone {
		t.Errorf("SOA owner = %q, want %q", soa.Hdr.Name, testZone)
	}
}

func TestApexNSReturnsEachNSValue(t *testing.T) {
	addr, _ := newTestServer(t)
	r := ask(t, addr, testZone, dns.TypeNS)
	var got []string
	for _, rr := range r.Answer {
		ns, ok := rr.(*dns.NS)
		if !ok {
			t.Fatalf("answer = %T, want *dns.NS", rr)
		}
		got = append(got, ns.Ns)
	}
	if strings.Join(got, ",") != "ns1.example.net.,ns2.example.net." {
		t.Errorf("NS values = %v, want ns1.example.net. and ns2.example.net.", got)
	}
}

func TestApexOtherTypeReturnsNoError(t *testing.T) {
	addr, _ := newTestServer(t)
	wantRcode(t, addr, testZone, dns.TypeA, dns.RcodeSuccess)
	wantRcode(t, addr, testZone, dns.TypeTXT, dns.RcodeSuccess)
}

func TestAXFRReturnsREFUSEDWithNoRecords(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, qtype := range []uint16{dns.TypeAXFR, dns.TypeIXFR} {
		r := ask(t, addr, testZone, qtype)
		if r.Rcode != dns.RcodeRefused {
			t.Errorf("type %d: rcode = %s, want REFUSED", qtype, dns.RcodeToString[r.Rcode])
		}
		if len(r.Answer)+len(r.Ns) != 0 {
			t.Errorf("type %d: answer and authority hold %d records, want 0", qtype, len(r.Answer)+len(r.Ns))
		}
	}
}

func TestAnswerInZoneSetsAA(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, name := range []string{testZone, "quicknet." + testZone, "foo." + testZone} {
		if r := ask(t, addr, name, dns.TypeSOA); !r.Authoritative {
			t.Errorf("%s: AA = false, want true", name)
		}
	}
}

func TestAnswerOutsideZoneClearsAA(t *testing.T) {
	addr, _ := newTestServer(t)
	r := ask(t, addr, "round.1000.quicknet.example.org.", dns.TypeTXT)
	if r.Authoritative {
		t.Error("AA = true, want false")
	}
}

func TestAnswerClearsRAWhenQueryHasRD(t *testing.T) {
	addr, _ := newTestServer(t)
	m := new(dns.Msg)
	m.SetQuestion(testZone, dns.TypeSOA)
	m.RecursionDesired = true
	r := exchange(t, "udp", addr, m)
	if r.RecursionAvailable {
		t.Error("RA = true, want false")
	}
}

func TestAnswerHasOPTOnlyWhenQueryHasOPT(t *testing.T) {
	addr, _ := newTestServer(t)
	queries := map[string]func() *dns.Msg{
		"apex SOA": func() *dns.Msg { return new(dns.Msg).SetQuestion(testZone, dns.TypeSOA) },
		"no question": func() *dns.Msg {
			m := new(dns.Msg)
			m.Id = dns.Id()
			return m
		},
		"update": func() *dns.Msg {
			m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
			m.Opcode = dns.OpcodeUpdate
			return m
		},
	}
	for name, build := range queries {
		plain := exchange(t, "udp", addr, build())
		if plain.IsEdns0() != nil {
			t.Errorf("%s: answer has OPT for a query with no OPT", name)
		}
		m := build()
		m.SetEdns0(1232, false)
		r := exchange(t, "udp", addr, m)
		if r.IsEdns0() == nil {
			t.Errorf("%s: answer has no OPT for a query with OPT", name)
		}
	}
}

func TestDOBitChangesNothing(t *testing.T) {
	addr, _ := newTestServer(t)
	ask := func(do bool) *dns.Msg {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
		m.SetEdns0(1232, do)
		return exchange(t, "udp", addr, m)
	}
	with, without := ask(true), ask(false)
	if with.IsEdns0().Do() {
		t.Error("answer to a DO query has DO set, want clear")
	}
	if fmt.Sprint(with.Answer, with.Rcode, with.Ns) != fmt.Sprint(without.Answer, without.Rcode, without.Ns) {
		t.Errorf("DO changed the answer: %v and %v", with.Answer, without.Answer)
	}
}

func TestAnyMapsToTXTAtRoundAndLatestNames(t *testing.T) {
	for _, kind := range []nameKind{nameRound, nameLatest} {
		if got := effectiveType(kind, dns.TypeANY); got != dns.TypeTXT {
			t.Errorf("kind %d: ANY maps to %d, want TXT", kind, got)
		}
	}
	for _, kind := range []nameKind{nameApex, nameChain, nameRoundParent} {
		if got := effectiveType(kind, dns.TypeANY); got != dns.TypeANY {
			t.Errorf("kind %d: ANY maps to %d, want ANY", kind, got)
		}
	}
	if got := effectiveType(nameRound, dns.TypeA); got != dns.TypeA {
		t.Errorf("A at a round name maps to %d, want A", got)
	}
}

func TestZeroAndTwoQuestionsReturnFORMERR(t *testing.T) {
	addr, _ := newTestServer(t)
	zero := new(dns.Msg)
	zero.Id = dns.Id()
	two := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
	two.Question = append(two.Question, dns.Question{Name: testZone, Qtype: dns.TypeNS, Qclass: dns.ClassINET})
	for name, m := range map[string]*dns.Msg{"zero": zero, "two": two} {
		if r := exchange(t, "udp", addr, m); r.Rcode != dns.RcodeFormatError {
			t.Errorf("%s questions: rcode = %s, want FORMERR", name, dns.RcodeToString[r.Rcode])
		}
	}
}

func TestNotifyAndUpdateReturnNOTIMP(t *testing.T) {
	addr, _ := newTestServer(t)
	for _, op := range []int{dns.OpcodeNotify, dns.OpcodeUpdate, dns.OpcodeStatus} {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
		m.Opcode = op
		r := exchange(t, "udp", addr, m)
		if r.Rcode != dns.RcodeNotImplemented {
			t.Errorf("opcode %d: rcode = %s, want NOTIMP", op, dns.RcodeToString[r.Rcode])
		}
		if len(r.Answer) != 0 {
			t.Errorf("opcode %d: answer holds %d records, want 0", op, len(r.Answer))
		}
	}
}

func TestChaosClassReturnsREFUSED(t *testing.T) {
	addr, _ := newTestServer(t)
	m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
	m.Question[0].Qclass = dns.ClassCHAOS
	r := exchange(t, "udp", addr, m)
	if r.Rcode != dns.RcodeRefused {
		t.Errorf("rcode = %s, want REFUSED", dns.RcodeToString[r.Rcode])
	}
	if len(r.Answer) != 0 {
		t.Errorf("answer holds %d records, want 0", len(r.Answer))
	}
}

func TestApexNSOverUDPSetsTCAboveClientSize(t *testing.T) {
	cfg := testConfig(t)
	cfg.NS = nil
	for i := 0; i < 20; i++ {
		cfg.NS = append(cfg.NS, fmt.Sprintf("ns%02d-%s.example.net.", i, strings.Repeat("x", 50)))
	}
	l := startListeners(t, cfg, &fakeSource{})
	query := func(size uint16) *dns.Msg {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeNS)
		if size != 0 {
			m.SetEdns0(size, false)
		}
		return m
	}

	small := exchange(t, "udp", l.Addr(), query(0))
	if !small.Truncated {
		t.Error("UDP answer with no OPT: TC = false, want true")
	}
	if len(small.Answer) >= len(cfg.NS) {
		t.Errorf("UDP answer with no OPT holds %d records, want fewer than %d", len(small.Answer), len(cfg.NS))
	}
	large := exchange(t, "udp", l.Addr(), query(4096))
	if large.Truncated || len(large.Answer) != len(cfg.NS) {
		t.Errorf("UDP answer with size 4096: TC = %v, records = %d, want false and %d", large.Truncated, len(large.Answer), len(cfg.NS))
	}
	tcp := exchange(t, "tcp", l.Addr(), query(0))
	if tcp.Truncated || len(tcp.Answer) != len(cfg.NS) {
		t.Errorf("TCP answer: TC = %v, records = %d, want false and %d", tcp.Truncated, len(tcp.Answer), len(cfg.NS))
	}
}
