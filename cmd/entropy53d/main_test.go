package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestVersionFlagPrintsNonEmptyVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("--version printed an empty string")
	}
}

func TestVersionFlagPrintsDevWithoutBuildVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "dev" {
		t.Fatalf("--version printed %q, want %q", got, "dev")
	}
}

func TestHelpPrintsUsageOnceAndExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got := strings.Count(errOut.String(), "Usage of"); got != 1 {
		t.Fatalf("usage printed %d times, want 1: %q", got, errOut.String())
	}
	if strings.Contains(errOut.String(), "help requested") {
		t.Fatalf("output contains the help-requested line: %q", errOut.String())
	}
}

func TestBadFlagPrintsErrorOnceAndExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if got := strings.Count(errOut.String(), "flag provided but not defined"); got != 1 {
		t.Fatalf("error printed %d times, want 1: %q", got, errOut.String())
	}
}

// waitLimit bounds each wait in these tests, so that a hang fails with a message.
const waitLimit = 10 * time.Second

// lineBuffer is an output target that is safe for concurrent writes.
// It closes ready when the output first holds the marker.
type lineBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	marker string
	ready  chan struct{}
	once   sync.Once
}

func newLineBuffer(marker string) *lineBuffer {
	return &lineBuffer{marker: marker, ready: make(chan struct{})}
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if strings.Contains(b.buf.String(), b.marker) {
		b.once.Do(func() { close(b.ready) })
	}
	return n, err
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lines counts the non-empty lines of the output.
func (b *lineBuffer) lines() int {
	n := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// quietUpstream returns the base URL of an upstream that answers 500 and signals each request for public/latest.
func quietUpstream(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	latest := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/public/latest") {
			select {
			case latest <- struct{}{}:
			default:
			}
		}
		http.Error(w, "no round", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, latest
}

// gatewayArgs returns the arguments of a gateway that listens on a free port and serves quicknet.
func gatewayArgs(upstream string) []string {
	return []string{
		"--zone", "beacon.example.",
		"--ns", "ns1.example.net.",
		"--soa-rname", "hostmaster.example.net.",
		"--listen", "127.0.0.1:0",
		"--chains", "quicknet",
		"--upstream", upstream,
	}
}

// without returns args with the flag name and its value removed.
func without(args []string, name string) []string {
	i := slices.Index(args, name)
	return slices.Concat(args[:i], args[i+2:])
}

// startGateway runs the gateway in a goroutine. It returns the output and the channel of the exit code.
func startGateway(args []string) (*lineBuffer, chan int) {
	out := newLineBuffer("gateway listening")
	done := make(chan int, 1)
	go func() {
		var errOut bytes.Buffer
		done <- run(args, out, &errOut)
	}()
	return out, done
}

func waitCode(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(waitLimit):
		t.Fatalf("run did not return within %s", waitLimit)
		return -1
	}
}

// waitListening waits for the listening line.
func waitListening(t *testing.T, out *lineBuffer, done <-chan int) {
	t.Helper()
	select {
	case <-out.ready:
	case code := <-done:
		t.Fatalf("run returned %d before it listened: %q", code, out.String())
	case <-time.After(waitLimit):
		t.Fatalf("no listening line within %s", waitLimit)
	}
}

func TestInZoneNSFailsStartup(t *testing.T) {
	url, _ := quietUpstream(t)
	for _, ns := range []string{"NS1.BEACON.EXAMPLE", "beacon.example."} {
		args := append(without(gatewayArgs(url), "--ns"), "--ns", ns)
		out, done := startGateway(args)
		if code := waitCode(t, done); code != 1 {
			t.Errorf("--ns %s: exit code = %d, want 1", ns, code)
		}
		if n := out.lines(); n != 1 {
			t.Errorf("--ns %s: %d log lines, want 1: %q", ns, n, out.String())
		}
	}
}

func TestMissingRequiredFlagExitsOneWithOneLogLine(t *testing.T) {
	url, _ := quietUpstream(t)
	for _, name := range []string{"--zone", "--ns", "--soa-rname"} {
		out, done := startGateway(without(gatewayArgs(url), name))
		if code := waitCode(t, done); code != 1 {
			t.Errorf("without %s: exit code = %d, want 1", name, code)
		}
		if n := out.lines(); n != 1 {
			t.Errorf("without %s: %d log lines, want 1: %q", name, n, out.String())
		}
	}
}

func TestUnknownChainFailsStartup(t *testing.T) {
	url, _ := quietUpstream(t)
	args := append(without(gatewayArgs(url), "--chains"), "--chains", "quicknet,nosuchchain")
	out, done := startGateway(args)
	if code := waitCode(t, done); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "nosuchchain") {
		t.Errorf("log %q does not name the unknown chain", out.String())
	}
}

