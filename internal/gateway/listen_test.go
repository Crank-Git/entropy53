package gateway

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestRunListensOnUDPAndTCPOnOnePort(t *testing.T) {
	l := startListeners(t, testConfig(t), &fakeSource{})
	for _, network := range []string{"udp", "tcp"} {
		m := new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)
		r := exchange(t, network, l.Addr(), m)
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
			t.Errorf("%s: rcode = %s, answers = %d, want NOERROR and 1", network, dns.RcodeToString[r.Rcode], len(r.Answer))
		}
	}
}

func TestTCPAnswerEqualsUDPAnswer(t *testing.T) {
	l := startListeners(t, testConfig(t), &fakeSource{})
	summary := func(network string, name string, qtype uint16) string {
		r := exchange(t, network, l.Addr(), new(dns.Msg).SetQuestion(name, qtype))
		return fmt.Sprint(r.Rcode, r.Authoritative, r.RecursionAvailable, r.Truncated, r.Answer, r.Ns, r.Extra)
	}
	for _, q := range []struct {
		name  string
		qtype uint16
	}{{testZone, dns.TypeSOA}, {testZone, dns.TypeNS}, {"foo." + testZone, dns.TypeTXT}, {"quicknet." + testZone, dns.TypeA}} {
		if udp, tcp := summary("udp", q.name, q.qtype), summary("tcp", q.name, q.qtype); udp != tcp {
			t.Errorf("%s type %d: UDP answer %q differs from TCP answer %q", q.name, q.qtype, udp, tcp)
		}
	}
}

func TestRunStopsWhenContextEnds(t *testing.T) {
	l, err := NewListeners(testConfig(t), &fakeSource{})
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	if r := exchange(t, "tcp", l.Addr(), new(dns.Msg).SetQuestion(testZone, dns.TypeSOA)); r.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[r.Rcode])
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(dnsWait):
		t.Fatalf("Run did not return within %s after the context ended", dnsWait)
	}

	if c, err := net.DialTimeout("tcp", l.Addr(), dnsWait); err == nil {
		_ = c.Close()
		t.Error("the TCP socket still accepts connections after Run returned")
	}
	deadline := time.Now().Add(dnsWait)
	for serverGoroutines() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d DNS server goroutines still run %s after Run returned", serverGoroutines(), dnsWait)
		}
		runtime.Gosched()
	}
}

// serverGoroutines counts the goroutines that run code of dns.Server.
func serverGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "miekg/dns.(*Server)") {
			n++
		}
	}
	return n
}

func TestNewListenersFailsWhenPortIsBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = busy.Close() }()
	cfg := testConfig(t)
	cfg.Listen = busy.Addr().String()
	if l, err := NewListeners(cfg, &fakeSource{}); err == nil {
		t.Errorf("NewListeners on a busy port returned %v and no error", l)
	}
}
