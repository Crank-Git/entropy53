package gateway

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

var limitStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newTestLimit returns a limiter on a frozen clock.
func newTestLimit(rate, capacity int) (*udpLimit, *fakeClock) {
	clk := newFakeClock(limitStart)
	return newUDPLimit(rate, capacity, clk), clk
}

// exhaust spends the whole budget of addr's prefix.
func exhaust(t *testing.T, l *udpLimit, addr string, rate int) {
	t.Helper()
	for i := range rate {
		if got := l.decide(netip.MustParseAddr(addr)); got != limitAllow {
			t.Fatalf("query %d of %s: got action %d, want allow", i+1, addr, got)
		}
	}
}

func TestIPv4PrefixIs24Bits(t *testing.T) {
	l, _ := newTestLimit(2, 10)
	exhaust(t, l, "192.0.2.1", 2)
	if got := l.decide(netip.MustParseAddr("192.0.2.200")); got == limitAllow {
		t.Errorf("same /24: got allow, want a limit")
	}
	if got := l.decide(netip.MustParseAddr("192.0.3.1")); got != limitAllow {
		t.Errorf("next /24: got action %d, want allow", got)
	}
}

func TestIPv6PrefixIs56Bits(t *testing.T) {
	l, _ := newTestLimit(2, 10)
	exhaust(t, l, "2001:db8:0:10::1", 2)
	if got := l.decide(netip.MustParseAddr("2001:db8:0:1f::9")); got == limitAllow {
		t.Errorf("same /56: got allow, want a limit")
	}
	if got := l.decide(netip.MustParseAddr("2001:db8:0:100::1")); got != limitAllow {
		t.Errorf("next /56: got action %d, want allow", got)
	}
}

func TestMappedIPv4UsesIPv4Prefix(t *testing.T) {
	l, _ := newTestLimit(1, 10)
	exhaust(t, l, "192.0.2.1", 1)
	if got := l.decide(netip.MustParseAddr("::ffff:192.0.2.77")); got == limitAllow {
		t.Errorf("mapped address: got allow, want the /24 limit")
	}
}

func TestOtherPrefixHasOwnLimit(t *testing.T) {
	l, _ := newTestLimit(1, 10)
	exhaust(t, l, "192.0.2.1", 1)
	exhaust(t, l, "198.51.100.1", 1)
}

func TestExcessAlternatesTruncateAndDrop(t *testing.T) {
	l, _ := newTestLimit(3, 10)
	addr := netip.MustParseAddr("192.0.2.1")
	exhaust(t, l, "192.0.2.1", 3)
	want := []limitAction{limitTruncate, limitDrop, limitTruncate, limitDrop}
	for i, w := range want {
		if got := l.decide(addr); got != w {
			t.Errorf("excess %d: got action %d, want %d", i+1, got, w)
		}
	}
}

func TestLimitRefillsAfterOneSecond(t *testing.T) {
	l, clk := newTestLimit(2, 10)
	addr := netip.MustParseAddr("192.0.2.1")
	exhaust(t, l, "192.0.2.1", 2)
	if got := l.decide(addr); got != limitTruncate {
		t.Fatalf("over the rate: got action %d, want truncate", got)
	}
	clk.Advance(time.Second)
	exhaust(t, l, "192.0.2.1", 2)
	if got := l.decide(addr); got != limitTruncate {
		t.Errorf("first excess after a refill: got action %d, want truncate", got)
	}
}

func TestLimiterHoldsAtMostCapacityPrefixes(t *testing.T) {
	l, _ := newTestLimit(1, 3)
	for i := range 10 {
		l.decide(netip.AddrFrom4([4]byte{10, 0, byte(i), 1}))
	}
	if n := len(l.entries); n != 3 {
		t.Errorf("entries: got %d, want 3", n)
	}
	if n := l.order.Len(); n != 3 {
		t.Errorf("order: got %d, want 3", n)
	}
}

func TestLimiterEvictsLeastRecentlyUsedPrefix(t *testing.T) {
	l, _ := newTestLimit(1, 2)
	a := netip.MustParseAddr("10.0.1.1")
	b := netip.MustParseAddr("10.0.2.1")
	c := netip.MustParseAddr("10.0.3.1")
	l.decide(a)
	l.decide(b)
	l.decide(a) // a is now more recent than b
	l.decide(c) // evicts b
	if got := l.decide(a); got == limitAllow {
		t.Errorf("a kept its entry: got allow, want a limit")
	}
	if got := l.decide(b); got != limitAllow {
		t.Errorf("evicted b starts again: got action %d, want allow", got)
	}
}

func TestUDPLimitCapacityIs65536(t *testing.T) {
	if udpLimitCapacity != 65536 {
		t.Errorf("udpLimitCapacity: got %d, want 65536", udpLimitCapacity)
	}
}

// countingHandler answers every query and signals each call.
type countingHandler struct{ calls chan struct{} }

func newCountingHandler() *countingHandler { return &countingHandler{calls: make(chan struct{}, 100)} }

func (h *countingHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	h.calls <- struct{}{}
	_ = w.WriteMsg(new(dns.Msg).SetReply(r))
}