func TestUDPRateBelowOneFailsStartup(t *testing.T) {
	url, _ := quietUpstream(t)
	out, done := startGateway(append(gatewayArgs(url), "--udp-rate", "0"))
	if code := waitCode(t, done); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if n := out.lines(); n != 1 {
		t.Errorf("%d log lines, want 1: %q", n, out.String())
	}
}

func TestBindFailureExitsOne(t *testing.T) {
	url, _ := quietUpstream(t)
	args := append(without(gatewayArgs(url), "--listen"), "--listen", "256.0.0.1:0")
	out, done := startGateway(args)
	if code := waitCode(t, done); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if n := out.lines(); n != 1 {
		t.Errorf("%d log lines, want 1: %q", n, out.String())
	}
}

func TestParseFlagsAppliesDefaults(t *testing.T) {
	var errOut bytes.Buffer
	s, err := parseFlags(nil, &errOut)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(s.upstream.Upstreams) != 4 {
		t.Errorf("upstreams = %v, want 4", s.upstream.Upstreams)
	}
	if len(s.zone.Chains) != 3 {
		t.Errorf("chains = %d, want 3", len(s.zone.Chains))
	}
	if s.zone.Listen != ":53" {
		t.Errorf("listen = %q, want %q", s.zone.Listen, ":53")
	}
	if s.zone.UDPRate != 2000 {
		t.Errorf("udp rate = %d, want 2000", s.zone.UDPRate)
	}
	if s.upstream.Rate != 20 {
		t.Errorf("upstream rate = %d, want 20", s.upstream.Rate)
	}
}

func TestUpstreamRateReachesConfig(t *testing.T) {
	var errOut bytes.Buffer
	s, err := parseFlags([]string{"--upstream-rate", "7", "--udp-rate", "9"}, &errOut)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if s.upstream.Rate != 7 || s.zone.UDPRate != 9 {
		t.Errorf("rates = %d, %d, want 7, 9", s.upstream.Rate, s.zone.UDPRate)
	}
}

func TestRepeatedNSAndUpstreamFlagsKeepOrder(t *testing.T) {
	var errOut bytes.Buffer
	args := []string{"--ns", "b.example.net.", "--upstream", "http://one", "--ns", "a.example.net.", "--upstream", "http://two"}
	s, err := parseFlags(args, &errOut)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if want := []string{"b.example.net.", "a.example.net."}; !slices.Equal(s.zone.NS, want) {
		t.Errorf("NS = %v, want %v", s.zone.NS, want)
	}
	if want := []string{"http://one", "http://two"}; !slices.Equal(s.upstream.Upstreams, want) {
		t.Errorf("upstreams = %v, want %v", s.upstream.Upstreams, want)
	}
}

func TestSIGTERMExitsZeroWithin5Seconds(t *testing.T) {
	url, _ := quietUpstream(t)
	out, done := startGateway(gatewayArgs(url))
	waitListening(t, out, done)

	start := time.Now()
	// run installs its handler before it logs the listening line, so this signal cannot end the test process.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("shutdown took %s, want at most 5s", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return within 5 seconds of SIGTERM")
	}
}

func TestRunStartsFollower(t *testing.T) {
	url, latest := quietUpstream(t)
	out, done := startGateway(gatewayArgs(url))
	waitListening(t, out, done)
	select {
	case <-latest:
	case <-time.After(5 * time.Second):
		t.Error("no public/latest request reached the upstream within 5 seconds")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if code := waitCode(t, done); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}
