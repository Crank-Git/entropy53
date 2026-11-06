package gateway

import (
	"testing"

	"github.com/Crank-Git/entropy53/chain"
)

func TestParseRoundLabel(t *testing.T) {
	cases := []struct {
		label string
		want  uint64
		ok    bool
	}{
		{"1", 1, true},
		{"1000", 1000, true},
		{"9007199254740992", chain.MaxRound, true},
		{"9007199254740993", 0, false},
		{"0", 0, false},
		{"01000", 0, false},
		{"", 0, false},
		{"+5", 0, false},
		{"-5", 0, false},
		{"1e3", 0, false},
		{"0x10", 0, false},
		{"1_000", 0, false},
		{"१२", 0, false},
		{"18446744073709551616", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseRoundLabel(tc.label)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseRoundLabel(%q) = %d, %v; want %d, %v", tc.label, got, ok, tc.want, tc.ok)
		}
	}
}

func FuzzParseName(f *testing.F) {
	quicknet, err := chain.Lookup("quicknet")
	if err != nil {
		f.Fatalf("Lookup(quicknet): %v", err)
	}
	chains := []chain.Chain{quicknet}
	for _, seed := range []string{
		"beacon.example.",
		"quicknet.beacon.example.",
		"1000.quicknet.beacon.example.",
		"round.1000.quicknet.beacon.example.",
		"ROUND.1000.QuickNet.Beacon.Example.",
		"latest.quicknet.beacon.example.",
		"round.01000.quicknet.beacon.example.",
		"round.0.quicknet.beacon.example.",
		"round.9007199254740993.quicknet.beacon.example.",
		"round.18446744073709551616.quicknet.beacon.example.",
		"round.1000.unknown.beacon.example.",
		"round.1000.quicknet.xbeacon.example.",
		"a\\.b.quicknet.beacon.example.",
		"\\049.quicknet.beacon.example.",
		".",
		"",
		"..",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, qname string) {
		p := parseName("beacon.example.", chains, qname)
		switch p.kind {
		case nameRound:
			if p.round < 1 || p.round > chain.MaxRound {
				t.Errorf("round %d is outside 1..MaxRound for %q", p.round, qname)
			}
			fallthrough
		case nameLatest, nameChain, nameRoundParent:
			if p.chain.BeaconID != "quicknet" {
				t.Errorf("chain %q is not configured for %q", p.chain.BeaconID, qname)
			}
		case nameNone, nameOutside, nameApex:
		default:
			t.Errorf("kind %d is unknown for %q", p.kind, qname)
		}
	})
}
