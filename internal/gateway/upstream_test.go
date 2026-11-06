package gateway

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// vector is one entry of testdata/vectors.json. The schema is in testdata/README.md.
type vector struct {
	BeaconID          string `json:"beacon_id"`
	Round             uint64 `json:"round"`
	Signature         string `json:"signature"`
	PreviousSignature string `json:"previous_signature"`
}

// vectorOf returns the real round of one chain from the vector file. Later tests in this package reuse it.
func vectorOf(t *testing.T, beaconID string) vector {
	t.Helper()
	data, err := os.ReadFile("../../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vector file: %v", err)
	}
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse vector file: %v", err)
	}
	for _, v := range file.Vectors {
		if v.BeaconID == beaconID {
			return v
		}
	}
	t.Fatalf("no vector for %s", beaconID)
	return vector{}
}

// logBuffer is a log target that is safe for concurrent writes. Later tests in this package reuse it.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeUpstream is an httptest server that counts requests and records their paths.
// Later tests in this package reuse it.
type fakeUpstream struct {
	srv   *httptest.Server
	hits  atomic.Int32
	mu    sync.Mutex
	paths []string
}

func newFakeUpstream(t *testing.T, handler http.HandlerFunc) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) url() string { return f.srv.URL }

func (f *fakeUpstream) requestPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// roundBody returns the drand v1 JSON of a vector, with an extra randomness field that no code may read.
func roundBody(v vector) string {
	return fmt.Sprintf(`{"round":%d,"randomness":12,"signature":%q,"previous_signature":%q}`,
		v.Round, v.Signature, v.PreviousSignature)
}

func serveRound(v vector) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(roundBody(v)))
	}
}

func fail500(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "boom", http.StatusInternalServerError)
}

func TestRoundFailsOverToSecondUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, fail500)
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	got, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if status != Found {
		t.Fatalf("status = %v, want Found", status)
	}
	if got.Number != v.Round || hex.EncodeToString(got.Sig) != v.Signature {
		t.Errorf("round = %d %x, want %d %s", got.Number, got.Sig, v.Round, v.Signature)
	}
	if first.hits.Load() != 1 || second.hits.Load() != 1 {
		t.Errorf("hits = %d, %d, want 1, 1", first.hits.Load(), second.hits.Load())
	}
}

func TestPreviousSignatureIsDecoded(t *testing.T) {
	v := vectorOf(t, "default")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	got, status := u.fetchRound(context.Background(), chain.Default, v.Round)

	if status != Found || hex.EncodeToString(got.PrevSig) != v.PreviousSignature {
		t.Errorf("status = %v, prev = %x, want Found, %s", status, got.PrevSig, v.PreviousSignature)
	}
}

func TestJunkJSONTriesNextUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{not json")) })
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if status != Found || second.hits.Load() != 1 {
		t.Errorf("status = %v, second hits = %d, want Found, 1", status, second.hits.Load())
	}
}

func TestInvalidHexTriesNextUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"round":1000,"signature":"zz","previous_signature":""}`))
	})
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if status != Found {
		t.Errorf("status = %v, want Found", status)
	}
}

func TestUpstreamFailureWritesLogLine(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, fail500)
	second := newFakeUpstream(t, serveRound(v))
	var logs logBuffer
	u := NewUpstreams(Config{
		Upstreams: []string{first.url(), second.url()},
		Log:       slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	out := logs.String()
	if strings.Count(out, "upstream failed") != 1 || !strings.Contains(out, first.url()) {
		t.Errorf("log = %q, want one upstream failed line naming %s", out, first.url())
	}
}

func TestRoundZeroSendsNoRequest(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, 0)

	if status != Unavailable || up.hits.Load() != 0 {
		t.Errorf("status = %v, hits = %d, want Unavailable, 0", status, up.hits.Load())
	}
}

func TestRoundAboveMaxRoundSendsNoRequest(t *testing.T) {
	up := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, chain.MaxRound+1)

	if status != Unavailable || up.hits.Load() != 0 {
		t.Errorf("status = %v, hits = %d, want Unavailable, 0", status, up.hits.Load())
	}
}

func TestHTTP425ReturnsNotPublishedAndSendsNoSecondRequest(t *testing.T) {
	first := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Requested future beacon", http.StatusTooEarly)
	})
	second := newFakeUpstream(t, serveRound(vectorOf(t, "quicknet")))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, 1000)

	if status != NotPublished || second.hits.Load() != 0 {
		t.Errorf("status = %v, second hits = %d, want NotPublished, 0", status, second.hits.Load())
	}
}

func TestStatus404ForDueRoundReturnsUnavailable(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, 1000)

	if status != Unavailable {
		t.Errorf("status = %v, want Unavailable", status)
	}
}

// paddedBody returns valid round JSON padded with spaces to size bytes.
func paddedBody(v vector, size int) string {
	body := roundBody(v)
	return body + strings.Repeat(" ", size-len(body))
}

func TestBody5000BytesReturnsUnavailable(t *testing.T) {
	v := vectorOf(t, "default")
	// The first 4096 bytes are valid JSON, so only the size limit can reject the body.
	body := paddedBody(v, 5000)
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Default, v.Round)

	if status != Unavailable {
		t.Errorf("status = %v, want Unavailable", status)
	}
}

func TestBody4095BytesReturnsFound(t *testing.T) {
	v := vectorOf(t, "default")
	body := paddedBody(v, 4095)
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Default, v.Round)

	if status != Found {
		t.Errorf("status = %v, want Found", status)
	}
}

func TestRequestPathUsesV1(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url() + "/"}})

	u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	want := "/" + chain.Quicknet.ChainHash + "/public/1000"
	if got := up.requestPaths(); len(got) != 1 || got[0] != want {
		t.Errorf("paths = %v, want [%s]", got, want)
	}
}

func TestRandomnessFieldIsIgnored(t *testing.T) {
	// The body holds "randomness": 12, which is not a hexadecimal string. A reader of the field would fail.
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if status != Found {
		t.Errorf("status = %v, want Found", status)
	}
}

func TestStartsWithLastSuccessfulUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	var failing atomic.Bool
	failing.Store(true)
	first := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			fail500(w, r)
			return
		}
		serveRound(v)(w, r)
	})
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	u.fetchRound(context.Background(), chain.Quicknet, v.Round)
	failing.Store(false)
	first.hits.Store(0)
	second.hits.Store(0)
	u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if first.hits.Load() != 0 || second.hits.Load() != 1 {
		t.Errorf("hits = %d, %d, want 0, 1", first.hits.Load(), second.hits.Load())
	}
}

func TestUpstreamOrderIsPerChain(t *testing.T) {
	q := vectorOf(t, "quicknet")
	d := vectorOf(t, "default")
	first := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, chain.Quicknet.ChainHash) {
			fail500(w, r)
			return
		}
		serveRound(d)(w, r)
	})
	second := newFakeUpstream(t, serveRound(q))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	u.fetchRound(context.Background(), chain.Quicknet, q.Round)
	first.hits.Store(0)
	second.hits.Store(0)
	_, status := u.fetchRound(context.Background(), chain.Default, d.Round)

	if status != Found || first.hits.Load() != 1 {
		t.Errorf("status = %v, first hits = %d, want Found, 1", status, first.hits.Load())
	}
}

func TestRequestTimeoutIs2Seconds(t *testing.T) {
	u := NewUpstreams(Config{Upstreams: []string{"http://127.0.0.1:1"}})

	if requestTimeout != 2*time.Second || u.client.Timeout != requestTimeout {
		t.Errorf("timeout = %v, client = %v, want 2s", requestTimeout, u.client.Timeout)
	}
}

func TestStalledUpstreamTriesNext(t *testing.T) {
	v := vectorOf(t, "quicknet")
	release := make(chan struct{})
	first := newFakeUpstream(t, func(http.ResponseWriter, *http.Request) { <-release })
	// Registered after the server, so the cleanup runs first and unblocks the handler.
	t.Cleanup(func() { close(release) })
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})
	u.client.Timeout = 20 * time.Millisecond

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if status != Found || second.hits.Load() != 1 {
		t.Errorf("status = %v, second hits = %d, want Found, 1", status, second.hits.Load())
	}
}

func TestCanceledContextStopsFailover(t *testing.T) {
	first := newFakeUpstream(t, fail500)
	second := newFakeUpstream(t, fail500)
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, status := u.fetchRound(ctx, chain.Quicknet, 1000)

	if status != Unavailable || first.hits.Load()+second.hits.Load() != 0 {
		t.Errorf("status = %v, hits = %d, want Unavailable, 0", status, first.hits.Load()+second.hits.Load())
	}
}

// badSignature returns v with a changed last hexadecimal digit in the signature.
func badSignature(v vector) vector {
	last := v.Signature[len(v.Signature)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	v.Signature = v.Signature[:len(v.Signature)-1] + string(repl)
	return v
}

func newLoggedUpstreams(urls []string, logs *logBuffer) *Upstreams {
	return NewUpstreams(Config{Upstreams: urls, Log: slog.New(slog.NewJSONHandler(logs, nil))})
}

func TestBadSignatureKeepsNoRound(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(badSignature(v)))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if _, ok := u.store.get("quicknet", v.Round); ok || status != Unavailable {
		t.Errorf("stored = %v, status = %v, want false, Unavailable", ok, status)
	}
}

func TestBadSignatureWritesOneLogLine(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(badSignature(v)))
	var logs logBuffer
	u := newLoggedUpstreams([]string{up.url()}, &logs)

	u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	out := logs.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "round discarded") || strings.Contains(out, "upstream failed") {
		t.Errorf("log = %q, want one round discarded line", out)
	}
}

func TestWrongRoundNumberKeepsNoRound(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round-1)

	_, storedWrong := u.store.get("quicknet", v.Round)
	_, storedAsked := u.store.get("quicknet", v.Round-1)
	if storedWrong || storedAsked || status != Unavailable {
		t.Errorf("stored = %v, %v, status = %v, want false, false, Unavailable", storedWrong, storedAsked, status)
	}
}

func TestWrongRoundNumberWritesOneLogLine(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	var logs logBuffer
	u := newLoggedUpstreams([]string{up.url()}, &logs)

	u.fetchRound(context.Background(), chain.Quicknet, v.Round-1)

	out := logs.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "round discarded") {
		t.Errorf("log = %q, want one round discarded line", out)
	}
}

func TestVerifiedRoundEntersStore(t *testing.T) {
	for _, c := range []chain.Chain{chain.Default, chain.Quicknet} {
		t.Run(c.BeaconID, func(t *testing.T) {
			v := vectorOf(t, c.BeaconID)
			up := newFakeUpstream(t, serveRound(v))
			u := NewUpstreams(Config{Upstreams: []string{up.url()}})

			_, status := u.fetchRound(context.Background(), c, v.Round)

			got, ok := u.store.get(c.BeaconID, v.Round)
			if status != Found || !ok || hex.EncodeToString(got.Sig) != v.Signature {
				t.Errorf("status = %v, stored = %v, want Found, true", status, ok)
			}
		})
	}
}

func TestBadSignatureTriesNextUpstream(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, serveRound(badSignature(v)))
	second := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if _, ok := u.store.get("quicknet", v.Round); status != Found || !ok || second.hits.Load() != 1 {
		t.Errorf("status = %v, stored = %v, second hits = %d, want Found, true, 1", status, ok, second.hits.Load())
	}
}

func TestBadSignatureDoesNotMoveUpstreamOrder(t *testing.T) {
	v := vectorOf(t, "quicknet")
	first := newFakeUpstream(t, serveRound(badSignature(v)))
	second := newFakeUpstream(t, serveRound(badSignature(v)))
	u := NewUpstreams(Config{Upstreams: []string{first.url(), second.url()}})
	u.setStart(chain.Quicknet.ChainHash, 1)

	u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if got := u.start(chain.Quicknet.ChainHash); got != 1 {
		t.Errorf("start = %d, want 1", got)
	}
}

func TestUnchainedRoundWithPreviousSignatureIsDiscarded(t *testing.T) {
	v := vectorOf(t, "quicknet")
	v.PreviousSignature = vectorOf(t, "default").PreviousSignature
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchRound(context.Background(), chain.Quicknet, v.Round)

	if _, ok := u.store.get("quicknet", v.Round); ok || status != Unavailable {
		t.Errorf("stored = %v, status = %v, want false, Unavailable", ok, status)
	}
}

func TestFetchPathWithNoRequestedNumberKeepsVerifiedRound(t *testing.T) {
	v := vectorOf(t, "quicknet")
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	got, status := u.fetchPath(context.Background(), chain.Quicknet, "latest", 0)

	_, ok := u.store.get("quicknet", v.Round)
	want := "/" + chain.Quicknet.ChainHash + "/public/latest"
	if status != Found || got.Number != v.Round || !ok || up.requestPaths()[0] != want {
		t.Errorf("status = %v, round = %d, stored = %v, paths = %v", status, got.Number, ok, up.requestPaths())
	}
}

func TestFetchPathRejectsRoundZeroInBody(t *testing.T) {
	v := vectorOf(t, "quicknet")
	v.Round = 0
	up := newFakeUpstream(t, serveRound(v))
	u := NewUpstreams(Config{Upstreams: []string{up.url()}})

	_, status := u.fetchPath(context.Background(), chain.Quicknet, "latest", 0)

	if _, ok := u.store.get("quicknet", 0); ok || status != Unavailable {
		t.Errorf("stored = %v, status = %v, want false, Unavailable", ok, status)
	}
}
