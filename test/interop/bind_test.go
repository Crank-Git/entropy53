//go:build interop

package interop

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// bindImage is BIND 9.20.24 on Ubuntu 26.04. The multi-arch digest covers arm64 and amd64.
	bindImage    = "ubuntu/bind9:9.20-26.04@sha256:7b7961ed04782d2f1ff15466af054df45b9bcb7c36e10a7387b83499eab50d9f"
	bindConfPath = "/etc/bind/named.conf"
)

// bindConfig returns a configuration with only the settings that the tests need.
// Every other option keeps its default.
// The grammar of server-addresses has no port, so the global port option sends the queries of the static-stub zone to the query counter.
// BIND listens on port 53 of the container, and listen-on keeps that port.
// The resolver accepts a client outside localnets, so allow-recursion is open.
// The zone is not signed and the name example has no secure delegation, so validate-except skips validation for the zone.
func bindConfig(s stub, settings []setting) (string, error) {
	for _, st := range settings {
		switch st {
		case settingQNAMEMinimization:
			// The default of qname-minimization is relaxed, so BIND minimizes without a setting.
		case settingMinNegativeTTL30, settingRandomCase:
			return "", fmt.Errorf("setting %d is not used with BIND", st)
		default:
			return "", fmt.Errorf("unknown setting %d", st)
		}
	}
	apex := strings.TrimSuffix(s.zone, ".")
	var b strings.Builder
	fmt.Fprintf(&b, "options {\n  directory \"/var/cache/bind\";\n  port %d;\n  listen-on port 53 { any; };\n", s.port)
	fmt.Fprintf(&b, "  allow-recursion { any; };\n  validate-except { %q; };\n};\n", apex)
	fmt.Fprintf(&b, "zone %q {\n  type static-stub;\n  server-addresses { %s; };\n};\n", apex, s.ip)
	return b.String(), nil
}

// startBIND runs BIND with a static-stub zone that points at the gateway, and returns the client address.
// The image entrypoint is Pebble, so the container runs named in the foreground instead.
func startBIND(t *testing.T, s stub, settings []setting) string {
	t.Helper()
	conf, err := bindConfig(s, settings)
	if err != nil {
		t.Fatalf("build the BIND configuration: %v", err)
	}
	port := freePort(t)
	name := startContainer(t, bindImage,
		[]string{"--entrypoint", "named", "-p", fmt.Sprintf("127.0.0.1:%d:53/udp", port), "-p", fmt.Sprintf("127.0.0.1:%d:53/tcp", port)},
		map[string]string{bindConfPath: conf}, "-g", "-c", bindConfPath)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	waitFor(t, "BIND to answer the zone apex", name, 30*time.Second, func() (bool, string) {
		m, err := dnsQuery("udp", addr, s.zone, dnsmessage.TypeSOA)
		if err != nil {
			return false, err.Error()
		}
		return m.RCode == dnsmessage.RCodeSuccess && len(m.Answers) == 1,
			fmt.Sprintf("rcode %v with %d answers", m.RCode, len(m.Answers))
	})
	return addr
}

func TestBINDPinnedRound(t *testing.T) {
	h := newHarness(t, resolverByName(t, "bind"))
	cl, c := newClient(t, h)
	t.Run("returns NODATA before the round time", func(t *testing.T) {
		if !h.gw.now().Before(c.RoundTime(testRound)) {
			t.Fatal("the gateway clock is not before the round time")
		}
		m, err := dnsQuery("udp", h.addr, roundName(), dnsmessage.TypeTXT)
		if err != nil {
			t.Fatal(err)
		}
		if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 0 || len(m.Authorities) != 1 {
			t.Errorf("rcode %v, %d answers, %d authority records, want NOERROR, 0 and 1 SOA",
				m.RCode, len(m.Answers), len(m.Authorities))
		}
	})
	t.Run("returns a record that verifies after the round time", func(t *testing.T) {
		awaitRound(t, h, cl, 250*time.Millisecond, 20*time.Second)
	})
	t.Run("sends no gateway query on the third query", func(t *testing.T) {
		before := h.counter.count()
		if before == 0 {
			t.Fatal("the gateway received no query, so the counter is not in the path")
		}
		if _, err := cl.Round(context.Background(), testRound); err != nil {
			t.Fatalf("Round: %v", err)
		}
		if after := h.counter.count(); after != before {
			t.Errorf("gateway queries rose from %d to %d on a cached answer: %+v", before, after, h.counter.exchanges())
		}
	})
}
