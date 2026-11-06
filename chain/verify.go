package chain

import (
	"fmt"

	"github.com/drand/drand/v2/common"
	"github.com/drand/drand/v2/crypto"
)

// Verify checks the signature of one round against the public key of the chain.
// It returns an error when the signature is invalid, the round number is wrong
// or the scheme is unknown. The error text names the beacon ID.
// An unchained scheme does not sign the previous signature, so a caller passes an empty one there.
func (c Chain) Verify(number uint64, sig, prevSig []byte) error {
	scheme, err := crypto.SchemeFromName(c.SchemeID)
	if err != nil {
		return fmt.Errorf("chain %s: scheme: %w", c.BeaconID, err)
	}
	key := scheme.KeyGroup.Point()
	if err := key.UnmarshalBinary(c.PublicKey); err != nil {
		return fmt.Errorf("chain %s: public key: %w", c.BeaconID, err)
	}
	beacon := &common.Beacon{Round: number, Signature: sig, PreviousSig: prevSig}
	if err := scheme.VerifyBeacon(beacon, key); err != nil {
		return fmt.Errorf("chain %s: round %d: %w", c.BeaconID, number, err)
	}
	return nil
}

// Randomness returns sha256 of the signature, through the drand library.
// The client library computes randomness with this function and never reads it from a gateway.
func Randomness(sig []byte) [32]byte {
	var out [32]byte
	copy(out[:], crypto.RandomnessFromSignature(sig))
	return out
}
