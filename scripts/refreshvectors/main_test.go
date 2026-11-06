package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
)

// fakeRound returns the JSON body of a drand public endpoint for chain c.
func fakeRound(c chain.Chain, round uint64) string {
	sig := hex.EncodeToString(bytes.Repeat([]byte{0xab}, c.SigLen))
	body := fmt.Sprintf(`{"round":%d,"randomness":"%s","signature":"%s"`,
		round, strings.Repeat("cd", 32), sig)
	if c.PrevSigLen > 0 {
		body += fmt.Sprintf(`,"previous_signature":"%s"`,
			hex.EncodeToString(bytes.Repeat([]byte{0xef}, c.PrevSigLen)))
	}
	return body + "}"
}

// upstream serves a fake round 1000 for each registry chain.
// The hook can replace the answer for one chain hash.
func upstream(t *testing.T, hook func(w http.ResponseWriter, hash string) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, c := range []chain.Chain{chain.Quicknet, chain.Default, chain.Evmnet} {
			if r.URL.Path != "/"+c.ChainHash+"/public/1000" {
				continue
			}
			if hook != nil && hook(w, c.ChainHash) {
				return
			}
			_, _ = fmt.Fprint(w, fakeRound(c, 1000))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type vectorFile struct {
	Vectors []struct {
		BeaconID          string   `json:"beacon_id"`
		Round             uint64   `json:"round"`
		Signature         string   `json:"signature"`
		PreviousSignature string   `json:"previous_signature"`
		TXT               []string `json:"txt"`
		Randomness        string   `json:"randomness"`
	} `json:"vectors"`
}

func TestRefreshWritesVectors(t *testing.T) {
	srv := upstream(t, nil)
	out := filepath.Join(t.TempDir(), "vectors.json")
	if err := run(srv.URL, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"quicknet", "default", "evmnet"}
	if len(f.Vectors) != len(wantIDs) {
		t.Fatalf("got %d vectors, want %d", len(f.Vectors), len(wantIDs))
	}
	for i, v := range f.Vectors {
		if v.BeaconID != wantIDs[i] || v.Round != 1000 {
			t.Errorf("vector %d = %s round %d", i, v.BeaconID, v.Round)
		}
		if v.Randomness != strings.Repeat("cd", 32) || len(v.TXT) == 0 {
			t.Errorf("vector %s lacks randomness or txt", v.BeaconID)
		}
	}
	if f.Vectors[0].PreviousSignature != "" || f.Vectors[1].PreviousSignature == "" {
		t.Error("previous signature must be empty only for a chain without one")
	}
}

func TestRefreshGivesSameFileTwice(t *testing.T) {
	srv := upstream(t, nil)
	out := filepath.Join(t.TempDir(), "vectors.json")
	var files [2][]byte
	for i := range files {
		if err := run(srv.URL, out); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = data
	}
	if !bytes.Equal(files[0], files[1]) {
		t.Fatal("second run changed the file")
	}
}

func TestRefreshKeepsFileOnUpstreamFailure(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, hash string) bool{
		"non-200 status": func(w http.ResponseWriter, hash string) bool {
			if hash != chain.Evmnet.ChainHash {
				return false
			}
			http.Error(w, "boom", http.StatusInternalServerError)
			return true
		},
		"bad hexadecimal": func(w http.ResponseWriter, hash string) bool {
			if hash != chain.Default.ChainHash {
				return false
			}
			_, _ = fmt.Fprint(w, `{"round":1000,"randomness":"zz","signature":"zz","previous_signature":"zz"}`)
			return true
		},
	}
	for name, hook := range cases {
		t.Run(name, func(t *testing.T) {
			srv := upstream(t, hook)
			out := filepath.Join(t.TempDir(), "vectors.json")
			const old = "{\"vectors\": []}\n"
			if err := os.WriteFile(out, []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := run(srv.URL, out); err == nil {
				t.Fatal("run returned nil, want an error")
			}
			got, err := os.ReadFile(out)
			if err != nil || string(got) != old {
				t.Fatalf("file changed on failure: %q, %v", got, err)
			}
		})
	}
}

func TestRefreshRejectsAnotherRoundNumber(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, hash string) bool {
		if hash != chain.Quicknet.ChainHash {
			return false
		}
		_, _ = fmt.Fprint(w, fakeRound(chain.Quicknet, 1001))
		return true
	})
	out := filepath.Join(t.TempDir(), "vectors.json")
	if err := run(srv.URL, out); err == nil {
		t.Fatal("run returned nil, want an error")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("run wrote a file after a wrong round number")
	}
}
