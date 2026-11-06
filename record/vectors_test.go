package record_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
)

// vector is one entry of testdata/vectors.json. The schema is in testdata/README.md.
type vector struct {
	BeaconID          string   `json:"beacon_id"`
	Round             uint64   `json:"round"`
	Signature         string   `json:"signature"`
	PreviousSignature string   `json:"previous_signature"`
	TXT               []string `json:"txt"`
	Randomness        string   `json:"randomness"`
}

// loadVectors reads the vector file. Later tests in this package reuse it.
func loadVectors(t *testing.T) []vector {
	t.Helper()
	data, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vector file: %v", err)
	}
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse vector file: %v", err)
	}
	return file.Vectors
}

// vectorsOf returns the vectors of one chain and fails if there are none.
func vectorsOf(t *testing.T, id string) (chain.Chain, []vector) {
	t.Helper()
	c, err := chain.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []vector
	for _, v := range loadVectors(t) {
		if v.BeaconID == id {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no vector for %s", id)
	}
	return c, out
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hexadecimal %q: %v", s, err)
	}
	return b
}

// expectedBytes builds the body layout by hand: version, round, signature, previous signature.
func expectedBytes(t *testing.T, v vector) []byte {
	t.Helper()
	raw := []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := 0; i < 8; i++ {
		raw[8-i] = byte(v.Round >> (8 * i))
	}
	raw = append(raw, mustHex(t, v.Signature)...)
	return append(raw, mustHex(t, v.PreviousSignature)...)
}

func TestVectorFileHoldsEachChain(t *testing.T) {
	for _, id := range []string{"quicknet", "default", "evmnet"} {
		vectorsOf(t, id)
	}
}

func TestVectorFieldsAreComplete(t *testing.T) {
	for _, v := range loadVectors(t) {
		c, err := chain.Lookup(v.BeaconID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Round < 1 {
			t.Errorf("%s: round %d", v.BeaconID, v.Round)
		}
		if got := len(mustHex(t, v.Signature)); got != c.SigLen {
			t.Errorf("%s: signature length %d, want %d", v.BeaconID, got, c.SigLen)
		}
		if got := len(mustHex(t, v.PreviousSignature)); got != c.PrevSigLen {
			t.Errorf("%s: previous signature length %d, want %d", v.BeaconID, got, c.PrevSigLen)
		}
		if got := len(mustHex(t, v.Randomness)); got != 32 {
			t.Errorf("%s: randomness length %d, want 32", v.BeaconID, got)
		}
		if len(v.TXT) == 0 {
			t.Errorf("%s: no TXT strings", v.BeaconID)
		}
	}
}

func TestVectorEncodeEqualsExpectedBytes(t *testing.T) {
	for _, v := range loadVectors(t) {
		c, _ := chain.Lookup(v.BeaconID)
		got, err := record.Encode(c, record.Body{
			Version: 1,
			Number:  v.Round,
			Sig:     mustHex(t, v.Signature),
			PrevSig: mustHex(t, v.PreviousSignature),
		})
		if err != nil {
			t.Fatalf("%s: Encode: %v", v.BeaconID, err)
		}
		if want := expectedBytes(t, v); !bytes.Equal(got, want) {
			t.Errorf("%s: Encode = %x, want %x", v.BeaconID, got, want)
		}
	}
}

func TestVectorToStringsEqualsExpectedStrings(t *testing.T) {
	for _, v := range loadVectors(t) {
		got := record.ToStrings(expectedBytes(t, v))
		if !slices.Equal(got, v.TXT) {
			t.Errorf("%s: ToStrings = %q, want %q", v.BeaconID, got, v.TXT)
		}
	}
}

func TestQuicknetVectorIsOneStringOf76Characters(t *testing.T) {
	_, vs := vectorsOf(t, "quicknet")
	for _, v := range vs {
		if got := lengths(record.ToStrings(expectedBytes(t, v))); !slices.Equal(got, []int{76}) {
			t.Errorf("string lengths = %v, want [76]", got)
		}
	}
}

func TestEvmnetVectorIsOneStringOf100Characters(t *testing.T) {
	_, vs := vectorsOf(t, "evmnet")
	for _, v := range vs {
		if got := lengths(record.ToStrings(expectedBytes(t, v))); !slices.Equal(got, []int{100}) {
			t.Errorf("string lengths = %v, want [100]", got)
		}
	}
}

func TestDefaultVectorIsTwoStringsOf268Characters(t *testing.T) {
	_, vs := vectorsOf(t, "default")
	for _, v := range vs {
		got := record.ToStrings(expectedBytes(t, v))
		if len(got) != 2 || len(got[0])+len(got[1]) != 268 {
			t.Errorf("string lengths = %v, want two strings totaling 268", lengths(got))
		}
	}
}

func TestDefaultVectorFromStringsAtAnySplit(t *testing.T) {
	c, vs := vectorsOf(t, "default")
	for _, v := range vs {
		text := strings.Join(v.TXT, "")
		want, err := record.Decode(c, expectedBytes(t, v))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i <= len(text); i++ {
			raw, err := record.FromStrings([]string{text[:i], text[i:]})
			if err != nil {
				t.Fatalf("split %d: FromStrings: %v", i, err)
			}
			got, err := record.Decode(c, raw)
			if err != nil {
				t.Fatalf("split %d: Decode: %v", i, err)
			}
			if !bytes.Equal(got.Sig, want.Sig) || !bytes.Equal(got.PrevSig, want.PrevSig) || got.Number != want.Number {
				t.Fatalf("split %d: body differs", i)
			}
		}
	}
}

func TestQuicknetRound1000String(t *testing.T) {
	const want = "AQAAAAAAAAPotEZ5uaWa8uyHaxprGtUuqbFhX8OYKxlXY1D5NEfLESXjQrc6jdK6y+R+S2tj7V45"
	_, vs := vectorsOf(t, "quicknet")
	for _, v := range vs {
		if v.Round == 1000 {
			if got := record.ToStrings(expectedBytes(t, v)); !slices.Equal(got, []string{want}) {
				t.Fatalf("strings = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatal("no quicknet vector for round 1000")
}
