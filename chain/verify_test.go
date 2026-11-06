package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyRejectsRoundNumberBounds(t *testing.T) {
	// These are the real signatures of round 1000 on each chain.
	// A forged signature fails at every round, so only a real one shows that the round number is the cause.
	cases := []struct {
		chain    *Chain
		sig, prv string
	}{
		{&Quicknet, "b44679b9a59af2ec876b1a6b1ad52ea9b1615fc3982b19576350f93447cb1125e342b73a8dd2bacbe47e4b6b63ed5e39", ""},
		{&Default, "99bf96de133c3d3937293cfca10c8152b18ab2d034ccecf115658db324d2edc00a16a2044cd04a8a38e2a307e5ecff3511315be8d282079faf24098f283e0ed2c199663b334d2e84c55c032fe469b212c5c2087ebb83a5b25155c3283f5b79ac", "af0d93299a363735fe847f5ea241442c65843dc1bd3a7b79646b3b10072e908bf034d35cd69d378e3341f139100cd4cd03030399864ef8803a5a4f5e64fccc20bbae36d1ca22a6ddc43d2630c41105e90598fab11e5c7456df3925d4b577b113"},
		{&Evmnet, "06fd5996329504d3a56b482d9222bf7205857d0a9559ddd216ca31a286f6a8cc0a120f021aac2f13553fb164f62bc3a5ca32c76dea88a777b39bcf3cac5fdbd6", ""},
	}
	for _, tc := range cases {
		c := tc.chain
		sig, err := hex.DecodeString(tc.sig)
		if err != nil {
			t.Fatal(err)
		}
		prev, err := hex.DecodeString(tc.prv)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Verify(1000, sig, prev); err != nil {
			t.Fatalf("%s: Verify rejected round 1000: %v", c.BeaconID, err)
		}
		for _, n := range []uint64{0, MaxRound + 1} {
			if err := c.Verify(n, sig, prev); err == nil {
				t.Errorf("%s: Verify accepted round %d", c.BeaconID, n)
			}
		}
	}
}

func TestVerifyRejectsEmptySignature(t *testing.T) {
	for _, c := range allChains {
		if err := c.Verify(1000, nil, make([]byte, c.PrevSigLen)); err == nil {
			t.Errorf("%s: Verify accepted an empty signature", c.BeaconID)
		}
	}
}

func TestVerifyRejectsShortSignature(t *testing.T) {
	for _, c := range allChains {
		if err := c.Verify(1000, make([]byte, c.SigLen-1), make([]byte, c.PrevSigLen)); err == nil {
			t.Errorf("%s: Verify accepted a short signature", c.BeaconID)
		}
	}
}

func TestVerifyRejectsUnknownScheme(t *testing.T) {
	c := Quicknet
	c.SchemeID = "no-such-scheme"
	if err := c.Verify(1000, make([]byte, c.SigLen), nil); err == nil {
		t.Error("Verify accepted an unknown scheme")
	}
}

func TestRandomnessEqualsSHA256OfSignature(t *testing.T) {
	sig := []byte("any signature bytes")
	if got, want := Randomness(sig), sha256.Sum256(sig); got != want {
		t.Errorf("Randomness = %x, want %x", got, want)
	}
}
