package record_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
)

var chains = []struct {
	c    chain.Chain
	size int
}{
	{chain.Quicknet, 57},
	{chain.Evmnet, 73},
	{chain.Default, 201},
}

// fill returns n bytes that all equal v.
func fill(n int, v byte) []byte { return bytes.Repeat([]byte{v}, n) }

func bodyFor(c chain.Chain, number uint64) record.Body {
	b := record.Body{Version: 1, Number: number, Sig: fill(c.SigLen, 0xaa)}
	if c.PrevSigLen > 0 {
		b.PrevSig = fill(c.PrevSigLen, 0xbb)
	}
	return b
}

func TestEncodeWritesLayout(t *testing.T) {
	for _, tc := range chains {
		t.Run(tc.c.BeaconID, func(t *testing.T) {
			raw, err := record.Encode(tc.c, bodyFor(tc.c, 0x0102030405))
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(raw) != tc.size {
				t.Fatalf("length = %d, want %d", len(raw), tc.size)
			}
			if raw[0] != 0x01 {
				t.Fatalf("version byte = %#x, want 0x01", raw[0])
			}
			if got := binary.BigEndian.Uint64(raw[1:9]); got != 0x0102030405 {
				t.Fatalf("round number = %#x", got)
			}
			if !bytes.Equal(raw[9:9+tc.c.SigLen], fill(tc.c.SigLen, 0xaa)) {
				t.Fatal("signature is not at offset 9")
			}
			if !bytes.Equal(raw[9+tc.c.SigLen:], fill(tc.c.PrevSigLen, 0xbb)) {
				t.Fatal("previous signature is not at offset 9+L")
			}
		})
	}
}

func TestDecodeReturnsAllFields(t *testing.T) {
	for _, tc := range chains {
		t.Run(tc.c.BeaconID, func(t *testing.T) {
			want := bodyFor(tc.c, 1000)
			raw, err := record.Encode(tc.c, want)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			got, err := record.Decode(tc.c, raw)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Version != 1 || got.Number != 1000 ||
				!bytes.Equal(got.Sig, want.Sig) || !bytes.Equal(got.PrevSig, want.PrevSig) {
				t.Fatalf("Decode = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDecodeReturnsEmptyPrevSigOnUnchainedChain(t *testing.T) {
	for _, c := range []chain.Chain{chain.Quicknet, chain.Evmnet} {
		raw, err := record.Encode(c, bodyFor(c, 5))
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := record.Decode(c, raw)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(got.PrevSig) != 0 {
			t.Fatalf("%s: PrevSig length = %d, want 0", c.BeaconID, len(got.PrevSig))
		}
	}
}

func validRaw(t *testing.T, c chain.Chain, number uint64) []byte {
	t.Helper()
	raw, err := record.Encode(c, bodyFor(c, number))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return raw
}

func TestDecodeRejectsWrongVersion(t *testing.T) {
	for _, tc := range chains {
		for _, v := range []byte{0x00, 0x02, 0xff} {
			raw := validRaw(t, tc.c, 7)
			raw[0] = v
			if _, err := record.Decode(tc.c, raw); err == nil {
				t.Fatalf("%s: version %#x accepted", tc.c.BeaconID, v)
			}
		}
	}
}

func TestDecodeRejectsWrongLength(t *testing.T) {
	for _, tc := range chains {
		t.Run(tc.c.BeaconID, func(t *testing.T) {
			if _, err := record.Decode(tc.c, nil); err == nil {
				t.Fatal("empty body accepted")
			}
			raw := validRaw(t, tc.c, 7)
			if _, err := record.Decode(tc.c, raw[:len(raw)-1]); err == nil {
				t.Fatal("body one byte short accepted")
			}
			for _, other := range chains {
				if other.c.BeaconID == tc.c.BeaconID {
					continue
				}
				if _, err := record.Decode(tc.c, validRaw(t, other.c, 7)); err == nil {
					t.Fatalf("body of chain %s accepted", other.c.BeaconID)
				}
			}
		})
	}
}

func TestDecodeRejectsTrailingBytes(t *testing.T) {
	for _, tc := range chains {
		raw := append(validRaw(t, tc.c, 7), 0x00)
		if _, err := record.Decode(tc.c, raw); err == nil {
			t.Fatalf("%s: trailing byte accepted", tc.c.BeaconID)
		}
	}
}

func TestDecodeRejectsRoundNumberOutsideRange(t *testing.T) {
	for _, tc := range chains {
		for _, n := range []uint64{0, chain.MaxRound + 1, math.MaxUint64} {
			raw := validRaw(t, tc.c, 1)
			binary.BigEndian.PutUint64(raw[1:9], n)
			if _, err := record.Decode(tc.c, raw); err == nil {
				t.Fatalf("%s: round number %d accepted", tc.c.BeaconID, n)
			}
		}
	}
}

func TestDecodeAcceptsRoundNumberBounds(t *testing.T) {
	for _, tc := range chains {
		for _, n := range []uint64{1, chain.MaxRound} {
			got, err := record.Decode(tc.c, validRaw(t, tc.c, n))
			if err != nil || got.Number != n {
				t.Fatalf("%s: round %d: got %d, err %v", tc.c.BeaconID, n, got.Number, err)
			}
		}
	}
}

func TestEncodeRejectsInvalidBody(t *testing.T) {
	for _, tc := range chains {
		c := tc.c
		bad := map[string]func(b *record.Body){
			"version":       func(b *record.Body) { b.Version = 2 },
			"short sig":     func(b *record.Body) { b.Sig = b.Sig[:len(b.Sig)-1] },
			"long sig":      func(b *record.Body) { b.Sig = append(b.Sig, 0) },
			"prev sig":      func(b *record.Body) { b.PrevSig = fill(c.PrevSigLen+1, 0xbb) },
			"round zero":    func(b *record.Body) { b.Number = 0 },
			"round too big": func(b *record.Body) { b.Number = chain.MaxRound + 1 },
		}
		for name, mutate := range bad {
			t.Run(c.BeaconID+"/"+name, func(t *testing.T) {
				b := bodyFor(c, 9)
				mutate(&b)
				if _, err := record.Encode(c, b); err == nil {
					t.Fatal("invalid body accepted")
				}
			})
		}
	}
}
