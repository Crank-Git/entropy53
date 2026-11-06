package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// This file builds DNS packets by hand, because cmd/ must not import miekg/dns.

// dnsQuery returns a DNS query packet with one question of class IN.
func dnsQuery(id uint16, name string, qtype uint16) []byte {
	p := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for label := range strings.SplitSeq(strings.TrimSuffix(name, "."), ".") {
		p = append(p, byte(len(label)))
		p = append(p, label...)
	}
	return append(p, 0, byte(qtype>>8), byte(qtype), 0, 1)
}

// listenAddr returns the address in the listening line of out.
func listenAddr(t *testing.T, out *lineBuffer) string {
	t.Helper()
	for _, line := range strings.Split(out.String(), "\n") {
		var entry struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "gateway listening" {
			return entry.Addr
		}
	}
	t.Fatalf("no listening line in %q", out.String())
	return ""
}

// stopGateway sends SIGTERM and requires exit code 0.
func stopGateway(t *testing.T, done <-chan int) {
	t.Helper()
	// run installs its handler before it logs the listening line, so this signal cannot end the test process.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if code := waitCode(t, done); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRunAppliesUDPRate(t *testing.T) {
	url, _ := quietUpstream(t)
	out, done := startGateway(append(gatewayArgs(url), "--udp-rate", "1"))
	waitListening(t, out, done)
	conn, err := net.Dial("udp", listenAddr(t, out))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	for id := range uint16(3) {
		if _, err := conn.Write(dnsQuery(id, "beacon.example.", 6)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	truncated := false
	deadline := time.Now().Add(waitLimit)
	buf := make([]byte, 512)
	for !truncated {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		// Byte 2 of a DNS header holds the TC bit as 0x02.
		truncated = n >= 12 && buf[2]&0x02 != 0
	}
	if !truncated {
		t.Error("no answer to 3 quick queries had the TC bit with --udp-rate 1")
	}
	stopGateway(t, done)
}

func TestRunAppliesUpstreamRate(t *testing.T) {
	var onDemand atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/public/latest") {
			onDemand.Add(1)
		}
		http.Error(w, "no round", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	out, done := startGateway(append(gatewayArgs(srv.URL), "--upstream-rate", "1"))
	waitListening(t, out, done)
	conn, err := net.Dial("udp", listenAddr(t, out))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 512)
	for id, n := range []string{"1000", "1001", "1002", "1003", "1004"} {
		if _, err := conn.Write(dnsQuery(uint16(id), "round."+n+".quicknet.beacon.example.", 16)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(waitLimit)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("no answer to the query for round %s within %s: %v", n, waitLimit, err)
		}
	}
	got := onDemand.Load()
	if got < 1 || got > 2 {
		t.Errorf("the upstream got %d on-demand requests for 5 rounds with --upstream-rate 1, want 1 or 2", got)
	}
	stopGateway(t, done)
}
