package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/client"
	"github.com/Crank-Git/entropy53/internal/gateway"
)

const (
	// testWait bounds the stop of a test gateway, so that a hang fails with a message.
	testWait = 5 * time.Second
	// quicknetOut is the text output of get on the quicknet vector, as the spec shows it.
	quicknetOut = "chain:      quicknet\n" +
		"round:      1000\n" +
		"time:       2023-08-23T15:59:24Z\n" +
		"randomness: fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff3cd603d930e54fdd\n" +
		"signature:  b44679b9a59af2ec876b1a6b1ad52ea9b1615fc3982b19576350f93447cb1125e342b73a8dd2bacbe47e4b6b63ed5e39\n" +
		"verified:   yes\n"
	quicknetJSON = `{"chain":"quicknet","round":1000,"time":"2023-08-23T15:59:24Z","randomness":"fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff3cd603d930e54fdd","signature":"b44679b9a59af2ec876b1a6b1ad52ea9b1615fc3982b19576350f93447cb1125e342b73a8dd2bacbe47e4b6b63ed5e39","previous_signature":"","verified":true}` + "\n"
)

// vector is one real round 1000 from testdata/vectors.json.
type vector struct {
	chain   chain.Chain
	sig     []byte
	prevSig []byte
}

func loadVectors(t *testing.T) map[string]vector {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var f struct {
		Vectors []struct {
			BeaconID          string `json:"beacon_id"`
			Signature         string `json:"signature"`
			PreviousSignature string `json:"previous_signature"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	out := map[string]vector{}
	for _, v := range f.Vectors {
		c, err := chain.Lookup(v.BeaconID)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", v.BeaconID, err)
		}
		sig, err := hex.DecodeString(v.Signature)
		if err != nil {
			t.Fatalf("signature: %v", err)
		}
		prev, err := hex.DecodeString(v.PreviousSignature)
		if err != nil {
			t.Fatalf("previous signature: %v", err)
		}
		out[v.BeaconID] = vector{chain: c, sig: sig, prevSig: prev}
	}
	return out
}

// vectorGateway is a gateway.Source that serves round 1000 of each vector chain.
// Every other round gets the status other. The latest round of a chain is round 1000 when hasLatest is true.
type vectorGateway struct {
	vectors   map[string]vector
	edit      func(*chain.Round)
	other     gateway.Status
	hasLatest bool
}

func (g vectorGateway) round(c chain.Chain) (chain.Round, bool) {
	v, ok := g.vectors[c.BeaconID]
	if !ok {
		return chain.Round{}, false
	}
	r := chain.Round{Number: 1000, Sig: append([]byte(nil), v.sig...), PrevSig: append([]byte(nil), v.prevSig...)}
	if g.edit != nil {
		g.edit(&r)
	}
	return r, true
}

func (g vectorGateway) Round(_ context.Context, c chain.Chain, n uint64) (chain.Round, gateway.Status) {
	if r, ok := g.round(c); ok && n == 1000 {
		return r, gateway.Found
	}
	return chain.Round{}, g.other
}

func (g vectorGateway) Latest(c chain.Chain) (chain.Round, bool) {
	if !g.hasLatest {
		return chain.Round{}, false
	}
	return g.round(c)
}

// startGateway runs an in-process gateway for the zone beacon.example on a loopback port and returns its address.
func startGateway(t *testing.T, src gateway.Source) string {
	t.Helper()
	l, err := gateway.NewListeners(gateway.ZoneConfig{
		Zone:     "beacon.example.",
		Chains:   []chain.Chain{chain.Quicknet, chain.Default, chain.Evmnet},
		NS:       []string{"ns.example.net."},
		SOARname: "hostmaster.example.net.",
		Listen:   "127.0.0.1:0",
	}, src)
	if err != nil {
		t.Fatalf("NewListeners: %v", err)
	}
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
		case <-time.After(testWait):
			t.Errorf("the gateway did not stop within %s", testWait)
		}
	})
	return l.Addr()
}

// gatewayArgs returns the flags that point a command at a test gateway.
func gatewayArgs(addr string) []string {
	return []string{"--zone", "beacon.example", "--resolver", addr}
}

func okGateway(t *testing.T) string {
	t.Helper()
	return startGateway(t, vectorGateway{vectors: loadVectors(t), other: gateway.NotPublished})
}

// round1000 is the round time of round 1000 of quicknet.
var round1000 = chain.Quicknet.RoundTime(1000)

func TestGetPrintsVectorRandomness(t *testing.T) {
	addr := okGateway(t)
	args := append([]string{"get", "quicknet", "1000"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(round1000, args...)
	if code != 0 || out != quicknetOut || errOut != "" {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestPinnedGetTwiceIsByteIdentical(t *testing.T) {
	addr := okGateway(t)
	for _, extra := range [][]string{nil, {"--json"}} {
		args := append(append([]string{"get", "quicknet", "1000"}, gatewayArgs(addr)...), extra...)
		_, first, _ := runCmd(round1000, args...)
		_, second, _ := runCmd(round1000.Add(time.Hour), args...)
		if first == "" || first != second {
			t.Fatalf("flags %v: first %q, second %q", extra, first, second)
		}
	}
}

func TestRoundTextUsesUTC(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("test", 5*3600)
	t.Cleanup(func() { time.Local = old })
	v := loadVectors(t)["quicknet"]
	r := client.Round{Chain: "quicknet", Number: 1000, Signature: v.sig}
	var out bytes.Buffer
	writeRound(&out, v.chain, r, false, "")
	if !strings.Contains(out.String(), "time:       2023-08-23T15:59:24Z\n") {
		t.Fatalf("output %q", out.String())
	}
}

func TestGetJSONIsExactAndVerified(t *testing.T) {
	addr := okGateway(t)
	args := append([]string{"get", "quicknet", "1000", "--json"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(round1000, args...)
	if code != 0 || out != quicknetJSON || errOut != "" {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil || m["verified"] != true {
		t.Fatalf("parse: %v, verified %v", err, m["verified"])
	}
}

func TestDefaultJSONHasPreviousSignature(t *testing.T) {
	addr := okGateway(t)
	args := append([]string{"get", "default", "1000", "--json"}, gatewayArgs(addr)...)
	code, out, _ := runCmd(chain.Default.RoundTime(1000), args...)
	var m struct {
		Prev string `json:"previous_signature"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil || code != 0 {
		t.Fatalf("code %d, out %q, err %v", code, out, err)
	}
	want := hex.EncodeToString(loadVectors(t)["default"].prevSig)
	if want == "" || m.Prev != want {
		t.Fatalf("previous_signature %q, want %q", m.Prev, want)
	}
}

func TestGetExitCodes(t *testing.T) {
	vectors := loadVectors(t)
	flip := func(r *chain.Round) { r.Sig[0] ^= 0xff }
	grace := 11 * time.Second
	t1001 := chain.Quicknet.RoundTime(1001)
	notPub := vectorGateway{vectors: vectors, other: gateway.NotPublished}
	cases := []struct {
		name   string
		src    vectorGateway
		now    time.Time
		round  string
		noZone bool
		want   int
	}{
		{"verified", notPub, round1000, "1000", false, 0},
		{"no zone", notPub, round1000, "1000", true, 2},
		{"round zero", notPub, round1000, "0", false, 2},
		{"not published", notPub, t1001.Add(-time.Second), "1001", false, 3},
		{"withheld", notPub, t1001.Add(grace), "1001", false, 4},
		{"invalid", vectorGateway{vectors: vectors, edit: flip, other: gateway.NotPublished}, round1000, "1000", false, 5},
		{"transport", vectorGateway{vectors: vectors, other: gateway.Unavailable}, round1000, "1001", false, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := startGateway(t, tc.src)
			args := []string{"get", "quicknet", tc.round}
			if tc.noZone {
				args = append(args, "--resolver", addr)
			} else {
				args = append(args, gatewayArgs(addr)...)
			}
			code, _, _ := runCmd(tc.now, args...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d", code, tc.want)
			}
		})
	}
}

func TestLatestStaleExitsSixAndPrintsRound(t *testing.T) {
	addr := startGateway(t, vectorGateway{vectors: loadVectors(t), hasLatest: true})
	args := append([]string{"latest", "quicknet"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(chain.Quicknet.RoundTime(1003), args...)
	if code != 6 || out != quicknetOut {
		t.Fatalf("got code %d, out %q", code, out)
	}
	if !strings.Contains(errOut, "error: latest round 1000 is stale.\n") {
		t.Fatalf("errOut %q", errOut)
	}
}

func TestLatestWithheldExitsFour(t *testing.T) {
	addr := startGateway(t, vectorGateway{vectors: loadVectors(t)})
	args := append([]string{"latest", "quicknet"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(round1000, args...)
	if code != 4 || out != "" || !strings.HasSuffix(errOut, "error: the latest round is withheld.\n") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestLatestWritesWarningToStandardError(t *testing.T) {
	addr := startGateway(t, vectorGateway{vectors: loadVectors(t), hasLatest: true})
	args := append([]string{"latest", "quicknet"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(chain.Quicknet.RoundTime(1002), args...)
	if code != 0 || out != quicknetOut {
		t.Fatalf("got code %d, out %q", code, out)
	}
	want := "warning: latest is not for a draw. Use get with a pinned round number.\n"
	if errOut != want {
		t.Fatalf("errOut %q, want %q", errOut, want)
	}
}

func TestNotPublishedErrorText(t *testing.T) {
	addr := okGateway(t)
	args := append([]string{"get", "quicknet", "99999999"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(round1000, args...)
	want := "error: round 99999999 is not published. Its round time is 2033-02-23T20:29:21Z.\n"
	if code != 3 || out != "" || errOut != want {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestJSONErrorUsesSameExitCode(t *testing.T) {
	addr := okGateway(t)
	args := append([]string{"get", "quicknet", "99999999", "--json"}, gatewayArgs(addr)...)
	code, out, errOut := runCmd(round1000, args...)
	want := `{"error":"round 99999999 is not published. Its round time is 2033-02-23T20:29:21Z."}` + "\n"
	if code != 3 || out != want || !strings.HasPrefix(errOut, "error: round 99999999") {
		t.Fatalf("got code %d, out %q, errOut %q", code, out, errOut)
	}
}

func TestFlagsBeforeAndAfterPositionals(t *testing.T) {
	addr := okGateway(t)
	_, after, _ := runCmd(round1000, append([]string{"get", "quicknet", "1000"}, gatewayArgs(addr)...)...)
	_, before, _ := runCmd(round1000, append(append([]string{"get"}, gatewayArgs(addr)...), "quicknet", "1000")...)
	if after != quicknetOut || before != after {
		t.Fatalf("after %q, before %q", after, before)
	}
}
