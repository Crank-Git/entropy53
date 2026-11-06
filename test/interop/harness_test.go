//go:build interop

package interop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/internal/gateway"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	// zone is the zone of the gateway under test.
	zone = "beacon.example."
	// testBeacon and testRound select the vector that the tests read.
	testBeacon        = "quicknet"
	testRound  uint64 = 1000
	// queryTimeout limits one query to a resolver.
	queryTimeout = 5 * time.Second
)

// vector is one entry of testdata/vectors.json.
type vector struct {
	BeaconID          string `json:"beacon_id"`
	Round             uint64 `json:"round"`
	Signature         string `json:"signature"`
	PreviousSignature string `json:"previous_signature"`
	Randomness        string `json:"randomness"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	data, err := os.ReadFile("../../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read the vector file: %v", err)
	}
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse the vector file: %v", err)
	}
	return file.Vectors
}

// newFakeUpstream serves the real rounds of the vector file over HTTP in the drand v1 path layout.
// Every other path, including public/latest, returns HTTP 404. No test contacts a real upstream.
func newFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	rounds := make(map[string]vector)
	for _, v := range loadVectors(t) {
		c, err := chain.Lookup(v.BeaconID)
		if err != nil {
			t.Fatalf("look up chain %q: %v", v.BeaconID, err)
		}
		rounds["/"+c.ChainHash+"/public/"+strconv.FormatUint(v.Round, 10)] = v
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := rounds[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"round":              v.Round,
			"signature":          v.Signature,
			"previous_signature": v.PreviousSignature,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gatewayUnderTest is a real gateway that reads from the fake upstream.
// It starts no follower, so only the vector rounds reach the fake upstream.
type gatewayUnderTest struct {
	listeners *gateway.Listeners
	upstreams *gateway.Upstreams
	chain     chain.Chain
	// offset is the distance of the gateway clock from the real time. Only the test goroutine reads or sets it.
	offset time.Duration
}

// startGateway serves all registry chains on 127.0.0.1 until the test ends.
// The clock stands two minutes before the round time of the test vector.
func startGateway(t *testing.T) *gatewayUnderTest {
	t.Helper()
	fake := newFakeUpstream(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var chains []chain.Chain
	for _, id := range []string{"quicknet", "default", "evmnet"} {
		c, err := chain.Lookup(id)
		if err != nil {
			t.Fatalf("look up chain %q: %v", id, err)
		}
		chains = append(chains, c)
	}
	ups := gateway.NewUpstreams(gateway.Config{Upstreams: []string{fake.URL}, Rate: 10, Log: log})
	l, err := gateway.NewListeners(gateway.ZoneConfig{
		Zone: zone, Chains: chains,
		NS:       []string{"ns1.example.net."},
		SOARname: "hostmaster.example.net.",
		Listen:   "127.0.0.1:0",
		Log:      log,
	}, ups)
	if err != nil {
		t.Fatalf("start the gateway: %v", err)
	}
	g := &gatewayUnderTest{listeners: l, upstreams: ups, chain: chains[0]}
	g.setLead(2 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("gateway Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("gateway Run did not return within 10 seconds")
		}
	})
	return g
}

// setLead moves the gateway clock to lead before the round time of the test vector.
// A negative lead puts the clock after the round time.
func (g *gatewayUnderTest) setLead(lead time.Duration) {
	g.offset = time.Until(g.chain.RoundTime(testRound).Add(-lead))
	g.listeners.SetClockOffset(g.offset)
	g.upstreams.SetClockOffset(g.offset)
}

// now returns the time on the gateway clock.
func (g *gatewayUnderTest) now() time.Time { return time.Now().Add(g.offset) }

// resolver describes one resolver under test.
type resolver struct {
	name  string
	image string
	// start runs the container and returns the address "host:port" where a client queries it.
	// It returns after the resolver answers a query for the zone apex.
	start func(t *testing.T, s stub, settings []setting) string
}

// stub tells a resolver where the gateway of the zone is, as the container sees it.
type stub struct {
	zone string
	ip   string
	port int
}

// resolvers lists the resolvers that the tests run against.
var resolvers = []resolver{
	{name: "unbound", image: unboundImage, start: startUnbound},
}

// harnessConfig holds the options of newHarness.
type harnessConfig struct {
	lead     time.Duration
	settings []setting
}

// harnessOption changes the harness that newHarness builds.
type harnessOption func(*harnessConfig)

// withLead sets how long before the round time the gateway clock stands. A negative value puts it after the round time.
func withLead(d time.Duration) harnessOption {
	return func(c *harnessConfig) { c.lead = d }
}

// setting is a resolver option that the tests switch on. Each resolver start function maps it to its own configuration.
type setting int

const (
	// settingQNAMEMinimization turns QNAME minimization on.
	settingQNAMEMinimization setting = iota + 1
	// settingMinNegativeTTL30 sets the minimum cache time of a negative answer to 30 seconds.
	settingMinNegativeTTL30
	// settingRandomCase turns on the random letter case in queries to the gateway.
	settingRandomCase
)

// withSettings switches on resolver settings.
func withSettings(s ...setting) harnessOption {
	return func(c *harnessConfig) { c.settings = append(c.settings, s...) }
}

// harness is a gateway, a query counter and a resolver in a container.
type harness struct {
	gw      *gatewayUnderTest
	counter *counter
	// addr is where a client queries the resolver.
	addr string
}

// newHarness starts the gateway, the counter and the resolver r, and then sets the gateway clock.
// The default lead is 4 seconds before the round time.
// The clock moves after the resolver is ready, because a container takes seconds to start.
func newHarness(t *testing.T, r resolver, opts ...harnessOption) *harness {
	t.Helper()
	needDocker(t)
	cfg := harnessConfig{lead: 4 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	g := startGateway(t)
	hostIP, err := containerHostIP()
	if err != nil {
		t.Fatalf("read the host address of a container: %v", err)
	}
	bind, err := listenIP(hostIP)
	if err != nil {
		t.Fatal(err)
	}
	c := newCounter(t, bind, g.listeners.Addr())
	addr := r.start(t, stub{zone: zone, ip: hostIP, port: c.port}, cfg.settings)
	c.reset()
	g.setLead(cfg.lead)
	return &harness{gw: g, counter: c, addr: addr}
}

// roundName returns the name of the test round.
func roundName() string {
	return fmt.Sprintf("round.%d.%s.%s", testRound, testBeacon, zone)
}

// dnsQuery sends one TXT-style question to addr over network and returns the parsed answer.
// It sets the RD bit and sends no OPT record.
func dnsQuery(network, addr, name string, qtype dnsmessage.Type) (*dnsmessage.Message, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, fmt.Errorf("name %q: %w", name, err)
	}
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: uint16(rand.Uint32()), RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: n, Type: qtype, Class: dnsmessage.ClassINET}},
	}
	packed, err := q.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack query: %w", err)
	}
	conn, err := net.DialTimeout(network, addr, queryTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(queryTimeout)); err != nil {
		return nil, err
	}
	var raw []byte
	if network == "tcp" {
		if err := writeFrame(conn, packed); err != nil {
			return nil, err
		}
		raw, err = readFrame(conn)
	} else {
		if _, err = conn.Write(packed); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		var got int
		got, err = conn.Read(buf)
		raw = buf[:max(got, 0)]
	}
	if err != nil {
		return nil, err
	}
	var m dnsmessage.Message
	if err := m.Unpack(raw); err != nil {
		return nil, fmt.Errorf("unpack answer: %w", err)
	}
	return &m, nil
}

// hasTXT reports whether the answer section of m holds a TXT record.
func hasTXT(m *dnsmessage.Message) bool {
	for _, a := range m.Answers {
		if a.Header.Type == dnsmessage.TypeTXT {
			return true
		}
	}
	return false
}

func TestFakeUpstreamServesOnlyVectors(t *testing.T) {
	srv := newFakeUpstream(t)
	c, err := chain.Lookup(testBeacon)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, body := get("/" + c.ChainHash + "/public/1000")
	if status != http.StatusOK || !strings.Contains(body, `"round":1000`) {
		t.Errorf("vector round: status %d, body %q, want 200 with round 1000", status, body)
	}
	for _, path := range []string{"/" + c.ChainHash + "/public/1001", "/" + c.ChainHash + "/public/latest", "/" + c.ChainHash + "/public/0"} {
		if status, _ := get(path); status != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, status)
		}
	}
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Errorf("fake upstream URL %q is not on the loopback address", srv.URL)
	}
}
