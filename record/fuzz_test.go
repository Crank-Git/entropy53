package record_test

import (
	"testing"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
)

// FuzzFromStringsDecode checks that hostile TXT strings never panic the
// decoder, whatever the split position.
func FuzzFromStringsDecode(f *testing.F) {
	all := []chain.Chain{chain.Quicknet, chain.Evmnet, chain.Default}
	for _, c := range all {
		raw, err := record.Encode(c, bodyFor(c, 1000))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(record.ToStrings(raw)[0], 5)
	}
	f.Add("QR==", 2)
	f.Fuzz(func(t *testing.T, text string, split int) {
		if split < 0 || split > len(text) {
			split = len(text) / 2
		}
		raw, err := record.FromStrings([]string{text[:split], text[split:]})
		if err != nil {
			return
		}
		for _, c := range all {
			_, _ = record.Decode(c, raw)
		}
	})
}
