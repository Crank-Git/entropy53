package gateway

import (
	"strconv"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// nameKind is the shape of a name in the name table.
type nameKind int

const (
	// nameNone is a name that the name table does not list. It returns NXDOMAIN.
	nameNone nameKind = iota
	// nameOutside is a name outside the zone. It returns REFUSED.
	nameOutside
	// nameApex is the zone apex.
	nameApex
	// nameChain is <chain>.
	nameChain
	// nameRoundParent is <n>.<chain>, an empty non-terminal.
	nameRoundParent
	// nameRound is round.<n>.<chain>.
	nameRound
	// nameLatest is latest.<chain>.
	nameLatest
)

// parsedName is the result of parseName.
type parsedName struct {
	kind  nameKind
	chain chain.Chain
	round uint64
}

// parseName classifies qname against the name table.
// The zone must be in canonical form. A name outside the zone is nameOutside.
// A name inside the zone that the table does not list is nameNone.
func parseName(zone string, chains []chain.Chain, qname string) parsedName {
	qname = dns.CanonicalName(qname)
	if !dns.IsSubDomain(zone, qname) {
		return parsedName{kind: nameOutside}
	}
	// SplitDomainName needs a syntactically valid name.
	if _, ok := dns.IsDomainName(qname); !ok {
		return parsedName{kind: nameNone}
	}
	labels := dns.SplitDomainName(qname)
	labels = labels[:len(labels)-len(dns.SplitDomainName(zone))]
	switch len(labels) {
	case 0:
		return parsedName{kind: nameApex}
	case 1:
		if c, ok := findChain(chains, labels[0]); ok {
			return parsedName{kind: nameChain, chain: c}
		}
	case 2:
		c, ok := findChain(chains, labels[1])
		if !ok {
			break
		}
		if labels[0] == "latest" {
			return parsedName{kind: nameLatest, chain: c}
		}
		if n, ok := parseRoundLabel(labels[0]); ok {
			return parsedName{kind: nameRoundParent, chain: c, round: n}
		}
	case 3:
		c, ok := findChain(chains, labels[2])
		if !ok || labels[0] != "round" {
			break
		}
		if n, ok := parseRoundLabel(labels[1]); ok {
			return parsedName{kind: nameRound, chain: c, round: n}
		}
	}
	return parsedName{kind: nameNone}
}

// findChain returns the configured chain whose beacon ID is label.
// The caller passes a lowercase label, because parseName canonicalizes the name first.
func findChain(chains []chain.Chain, label string) (chain.Chain, bool) {
	for _, c := range chains {
		if c.BeaconID == label {
			return c, true
		}
	}
	return chain.Chain{}, false
}

// parseRoundLabel returns the round number in label.
// It accepts only canonical decimal digits with no leading zero, from 1 to chain.MaxRound,
// so that two names never hold one round (FR-answers-3, FR-answers-5).
func parseRoundLabel(label string) (uint64, bool) {
	if label == "" || label[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(label, 10, 64)
	if err != nil || n > chain.MaxRound {
		return 0, false
	}
	return n, true
}
