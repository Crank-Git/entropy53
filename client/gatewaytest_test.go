package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/internal/gateway"
)

// testVector is one real round from testdata/vectors.json.
type testVector struct {
	chain      chain.Chain
	number     uint64
	sig        []byte
	prevSig    []byte
	txt        []string
	randomness [32]byte
}

// loadVectors returns every real vector from testdata/vectors.json.
func loadVectors(t *testing.T) []testVector {
	t.Helper()
	raw, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var f struct {
		Vectors []struct {
			BeaconID          string   `json:"beacon_id"`
			Round             uint64   `json:"round"`
			Signature         string   `json:"signature"`
			PreviousSignature string   `json:"previous_signature"`
			TXT               []string `json:"txt"`
			Randomness        string   `json:"randomness"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	var out []testVector
	for _, v := range f.Vectors {
		c, err := chain.Lookup(v.BeaconID)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", v.BeaconID, err)
		}
		tv := testVector{chain: c, number: v.Round, txt: v.TXT}
		if tv.sig, err = hex.DecodeString(v.Signature); err != nil {
			t.Fatalf("signature of %s: %v", v.BeaconID, err)
		}
		if tv.prevSig, err = hex.DecodeString(v.PreviousSignature); err != nil {
			t.Fatalf("previous signature of %s: %v", v.BeaconID, err)
		}
		rnd, err := hex.DecodeString(v.Randomness)
		if err != nil || len(rnd) != 32 {
			t.Fatalf("randomness of %s: %v", v.BeaconID, err)
		}
		copy(tv.randomness[:], rnd)
		out = append(out, tv)
	}
	return out
}

// sourceFunc is a gateway.Source that answers from a function. It never has a latest round.
type sourceFunc func(c chain.Chain, n uint64) (chain.Round, gateway.Status)

func (f sourceFunc) Round(_ context.Context, c chain.Chain, n uint64) (chain.Round, gateway.Status) {
	return f(c, n)
}

func (sourceFunc) Latest(chain.Chain) (chain.Round, bool) { return chain.Round{}, false }

// vectorSource serves the real vectors and reports every other round as not published.
// edit changes a round before the gateway encodes it. A nil edit changes nothing.
func vectorSource(vectors []testVector, edit func(*chain.Round)) sourceFunc {
	return func(c chain.Chain, n uint64) (chain.Round, gateway.Status) {
		for _, v := range vectors {
			if v.chain.BeaconID == c.BeaconID && v.number == n {
				r := chain.Round{Number: n, Sig: append([]byte(nil), v.sig...), PrevSig: append([]byte(nil), v.prevSig...)}
				if edit != nil {
					edit(&r)
				}
				return r, gateway.Found
			}
		}
		return chain.Round{}, gateway.NotPublished
	}
}

// startGateway runs an in-process gateway for testZone and returns its address.
// It stops the gateway at cleanup and fails the test when the stop takes longer than testWait.
func startGateway(t *testing.T, src gateway.Source, chains ...chain.Chain) string {
	t.Helper()
	l, err := gateway.NewListeners(gateway.ZoneConfig{
		Zone:     testZone,
		Chains:   chains,
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

// newTestClient returns a Client for c that queries resolver. A nil clock selects the real clock.
func newTestClient(t *testing.T, c chain.Chain, resolver string, clock func() time.Time) *Client {
	t.Helper()
	cl, err := New(Config{Zone: testZone, Chain: c, Resolver: resolver, Timeout: testWait, Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cl
}

// fixedClock returns a clock that always reads at.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}
