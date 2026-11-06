package chain

import (
	"crypto/sha256"
	"testing"
)

func TestVerifyRejectsRoundNumberBounds(t *testing.T) {
	for _, c := range allChains {
		sig := make([]byte, c.SigLen)
		prev := make([]byte, c.PrevSigLen)
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
