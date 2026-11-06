package gateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// Status is the result of a request for a round.
type Status int

const (
	// Found means the round is available.
	Found Status = iota
	// NotPublished means the round does not exist yet.
	NotPublished
	// Unavailable means no upstream gave a usable answer.
	Unavailable
)

// Source gives the gateway rounds.
type Source interface {
	// Round returns round n of chain c.
	Round(ctx context.Context, c chain.Chain, n uint64) (chain.Round, Status)
	// Latest returns the newest round of c that the gateway holds.
	Latest(c chain.Chain) (chain.Round, bool)
}

const (
	// requestTimeout limits one HTTP request to an upstream (FR-upstream-9).
	requestTimeout = 2 * time.Second
	// maxBody limits the bytes the gateway reads from an upstream response (FR-upstream-11).
	maxBody = 4096
)

// Config holds the settings of the upstream client.
type Config struct {
	// Upstreams holds the upstream base URLs in order of preference.
	Upstreams []string
	// Log receives one line per upstream failure. A nil Log selects slog.Default.
	Log *slog.Logger
}

// Upstreams fetches rounds from the upstream list, with failover.
type Upstreams struct {
	upstreams []string
	log       *slog.Logger
	// client is a field so that a test can shorten its timeout.
	client *http.Client

	clock clock
	store *store

	mu sync.Mutex
	// preferred holds, per chain hash, the index of the upstream that last succeeded.
	preferred map[string]int
}

// NewUpstreams returns an Upstreams for the list in cfg.
func NewUpstreams(cfg Config) *Upstreams {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	list := make([]string, len(cfg.Upstreams))
	for i, base := range cfg.Upstreams {
		list[i] = strings.TrimRight(base, "/")
	}
	return &Upstreams{
		upstreams: list,
		log:       log,
		client:    &http.Client{Timeout: requestTimeout},
		clock:     realClock{},
		store:     newStore(storeCapacity),
		preferred: make(map[string]int),
	}
}

// upstreamRound is the part of the drand v1 JSON that the gateway reads.
// It has no randomness field, because the gateway never reads that value.
type upstreamRound struct {
	Round             uint64 `json:"round"`
	Signature         string `json:"signature"`
	PreviousSignature string `json:"previous_signature"`
}

var (
	errBodyTooLarge = errors.New("response body reaches the size limit")
	errStatus       = errors.New("unexpected HTTP status")
	errTooEarly     = errors.New("round not published")
)

// fetchRound returns round n of chain c from the first upstream that gives a verified round.
// It returns Unavailable for round 0 and for a round above chain.MaxRound
// without a request, because the path public/0 returns the latest round.
// It returns NotPublished at the first HTTP 425 and tries no other upstream.
func (u *Upstreams) fetchRound(ctx context.Context, c chain.Chain, n uint64) (chain.Round, Status) {
	if n == 0 || n > chain.MaxRound {
		return chain.Round{}, Unavailable
	}
	return u.fetchPath(ctx, c, strconv.FormatUint(n, 10), n)
}

// fetchPath requests public/label from the upstreams in order and keeps the first verified round.
// The want argument is the requested round number, or 0 when label is latest and no number is requested.
// A round with another number, a wrong previous signature length or a failed signature check
// is discarded with one log line, and the next upstream is tried (FR-upstream-4 to FR-upstream-7).
func (u *Upstreams) fetchPath(ctx context.Context, c chain.Chain, label string, want uint64) (chain.Round, Status) {
	start := u.start(c.ChainHash)
	for i := range u.upstreams {
		if ctx.Err() != nil {
			return chain.Round{}, Unavailable
		}
		idx := (start + i) % len(u.upstreams)
		base := u.upstreams[idx]
		r, err := u.get(ctx, base, c, label)
		switch {
		case errors.Is(err, errTooEarly):
			return chain.Round{}, NotPublished
		case err != nil:
			if ctx.Err() != nil {
				return chain.Round{}, Unavailable
			}
			u.log.Warn("upstream failed", "upstream", base, "beacon_id", c.BeaconID, "path", label, "error", err)
			continue
		}
		if err := checkRound(c, r, want); err != nil {
			u.log.Warn("round discarded", "upstream", base, "beacon_id", c.BeaconID, "path", label, "round", r.Number, "error", err)
			continue
		}
		u.store.put(c.BeaconID, r)
		u.setStart(c.ChainHash, idx)
		return r, Found
	}
	return chain.Round{}, Unavailable
}

// checkRound returns an error when r is not the requested round or fails verification.
// An unchained scheme does not sign the previous signature, so a wrong length is rejected first.
func checkRound(c chain.Chain, r chain.Round, want uint64) error {
	if want != 0 && r.Number != want {
		return fmt.Errorf("round number %d, requested %d", r.Number, want)
	}
	if len(r.PrevSig) != c.PrevSigLen {
		return fmt.Errorf("previous signature has %d bytes, want %d", len(r.PrevSig), c.PrevSigLen)
	}
	return c.Verify(r.Number, r.Sig, r.PrevSig)
}

func (u *Upstreams) start(chainHash string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.preferred[chainHash]
}

func (u *Upstreams) setStart(chainHash string, idx int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.preferred[chainHash] = idx
}

// get requests public/label from one upstream and decodes the answer. It does not verify the round.
func (u *Upstreams) get(ctx context.Context, base string, c chain.Chain, label string) (chain.Round, error) {
	url := base + "/" + c.ChainHash + "/public/" + label
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return chain.Round{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return chain.Round{}, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooEarly:
		return chain.Round{}, errTooEarly
	default:
		return chain.Round{}, fmt.Errorf("%w: %d", errStatus, resp.StatusCode)
	}
	// A body of exactly maxBody bytes fails, because a truncated body can still be valid JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return chain.Round{}, fmt.Errorf("read body: %w", err)
	}
	if len(body) >= maxBody {
		return chain.Round{}, errBodyTooLarge
	}
	var raw upstreamRound
	if err := json.Unmarshal(body, &raw); err != nil {
		return chain.Round{}, fmt.Errorf("decode body: %w", err)
	}
	sig, err := hex.DecodeString(raw.Signature)
	if err != nil {
		return chain.Round{}, fmt.Errorf("decode signature: %w", err)
	}
	var prev []byte
	if raw.PreviousSignature != "" {
		if prev, err = hex.DecodeString(raw.PreviousSignature); err != nil {
			return chain.Round{}, fmt.Errorf("decode previous signature: %w", err)
		}
	}
	return chain.Round{Number: raw.Round, Sig: sig, PrevSig: prev}, nil
}
