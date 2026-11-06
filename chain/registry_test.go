package chain

import (
	"encoding/hex"
	"testing"
)

func TestRegistryValuesEqualEvidence(t *testing.T) {
	want := []struct {
		c          Chain
		beaconID   string
		hash       string
		key        string
		scheme     string
		genesis    int64
		period     int64
		sigLen     int
		prevSigLen int
	}{
		{Quicknet, "quicknet", "52db9ba70e0cc0f6eaf7803dd07447a1f5477735fd3f661792ba94600c84e971",
			"83cf0f2896adee7eb8b5f01fcad3912212c437e0073e911fb90022d3e760183c8c4b450b6a0a6c3ac6a5776a2d1064510d1fec758c921cc22b0e17e63aaf4bcb5ed66304de9cf809bd274ca73bab4af5a6e9c76a4bc09e76eae8991ef5ece45a",
			"bls-unchained-g1-rfc9380", 1692803367, 3, 48, 0},
		{Default, "default", "8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce",
			"868f005eb8e6e4ca0a47c8a77ceaa5309a47978a7c71bc5cce96366b5d7a569937c529eeda66c7293784a9402801af31",
			"pedersen-bls-chained", 1595431050, 30, 96, 96},
		{Evmnet, "evmnet", "04f1e9062b8a81f848fded9c12306733282b2727ecced50032187751166ec8c3",
			"07e1d1d335df83fa98462005690372c643340060d205306a9aa8106b6bd0b3820557ec32c2ad488e4d4f6008f89a346f18492092ccc0d594610de2732c8b808f0095685ae3a85ba243747b1b2f426049010f6b73a0cf1d389351d5aaaa1047f6297d3a4f9749b33eb2d904c9d9ebf17224150ddd7abd7567a9bec6c74480ee0b",
			"bls-bn254-unchained-on-g1", 1727521075, 3, 64, 0},
	}
	for _, w := range want {
		t.Run(w.beaconID, func(t *testing.T) {
			c := w.c
			if c.BeaconID != w.beaconID || c.ChainHash != w.hash || c.SchemeID != w.scheme ||
				c.Genesis != w.genesis || c.Period != w.period ||
				c.SigLen != w.sigLen || c.PrevSigLen != w.prevSigLen {
				t.Fatalf("registry entry differs from evidence: %+v", c)
			}
			if got := hex.EncodeToString(c.PublicKey); got != w.key {
				t.Fatalf("public key = %s, want %s", got, w.key)
			}
		})
	}
}

func TestLookupReturnsKnownChain(t *testing.T) {
	for _, want := range []Chain{Quicknet, Default, Evmnet} {
		got, err := Lookup(want.BeaconID)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", want.BeaconID, err)
		}
		if got.ChainHash != want.ChainHash {
			t.Fatalf("Lookup(%q) returned chain %q", want.BeaconID, got.BeaconID)
		}
	}
}

func TestLookupRejectsUnknownBeaconID(t *testing.T) {
	for _, id := range []string{"", "unknown", "Quicknet"} {
		if _, err := Lookup(id); err == nil {
			t.Fatalf("Lookup(%q) returned no error", id)
		}
	}
}

func TestLookupReturnsAnOwnCopyOfThePublicKey(t *testing.T) {
	got, err := Lookup("quicknet")
	if err != nil {
		t.Fatal(err)
	}
	want := got.PublicKey[0]
	got.PublicKey[0] ^= 0xff
	again, err := Lookup("quicknet")
	if err != nil {
		t.Fatal(err)
	}
	if again.PublicKey[0] != want {
		t.Fatal("a change to a returned key changed the registry")
	}
}
