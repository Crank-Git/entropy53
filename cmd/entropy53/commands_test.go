package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// fixedNow returns a clock that always reads t, so no test depends on the wall clock.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

var beforeAll = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func runCmd(now time.Time, args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = runWith(args, &o, &e, fixedNow(now))
	return code, o.String(), e.String()
}

func TestRoundAtPrintsPinnedRound(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "round-at", "quicknet", "2026-12-01T18:00:00Z")
	if code != 0 || out != "34448212\n" || errOut != "" {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestRoundAtResultIsWithinOnePeriod(t *testing.T) {
	times := []string{
		"2026-12-01T18:00:01Z",
		"2026-12-01T18:00:02Z",
		"2026-12-01T18:00:00.5Z",
		"2026-12-01T20:00:02+02:00",
	}
	c := chain.Quicknet
	for _, s := range times {
		code, out, _ := runCmd(beforeAll, "round-at", "quicknet", s)
		if code != 0 {
			t.Fatalf("%s: exit %d", s, code)
		}
		var n uint64
		for _, ch := range strings.TrimSpace(out) {
			n = n*10 + uint64(ch-'0')
		}
		in, _ := time.Parse(time.RFC3339, s)
		rt := c.RoundTime(n)
		if rt.Before(in) || rt.Sub(in) >= time.Duration(c.Period)*time.Second {
			t.Fatalf("%s: round %d has time %v", s, n, rt)
		}
	}
}

func TestRoundAtExactRoundTimeReturnsThatRound(t *testing.T) {
	code, out, _ := runCmd(beforeAll, "round-at", "quicknet", "2023-08-23T15:59:24Z")
	if code != 0 || out != "1000\n" {
		t.Fatalf("got code %d, out %q", code, out)
	}
}

func TestRoundAtPastTimeWritesWarning(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	code, out, errOut := runCmd(now, "round-at", "quicknet", "2026-12-01T18:00:00Z")
	if code != 0 || out != "34448212\n" {
		t.Fatalf("got code %d, out %q", code, out)
	}
	want := "warning: this time is in the past. A draw must pin a round with a future time.\n"
	if errOut != want {
		t.Fatalf("errOut %q, want %q", errOut, want)
	}
}

func TestRoundAtBeforeGenesisExitsTwo(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "round-at", "quicknet", "2020-01-01T00:00:00Z")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "error: ") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestRoundAtWithoutZoneOffsetExitsTwo(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "round-at", "quicknet", "2026-12-01T18:00:00")
	want := "error: the time \"2026-12-01T18:00:00\" is not RFC 3339 with a zone offset.\n"
	if code != 2 || out != "" || errOut != want {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestRoundAtUnknownChainListsKnownChains(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "round-at", "x", "2026-12-01T18:00:00Z")
	want := "error: unknown chain \"x\". Known chains: quicknet, default, evmnet.\n"
	if code != 2 || out != "" || errOut != want {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestRoundAtFlagAfterPositionalIsParsed(t *testing.T) {
	code, out, _ := runCmd(beforeAll, "round-at", "quicknet", "2026-12-01T18:00:00Z", "--bogus")
	if code != 2 || out != "" {
		t.Fatalf("got code %d, out %q", code, out)
	}
}

func TestRoundAtWrongArgumentCountExitsTwo(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "round-at", "quicknet")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "error: ") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestChainsPrintsRegistry(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "chains")
	if code != 0 || out != "quicknet\ndefault\nevmnet\n" || errOut != "" {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestNoCommandExitsTwo(t *testing.T) {
	code, out, errOut := runCmd(beforeAll)
	if code != 2 || out != "" || errOut == "" {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	code, out, errOut := runCmd(beforeAll, "bogus")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "error: ") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestSubcommandHelpExitsZero(t *testing.T) {
	for _, cmd := range []string{"round-at", "chains"} {
		code, out, errOut := runCmd(beforeAll, cmd, "--help")
		if code != 0 || out != "" || !strings.Contains(errOut, "Usage of entropy53 "+cmd) {
			t.Fatalf("%s: got code %d, out %q, errOut %q", cmd, code, out, errOut)
		}
	}
}

func TestResultsGoToStandardOutputAndErrorsToStandardError(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	_, out, errOut := runCmd(now, "round-at", "quicknet", "2026-12-01T18:00:00Z")
	if out != "34448212\n" || strings.Contains(out, "warning") || !strings.Contains(errOut, "warning") {
		t.Fatalf("out %q, errOut %q", out, errOut)
	}
	_, out, errOut = runCmd(now, "round-at", "x", "2026-12-01T18:00:00Z")
	if out != "" || errOut == "" {
		t.Fatalf("out %q, errOut %q", out, errOut)
	}
}

func TestRoundAtPastTimeWarningMakesNoFalseClaim(t *testing.T) {
	// The time is 1 second in the past, and the pinned round lies 13 seconds after it.
	now := time.Date(2026, 12, 1, 18, 0, 1, 0, time.UTC)
	code, out, errOut := runCmd(now, "round-at", "default", "2026-12-01T18:00:00Z")
	if code != 0 || out == "" || strings.Contains(errOut, "exists") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

// failWriter is a writer whose every write fails, like a closed pipe.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestFailedWriteToStandardOutputExitsOne(t *testing.T) {
	addr := okGateway(t)
	latestAddr := startGateway(t, vectorGateway{vectors: loadVectors(t), hasLatest: true})
	cases := map[string][]string{
		"chains":     {"chains"},
		"round-at":   {"round-at", "quicknet", "2026-12-01T18:00:00Z"},
		"get":        append([]string{"get", "quicknet", "1000"}, gatewayArgs(addr)...),
		"get --json": append([]string{"get", "quicknet", "1000", "--json"}, gatewayArgs(addr)...),
		"latest":     append([]string{"latest", "quicknet"}, gatewayArgs(latestAddr)...),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var e bytes.Buffer
			code := runWith(args, failWriter{}, &e, fixedNow(round1000))
			want := "error: cannot write to standard output (broken pipe).\n"
			if code != 1 || strings.Count(e.String(), "error: ") != 1 || !strings.HasSuffix(e.String(), want) {
				t.Fatalf("got code %d, errOut %q", code, e.String())
			}
		})
	}
}
