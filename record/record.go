package record

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Crank-Git/entropy53/chain"
)

// version is the only body version this package reads and writes.
const version uint8 = 0x01

// headerLen is the version byte plus the 8-byte round number.
const headerLen = 9

// Body is a decoded record body. It holds no randomness: the client library
// computes that value from Sig and never reads it from a body.
type Body struct {
	Version uint8
	Number  uint64
	Sig     []byte
	// PrevSig is empty on a chain without a previous signature.
	PrevSig []byte
}

// Encode returns the binary layout of b for chain c.
// It returns an error for any body that Decode rejects.
func Encode(c chain.Chain, b Body) ([]byte, error) {
	if b.Version != version {
		return nil, fmt.Errorf("record: version %#x, want %#x", b.Version, version)
	}
	if err := checkNumber(b.Number); err != nil {
		return nil, err
	}
	if len(b.Sig) != c.SigLen {
		return nil, fmt.Errorf("record: signature length %d, want %d", len(b.Sig), c.SigLen)
	}
	if len(b.PrevSig) != c.PrevSigLen {
		return nil, fmt.Errorf("record: previous signature length %d, want %d", len(b.PrevSig), c.PrevSigLen)
	}
	raw := make([]byte, headerLen, headerLen+c.SigLen+c.PrevSigLen)
	raw[0] = version
	binary.BigEndian.PutUint64(raw[1:headerLen], b.Number)
	raw = append(raw, b.Sig...)
	return append(raw, b.PrevSig...), nil
}

// Decode parses raw as a record body of chain c.
// It returns an error for a wrong length, a wrong version or a round number
// outside 1 to chain.MaxRound. The length check runs first, so no offset is
// read from a short body.
func Decode(c chain.Chain, raw []byte) (Body, error) {
	if want := headerLen + c.SigLen + c.PrevSigLen; len(raw) != want {
		return Body{}, fmt.Errorf("record: body length %d, want %d", len(raw), want)
	}
	if raw[0] != version {
		return Body{}, fmt.Errorf("record: version %#x, want %#x", raw[0], version)
	}
	number := binary.BigEndian.Uint64(raw[1:headerLen])
	if err := checkNumber(number); err != nil {
		return Body{}, err
	}
	sigEnd := headerLen + c.SigLen
	return Body{
		Version: raw[0],
		Number:  number,
		Sig:     append([]byte(nil), raw[headerLen:sigEnd]...),
		PrevSig: append([]byte(nil), raw[sigEnd:]...),
	}, nil
}

// checkNumber returns an error unless n is from 1 to chain.MaxRound.
// Round 0 must never reach an upstream, because public/0 returns the latest round.
func checkNumber(n uint64) error {
	if n < 1 || n > chain.MaxRound {
		return errors.New("record: round number outside 1 to 2^53")
	}
	return nil
}
