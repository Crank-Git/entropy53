// Command refreshvectors rewrites testdata/vectors.json from a real upstream.
//
// It is the only program that reads randomness from an upstream. The value is
// test data from an independent source: no verification path uses it.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
)

// upstreamURL is the first default upstream in docs/specs/spec.md.
const upstreamURL = "https://api.drand.sh"

// vectorRound is the round that every vector holds.
const vectorRound = 1000

// outPath is relative to the repository root, where make runs this program.
const outPath = "testdata/vectors.json"

// beaconIDs fixes the order of the vectors, so that two runs give one file.
var beaconIDs = []string{"quicknet", "default", "evmnet"}

type vector struct {
	BeaconID          string   `json:"beacon_id"`
	Round             uint64   `json:"round"`
	Signature         string   `json:"signature"`
	PreviousSignature string   `json:"previous_signature"`
	TXT               []string `json:"txt"`
	Randomness        string   `json:"randomness"`
}

// upstreamRound is the drand HTTP API v1 answer for public/<round>.
type upstreamRound struct {
	Round             uint64 `json:"round"`
	Randomness        string `json:"randomness"`
	Signature         string `json:"signature"`
	PreviousSignature string `json:"previous_signature"`
}

func main() {
	if err := run(upstreamURL, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "refresh-vectors:", err)
		os.Exit(1)
	}
}

// run fetches round 1000 of each chain from baseURL and writes the vector file.
// It writes nothing unless all fetches succeed.
func run(baseURL, path string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	var vectors []vector
	for _, id := range beaconIDs {
		c, err := chain.Lookup(id)
		if err != nil {
			return err
		}
		v, err := fetch(client, baseURL, c)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		vectors = append(vectors, v)
	}
	data, err := json.MarshalIndent(struct {
		Vectors []vector `json:"vectors"`
	}{vectors}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// fetch gets one round and builds its vector.
func fetch(client *http.Client, baseURL string, c chain.Chain) (vector, error) {
	url := fmt.Sprintf("%s/%s/public/%d", baseURL, c.ChainHash, vectorRound)
	resp, err := client.Get(url)
	if err != nil {
		return vector{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return vector{}, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return vector{}, err
	}
	var r upstreamRound
	if err := json.Unmarshal(body, &r); err != nil {
		return vector{}, fmt.Errorf("parse answer: %w", err)
	}
	if r.Round != vectorRound {
		return vector{}, fmt.Errorf("round %d, want %d", r.Round, vectorRound)
	}
	sig, err := hex.DecodeString(r.Signature)
	if err != nil {
		return vector{}, fmt.Errorf("signature: %w", err)
	}
	prev, err := hex.DecodeString(r.PreviousSignature)
	if err != nil {
		return vector{}, fmt.Errorf("previous signature: %w", err)
	}
	rnd, err := hex.DecodeString(r.Randomness)
	if err != nil || len(rnd) != 32 {
		return vector{}, errors.New("randomness: want 32 hexadecimal octets")
	}
	raw, err := record.Encode(c, record.Body{Version: 1, Number: r.Round, Sig: sig, PrevSig: prev})
	if err != nil {
		return vector{}, err
	}
	return vector{
		BeaconID:          c.BeaconID,
		Round:             r.Round,
		Signature:         r.Signature,
		PreviousSignature: r.PreviousSignature,
		TXT:               record.ToStrings(raw),
		Randomness:        r.Randomness,
	}, nil
}

// writeAtomic replaces path through a temporary file in the same directory.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vectors-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
