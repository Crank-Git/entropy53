package record

import (
	"encoding/base64"
	"errors"
	"strings"
)

// maxString is the longest TXT character-string, in octets (RFC 1035 section 3.3).
const maxString = 255

// ToStrings returns raw as standard padded base64 text (RFC 4648 section 4),
// split into character-strings of at most 255 octets.
// It returns no strings for an empty raw.
func ToStrings(raw []byte) []string {
	text := base64.StdEncoding.EncodeToString(raw)
	var strs []string
	for len(text) > maxString {
		strs = append(strs, text[:maxString])
		text = text[maxString:]
	}
	if text != "" {
		strs = append(strs, text)
	}
	return strs
}

// FromStrings joins the character-strings of one TXT record and decodes the
// base64 text. It accepts any split position and ignores zero-length strings.
// It returns an error for whitespace, missing padding or non-zero trailing
// bits. The caller passes the result to Decode, which checks the length.
func FromStrings(strs []string) ([]byte, error) {
	text := strings.Join(strs, "")
	// The strict decoder still skips CR and LF, so reject them here.
	if strings.ContainsAny(text, "\r\n") {
		return nil, errors.New("record: base64 text contains a line break")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil {
		return nil, errors.New("record: invalid base64 text: " + err.Error())
	}
	return raw, nil
}
