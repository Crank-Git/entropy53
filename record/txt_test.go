package record_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
)

// encoded returns the Encode output of a synthetic body of chain c.
func encoded(t *testing.T, c chain.Chain) []byte {
	t.Helper()
	raw, err := record.Encode(c, bodyFor(c, 1000))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return raw
}

func lengths(strs []string) []int {
	n := make([]int, len(strs))
	for i, s := range strs {
		n[i] = len(s)
	}
	return n
}

func TestToStringsUsesStandardPaddedBase64(t *testing.T) {
	raw := encoded(t, chain.Evmnet)
	got := strings.Join(record.ToStrings(raw), "")
	if want := base64.StdEncoding.EncodeToString(raw); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, "==") {
		t.Fatalf("text %q has no == padding", got)
	}
}

func TestToStringsSplitsAt255Octets(t *testing.T) {
	long := record.ToStrings(encoded(t, chain.Default))
	if len(long) != 2 || len(long[0]) != 255 || len(long[1]) != 13 {
		t.Fatalf("default string lengths = %v, want [255 13]", lengths(long))
	}
	short := record.ToStrings(encoded(t, chain.Quicknet))
	if len(short) != 1 || len(short[0]) != 76 {
		t.Fatalf("quicknet string lengths = %v, want [76]", lengths(short))
	}
}

func TestFromStringsConcatenatesAllStrings(t *testing.T) {
	for _, tc := range chains {
		raw := encoded(t, tc.c)
		got, err := record.FromStrings(record.ToStrings(raw))
		if err != nil {
			t.Fatalf("%s: FromStrings: %v", tc.c.BeaconID, err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("%s: round trip changed the body", tc.c.BeaconID)
		}
	}
}

func TestFromStringsAcceptsAnySplitPosition(t *testing.T) {
	raw := encoded(t, chain.Default)
	text := base64.StdEncoding.EncodeToString(raw)
	for i := 0; i <= len(text); i++ {
		got, err := record.FromStrings([]string{text[:i], text[i:]})
		if err != nil {
			t.Fatalf("split at %d: %v", i, err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("split at %d changed the body", i)
		}
	}
}

func TestFromStringsIgnoresZeroLengthStrings(t *testing.T) {
	raw := encoded(t, chain.Quicknet)
	text := base64.StdEncoding.EncodeToString(raw)
	got, err := record.FromStrings([]string{"", text[:10], "", "", text[10:], ""})
	if err != nil {
		t.Fatalf("FromStrings: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("zero-length strings changed the body")
	}
}

func TestFromStringsRejectsWhitespace(t *testing.T) {
	text := base64.StdEncoding.EncodeToString(encoded(t, chain.Quicknet))
	for _, ws := range []string{" ", "\t", "\r", "\n"} {
		for name, s := range map[string]string{
			"start":  ws + text,
			"middle": text[:20] + ws + text[20:],
			"end":    text + ws,
		} {
			if _, err := record.FromStrings([]string{s}); err == nil {
				t.Errorf("accepted %q at the %s", ws, name)
			}
		}
	}
}

func TestFromStringsRejectsMissingPadding(t *testing.T) {
	text := base64.StdEncoding.EncodeToString(encoded(t, chain.Evmnet))
	if _, err := record.FromStrings([]string{strings.TrimRight(text, "=")}); err == nil {
		t.Fatal("accepted text without padding")
	}
}

func TestFromStringsRejectsNonZeroTrailingBits(t *testing.T) {
	if _, err := record.FromStrings([]string{"QR=="}); err == nil {
		t.Fatal("accepted non-zero trailing bits")
	}
}
