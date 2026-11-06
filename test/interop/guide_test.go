//go:build interop

package interop

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// guideLimit bounds each wait in the guide test, so that a hang fails with a message.
const guideLimit = 30 * time.Second

// guideHeading is the heading of the guide section that holds the loopback commands.
const guideHeading = "## First read on loopback"

// loopbackCommands returns the gateway, dig and get command lines of the loopback section of the guide.
// Each command must occur once, so that the test runs exactly what a reader runs.
func loopbackCommands(t *testing.T) (gatewayLine, digLine, getLine string) {
	t.Helper()
	data, err := os.ReadFile("../../docs/operator-guide.md")
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}
	_, section, found := strings.Cut(string(data), guideHeading+"\n")
	if !found {
		t.Fatalf("the guide has no %q section", guideHeading)
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	fenced := regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	counts := map[string]int{}
	for _, block := range fenced.FindAllStringSubmatch(section, -1) {
		for line := range strings.SplitSeq(block[1], "\n") {
			switch {
			case strings.HasPrefix(line, "entropy53d "):
				gatewayLine = line
				counts["entropy53d"]++
			case strings.HasPrefix(line, "dig "):
				digLine = line
				counts["dig"]++
			case strings.HasPrefix(line, "entropy53 get "):
				getLine = line
				counts["entropy53 get"]++
			}
		}
	}
	for _, name := range []string{"entropy53d", "dig", "entropy53 get"} {
		if counts[name] != 1 {
			t.Fatalf("the loopback section holds %d %q command lines, want 1", counts[name], name)
		}
	}
	return gatewayLine, digLine, getLine
}

// buildBinary builds the command at pkg into dir and returns the path of the binary.
func buildBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", path, "../../cmd/"+name).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return path
}

// guideArgs returns the arguments of a command line of the guide with the guide port replaced by port.
func guideArgs(line, port string) []string {
	return strings.Fields(strings.ReplaceAll(line, "15353", port))
}

// guideGateway is a running gateway process.
type guideGateway struct {
	cmd *exec.Cmd
	// exited closes after exitErr holds the result of Wait.
	exited  chan struct{}
	exitErr error
}

// startGuideGateway starts the binary with args and waits for the listening line.
func startGuideGateway(t *testing.T, bin string, args []string) *guideGateway {
	t.Helper()
	g := &guideGateway{cmd: exec.Command(bin, args...), exited: make(chan struct{})}
	stdout, err := g.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.cmd.Start(); err != nil {
		t.Fatalf("start the gateway: %v", err)
	}
	listening := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		signalled := false
		for scanner.Scan() {
			if !signalled && strings.Contains(scanner.Text(), "gateway listening") {
				signalled = true
				close(listening)
			}
		}
		// Wait closes the pipe, so it runs after the scanner reads to the end of the output.
		g.exitErr = g.cmd.Wait()
		close(g.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-g.exited:
		default:
			_ = g.cmd.Process.Kill()
			<-g.exited
		}
	})
	select {
	case <-listening:
	case <-g.exited:
		t.Fatalf("the gateway exited before it listened: %v", g.exitErr)
	case <-time.After(guideLimit):
		t.Fatalf("no listening line within %s", guideLimit)
	}
	return g
}

// stop sends SIGTERM and requires exit code 0.
func (g *guideGateway) stop(t *testing.T) {
	t.Helper()
	if err := g.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case <-g.exited:
		if g.exitErr != nil {
			t.Errorf("the gateway did not exit with code 0 after SIGTERM: %v", g.exitErr)
		}
	case <-time.After(guideLimit):
		t.Fatalf("the gateway did not stop within %s of SIGTERM", guideLimit)
	}
}

// runGuideDig runs the dig line of the guide and requires a TXT record in the answer.
// A missing dig fails the test in CI. On a developer machine, the test logs it and skips only this step.
func runGuideDig(t *testing.T, line, port string) {
	t.Helper()
	if _, err := exec.LookPath("dig"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("dig is not installed, and CI requires it: %v", err)
		}
		t.Logf("dig is not installed, so the test skips the dig command: %v", err)
		return
	}
	args := guideArgs(line, port)
	out, err := exec.Command(args[0], append(args[1:], "+short", "+time=5", "+tries=1")...).CombinedOutput()
	if err != nil {
		t.Fatalf("dig: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Errorf("dig printed no TXT record")
	}
}

func TestOperatorGuideLoopbackReachesVerifiedRead(t *testing.T) {
	gatewayLine, digLine, getLine := loopbackCommands(t)
	dir := t.TempDir()
	gatewayBin := buildBinary(t, dir, "entropy53d")
	cliBin := buildBinary(t, dir, "entropy53")
	fake := newFakeUpstream(t)
	port := strconv.Itoa(freePort(t))

	// The guide command reads the public relays. The test adds --upstream so that no real upstream receives a request.
	gw := startGuideGateway(t, gatewayBin, append(guideArgs(gatewayLine, port)[1:], "--upstream", fake.URL))
	runGuideDig(t, digLine, port)

	ctx, cancel := context.WithTimeout(context.Background(), guideLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, cliBin, guideArgs(getLine, port)[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("entropy53 get: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "verified:   yes") {
		t.Errorf("get output does not hold %q:\n%s", "verified:   yes", out)
	}
	for _, v := range loadVectors(t) {
		if v.BeaconID == testBeacon && v.Round == testRound && !strings.Contains(string(out), v.Randomness) {
			t.Errorf("get output does not hold the vector randomness %s:\n%s", v.Randomness, out)
		}
	}
	gw.stop(t)
}
