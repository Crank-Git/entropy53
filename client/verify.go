package client

import (
	"fmt"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
	"github.com/miekg/dns"
)

// minGrace is the shortest grace time. A round that is late by less than this time is not yet withheld.
const minGrace = 10 * time.Second

// graceTime returns the time after the round time at which a missing round counts as withheld.
func graceTime(c chain.Chain) time.Duration {
	return max(2*time.Duration(c.Period)*time.Second, minGrace)
}

// negative returns the error for a negative answer to the query for round n.
// The clock selects the error type only. It has no effect on whether a round passes.
func (c *Client) negative(n uint64) error {
	deadline := c.cfg.Chain.RoundTime(n).Add(graceTime(c.cfg.Chain))
	if c.clock().Before(deadline) {
		return fmt.Errorf("%w: round %d of %s", ErrNotPublished, n, c.cfg.Chain.BeaconID)
	}
	return fmt.Errorf("%w: round %d of %s", ErrWithheld, n, c.cfg.Chain.BeaconID)
}

// verifyRecords returns the first TXT record of answer that verifies against the registry key.
// A want of 0 accepts any round number. Any other want rejects a record with another number.
// It computes the randomness from the signature and reads no other value from a record.
// It returns an error that wraps ErrInvalid when no record verifies.
func (c *Client) verifyRecords(answer []dns.RR, want uint64) (Round, error) {
	ch := c.cfg.Chain
	for _, rr := range answer {
		txt, ok := rr.(*dns.TXT)
		if !ok {
			continue
		}
		raw, err := record.FromStrings(txt.Txt)
		if err != nil {
			continue
		}
		body, err := record.Decode(ch, raw)
		if err != nil || (want != 0 && body.Number != want) {
			continue
		}
		if err := ch.Verify(body.Number, body.Sig, body.PrevSig); err != nil {
			continue
		}
		return Round{
			Chain:             ch.BeaconID,
			Number:            body.Number,
			Randomness:        chain.Randomness(body.Sig),
			Signature:         body.Sig,
			PreviousSignature: body.PrevSig,
		}, nil
	}
	return Round{}, fmt.Errorf("%w: %d records in the answer", ErrInvalid, len(answer))
}
