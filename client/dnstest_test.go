package client

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// testZone is the zone of every client test.
const testZone = "beacon.example."

// testWait bounds each wait in the test helpers, so that a hang fails with a message.
const testWait = 5 * time.Second

// seenQuery is one query that a test resolver received.
type seenQuery struct {
	network string // "udp" or "tcp".
	msg     *dns.Msg
}

// testResolver is a resolver on a loopback port that the OS assigns.
// It serves UDP and TCP on the same port number.
type testResolver struct {
	addr    string
	queried chan struct{} // Receives one value for each query. A full channel drops values.

	mu   sync.Mutex
	seen []seenQuery
}

// queries returns a copy of the queries received so far.
func (r *testResolver) queries() []seenQuery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenQuery(nil), r.seen...)
}

// count returns the number of queries received over network.
func (r *testResolver) count(network string) int {
	n := 0
	for _, q := range r.queries() {
		if q.network == network {
			n++
		}
	}
	return n
}

// waitQuery blocks until the resolver receives a query. It fails the test after testWait.
func (r *testResolver) waitQuery(t *testing.T) {
	t.Helper()
	select {
	case <-r.queried:
	case <-time.After(testWait):
		t.Errorf("the test resolver received no query within %s", testWait)
	}
}

// startResolver serves answer on UDP and TCP and stops the servers at cleanup.
// A nil result from answer sends nothing. An answer function that blocks must unblock before cleanup.
func startResolver(t *testing.T, answer func(network string, req *dns.Msg) *dns.Msg) *testResolver {
	t.Helper()
	return startRawResolver(t, func(network string, w dns.ResponseWriter, req *dns.Msg) {
		if resp := answer(network, req); resp != nil {
			_ = w.WriteMsg(resp)
		}
	})
}

// startRawResolver is startResolver with a handler that writes to w itself.
// A test uses it to send a packet that a Msg cannot encode, such as a cut packet.
func startRawResolver(t *testing.T, serve func(network string, w dns.ResponseWriter, req *dns.Msg)) *testResolver {
	t.Helper()
	l, pc := listenPair(t)
	r := &testResolver{addr: l.Addr().String(), queried: make(chan struct{}, 16)}
	h := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		network := "tcp"
		if _, ok := w.RemoteAddr().(*net.UDPAddr); ok {
			network = "udp"
		}
		r.mu.Lock()
		r.seen = append(r.seen, seenQuery{network: network, msg: req.Copy()})
		r.mu.Unlock()
		select {
		case r.queried <- struct{}{}:
		default:
		}
		serve(network, w, req)
	})
	startedUDP, startedTCP := make(chan struct{}), make(chan struct{})
	servers := []*dns.Server{
		{PacketConn: pc, Handler: h, NotifyStartedFunc: func() { close(startedUDP) }},
		{Listener: l, Handler: h, NotifyStartedFunc: func() { close(startedTCP) }},
	}
	done := make(chan struct{}, len(servers))
	for _, srv := range servers {
		go func() {
			defer func() { done <- struct{}{} }()
			if err := srv.ActivateAndServe(); err != nil {
				t.Errorf("ActivateAndServe: %v", err)
			}
		}()
	}
	for _, started := range []chan struct{}{startedUDP, startedTCP} {
		select {
		case <-started:
		case <-time.After(testWait):
			t.Fatalf("the test resolver did not start within %s", testWait)
		}
	}
	t.Cleanup(func() {
		for _, srv := range servers {
			ctx, cancel := context.WithTimeout(context.Background(), testWait)
			if err := srv.ShutdownContext(ctx); err != nil {
				t.Errorf("ShutdownContext: %v", err)
			}
			cancel()
		}
		for range servers {
			select {
			case <-done:
			case <-time.After(testWait):
				t.Errorf("a test resolver server did not stop within %s", testWait)
			}
		}
	})
	return r
}

// listenPair binds a TCP listener and a UDP socket on the same loopback port.
// The OS assigns the TCP port, so the UDP bind can fail when another process holds the port. It retries.
func listenPair(t *testing.T) (net.Listener, net.PacketConn) {
	t.Helper()
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		pc, err := net.ListenPacket("udp", l.Addr().String())
		if err == nil {
			return l, pc
		}
		_ = l.Close()
	}
	t.Fatalf("no loopback port is free for both TCP and UDP")
	return nil, nil
}

// vectorTXT returns the TXT strings of the real vector for beaconID from testdata/vectors.json.
func vectorTXT(t *testing.T, beaconID string) []string {
	t.Helper()
	raw, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var f struct {
		Vectors []struct {
			BeaconID string   `json:"beacon_id"`
			TXT      []string `json:"txt"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	for _, v := range f.Vectors {
		if v.BeaconID == beaconID {
			return v.TXT
		}
	}
	t.Fatalf("no vector for %q", beaconID)
	return nil
}

// txtReply returns a NOERROR reply to req with one TXT record that holds strs.
func txtReply(req *dns.Msg, strs []string) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Answer = append(resp.Answer, &dns.TXT{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
		Txt: strs,
	})
	return resp
}

// rcodeReply returns a reply to req with the given RCODE and no record.
func rcodeReply(req *dns.Msg, rcode int) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = rcode
	return resp
}
