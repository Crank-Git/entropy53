package draft

import (
	"strings"
	"testing"
)

// The example in the Base64 Text section is a folded copy of the quicknet record of round 1000.
func TestBase64ExampleEqualsQuicknetVector(t *testing.T) {
	_, rest, ok := strings.Cut(section(t, "Record Body"), "\n~~~\n")
	if !ok {
		t.Fatal("the Record Body section holds no example block")
	}
	block, _, ok := strings.Cut(rest, "\n~~~\n")
	if !ok {
		t.Fatal("the example block has no closing fence")
	}
	var got string
	for _, line := range strings.Split(block, "\n") {
		got += strings.TrimSpace(line)
	}
	for _, v := range readFileVectors(t) {
		if v.BeaconID == "quicknet" && (len(v.TXT) != 1 || got != v.TXT[0]) {
			t.Errorf("example = %q, want the quicknet vector %q", got, v.TXT)
		}
	}
}

func TestAnswerRulesStateBADVERSAndAAExceptions(t *testing.T) {
	requireText(t, section(t, "Answer Rules"),
		"MUST return the extended RCODE BADVERS",
		"an OPT record of version 0 and no answer record",
		"MUST clear the AA bit on every other response",
		"The only exception is the TC response")
}

func TestAnswerRulesStateClockBeforeGenesis(t *testing.T) {
	requireText(t, section(t, "Answer Rules"),
		"If the clock of the gateway is before the genesis time of the chain",
		"MUST then return SERVFAIL")
}

func TestChainParametersRequireKeyOfSelectedChain(t *testing.T) {
	requireText(t, section(t, "Chain Parameters"),
		"MUST verify a round with the public key of the chain that it selected",
		"from a source that it trusts")
}
