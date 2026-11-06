package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"syscall"
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

func realUDP(addr string) (net.PacketConn, error) { return net.ListenPacket("udp", addr) }

func TestListenRetriesWhenUDPPortIsTaken(t *testing.T) {
	var tried []string
	bindUDP := func(addr string) (net.PacketConn, error) {
		tried = append(tried, addr)
		if len(tried) == 1 {
			return nil, syscall.EADDRINUSE
		}
		return realUDP(addr)
	}
	tcp, udp, err := bindPair("127.0.0.1:0", bindUDP)
	if err != nil {
		t.Fatalf("bindPair: %v", err)
	}
	defer func() { _ = tcp.Close(); _ = udp.Close() }()
	if len(tried) != 2 {
		t.Fatalf("UDP bind attempts = %d, want 2", len(tried))
	}
	// The TCP socket of the first attempt must be closed, so its port binds again.
	again, err := net.Listen("tcp", tried[0])
	if err != nil {
		t.Errorf("TCP port of the first attempt is still bound: %v", err)
	} else {
		_ = again.Close()
	}
}

func TestListenFailsAfterBoundedRetries(t *testing.T) {
	attempts := 0
	bindUDP := func(string) (net.PacketConn, error) {
		attempts++
		return nil, syscall.EADDRINUSE
	}
	_, _, err := bindPair("127.0.0.1:0", bindUDP)
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Errorf("error = %v, want EADDRINUSE", err)
	}
	if attempts != maxBindAttempts {
		t.Errorf("attempts = %d, want %d", attempts, maxBindAttempts)
	}
}

func TestExplicitPortDoesNotRetry(t *testing.T) {
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer func() { _ = udp.Close() }()
	attempts := 0
	counted := func(addr string) (net.PacketConn, error) {
		attempts++
		return realUDP(addr)
	}
	cfg := testConfig(t)
	cfg.Listen = udp.LocalAddr().String()
	if _, _, err := bindPair(cfg.Listen, counted); !errors.Is(err, syscall.EADDRINUSE) {
		t.Errorf("error = %v, want EADDRINUSE", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if l, err := NewListeners(cfg, &fakeSource{}); err == nil {
		t.Errorf("NewListeners on a taken UDP port returned %v and no error", l)
	}
}