// waitCalls fails unless the handler received exactly want calls.
func (h *countingHandler) waitCalls(t *testing.T, want int) {
	t.Helper()
	for i := range want {
		select {
		case <-h.calls:
		case <-time.After(dnsWait):
			t.Fatalf("handler call %d of %d did not arrive within %s", i+1, want, dnsWait)
		}
	}
	select {
	case <-h.calls:
		t.Fatalf("handler received more than %d calls", want)
	default:
	}
}

// burst sends n queries from one socket and reads replies until none arrives for wait.
// Dropped queries cause that end, so wait bounds the test.
func burst(t *testing.T, addr string, n int, wait time.Duration) []*dns.Msg {
	t.Helper()
	conn, err := dns.Dial("udp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	for i := range n {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
		m.Id = uint16(i + 1)
		if err := conn.WriteMsg(m); err != nil {
			t.Fatalf("WriteMsg: %v", err)
		}
	}
	var got []*dns.Msg
	for {
		if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		r, err := conn.ReadMsg()
		if err != nil {
			return got
		}
		got = append(got, r)
	}
}

func TestExcessAnswersAreHalfTCAndHalfDropped(t *testing.T) {
	next := newCountingHandler()
	l, _ := newTestLimit(10, 10)
	addr := startDNS(t, &limitHandler{limit: l, next: next})
	replies := burst(t, addr, 30, 300*time.Millisecond)
	var full, tc int
	for _, r := range replies {
		if r.Truncated {
			tc++
		} else {
			full++
		}
	}
	if full != 10 || tc != 10 || len(replies) != 20 {
		t.Errorf("replies: got %d full and %d TC of %d, want 10 full and 10 TC of 20", full, tc, len(replies))
	}
	next.waitCalls(t, 10)
}

func TestExcessAnswerHasOnlyHeaderQuestionAndTC(t *testing.T) {
	l, _ := newTestLimit(1, 10)
	addr := startDNS(t, &limitHandler{limit: l, next: newCountingHandler()})
	var tcReply *dns.Msg
	for _, r := range burst(t, addr, 2, 300*time.Millisecond) {
		if r.Truncated {
			tcReply = r
		}
	}
	if tcReply == nil {
		t.Fatal("no truncated reply arrived")
	}
	if len(tcReply.Question) != 1 || tcReply.Question[0].Name != testZone || tcReply.Question[0].Qtype != dns.TypeSOA {
		t.Errorf("question: got %v", tcReply.Question)
	}
	if len(tcReply.Answer)+len(tcReply.Ns)+len(tcReply.Extra) != 0 {
		t.Errorf("sections: got %d answer, %d authority, %d additional records, want none",
			len(tcReply.Answer), len(tcReply.Ns), len(tcReply.Extra))
	}
	if !tcReply.Response || tcReply.Authoritative || tcReply.Rcode != dns.RcodeSuccess {
		t.Errorf("header: response=%v aa=%v rcode=%d", tcReply.Response, tcReply.Authoritative, tcReply.Rcode)
	}
}

func TestExcessQueryDoesNotReachHandler(t *testing.T) {
	next := newCountingHandler()
	l, _ := newTestLimit(1, 10)
	addr := startDNS(t, &limitHandler{limit: l, next: next})
	burst(t, addr, 5, 300*time.Millisecond)
	next.waitCalls(t, 1)
}

// remoteWriter is a ResponseWriter with a chosen remote address.
type remoteWriter struct {
	dns.ResponseWriter
	remote net.Addr
}

func (w *remoteWriter) RemoteAddr() net.Addr    { return w.remote }
func (w *remoteWriter) WriteMsg(*dns.Msg) error { return nil }

func TestNonUDPSourcePassesToHandler(t *testing.T) {
	next := newCountingHandler()
	l, _ := newTestLimit(1, 10)
	h := &limitHandler{limit: l, next: next}
	w := &remoteWriter{remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1}}
	for range 3 {
		h.ServeDNS(w, new(dns.Msg).SetQuestion(testZone, dns.TypeSOA))
	}
	next.waitCalls(t, 3)
}

func TestTCPHasNoUDPLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.UDPRate = 1
	l := startListeners(t, cfg, &fakeSource{})
	for i := range 10 {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
		r := exchange(t, "tcp", l.Addr(), m)
		if r.Truncated || r.Rcode != dns.RcodeSuccess {
			t.Fatalf("TCP query %d: tc=%v rcode=%d", i+1, r.Truncated, r.Rcode)
		}
	}
}

func TestUDPRateLimitsListenersUDP(t *testing.T) {
	cfg := testConfig(t)
	cfg.UDPRate = 1
	l := startListeners(t, cfg, &fakeSource{})
	var tc int
	for _, r := range burst(t, l.Addr(), 20, 300*time.Millisecond) {
		if r.Truncated {
			tc++
		}
	}
	if tc == 0 {
		t.Error("no truncated reply under a rate of 1 per second")
	}
}

func TestZeroUDPRateSetsNoLimit(t *testing.T) {
	l := startListeners(t, testConfig(t), &fakeSource{})
	replies := burst(t, l.Addr(), 20, 300*time.Millisecond)
	if len(replies) != 20 {
		t.Fatalf("replies: got %d, want 20", len(replies))
	}
	for _, r := range replies {
		if r.Truncated {
			t.Fatal("got a truncated reply with no limit set")
		}
	}
}
