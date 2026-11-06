package client

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// latestVectorSource serves the real vectors as the latest round of their chain, with the given edit.
// It reports no latest round when found is false.
type latestVectorSource struct {
	sourceFunc
	vectors []testVector
	edit    func(*chain.Round)
	found   bool
}

func (s latestVectorSource) Latest(c chain.Chain) (chain.Round, bool) {
	if !s.found {
		return chain.Round{}, false
	}
	for _, v := range s.vectors {
		if v.chain.BeaconID == c.BeaconID {
			r := chain.Round{Number: v.number, Sig: append([]byte(nil), v.sig...), PrevSig: append([]byte(nil), v.prevSig...)}
			if s.edit != nil {
				s.edit(&r)
			}
			return r, true
		}
	}
	return chain.Round{}, false
}

func startLatestGateway(t *testing.T, vectors []testVector, edit func(*chain.Round), found bool) string {
	t.Helper()
	return startGateway(t, latestVectorSource{sourceFunc: vectorSource(vectors, nil), vectors: vectors, edit: edit, found: found}, chain.Quicknet)
}

// quicknetVector returns the real quicknet vector, which is round 1000.
func quicknetVector(t *testing.T) testVector {
	t.Helper()
	for _, v := range loadVectors(t) {
		if v.chain.BeaconID == chain.Quicknet.BeaconID {
			return v
		}
	}
	t.Fatal("no quicknet vector")
	return testVector{}
}

func TestLatestThreeRoundsOldReturnsErrStaleAndRound(t *testing.T) {
	v := quicknetVector(t)
	addr := startLatestGateway(t, []testVector{v}, nil, true)
	clock := fixedClock(v.chain.RoundTime(v.number + 3))
	got, err := newTestClient(t, v.chain, addr, clock).Latest(t.Context())
	if !errors.Is(err, ErrStale) {
		t.Fatalf("Latest error = %v; want ErrStale", err)
	}
	if got.Number != v.number || got.Randomness != v.randomness {
		t.Errorf("round = %d, %x; want %d, %x", got.Number, got.Randomness, v.number, v.randomness)
	}
}

func TestLatestTwoRoundsOldReturnsNoError(t *testing.T) {
	v := quicknetVector(t)
	addr := startLatestGateway(t, []testVector{v}, nil, true)
	for name, at := range map[string]time.Time{
		"at round time 2":          v.chain.RoundTime(v.number + 2),
		"just before round time 3": v.chain.RoundTime(v.number + 3).Add(-time.Nanosecond),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newTestClient(t, v.chain, addr, fixedClock(at)).Latest(t.Context())
			if err != nil {
				t.Fatalf("Latest: %v", err)
			}
			if got.Number != v.number || got.Randomness != v.randomness {
				t.Errorf("round = %d, %x; want %d, %x", got.Number, got.Randomness, v.number, v.randomness)
			}
		})
	}
}

func TestLatestAboveCurrentRoundPasses(t *testing.T) {
	v := quicknetVector(t)
	addr := startLatestGateway(t, []testVector{v}, nil, true)
	clock := fixedClock(v.chain.RoundTime(v.number - 1))
	if _, err := newTestClient(t, v.chain, addr, clock).Latest(t.Context()); err != nil {
		t.Errorf("Latest error = %v; want none", err)
	}
}

func TestLatestClockBeforeGenesisPasses(t *testing.T) {
	v := quicknetVector(t)
	addr := startLatestGateway(t, []testVector{v}, nil, true)
	clock := fixedClock(time.Unix(v.chain.Genesis-1, 0))
	if _, err := newTestClient(t, v.chain, addr, clock).Latest(t.Context()); err != nil {
		t.Errorf("Latest error = %v; want none", err)
	}
}

func TestLatestQueriesLatestName(t *testing.T) {
	v := quicknetVector(t)
	r := fixedResolver(t, v.txt)
	if _, err := newTestClient(t, v.chain, r.addr, fixedClock(v.chain.RoundTime(v.number))).Latest(t.Context()); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	qs := r.queries()
	if len(qs) != 1 {
		t.Fatalf("resolver received %d queries; want 1", len(qs))
	}
	q := qs[0].msg.Question[0]
	want := "latest." + v.chain.BeaconID + "." + testZone
	if !strings.EqualFold(q.Name, want) || q.Qtype != dns.TypeTXT {
		t.Errorf("question = %s type %d; want %s TXT", q.Name, q.Qtype, want)
	}
}

func TestLatestNegativeAnswerReturnsErrWithheld(t *testing.T) {
	addr := startLatestGateway(t, nil, nil, false)
	at := chain.Quicknet.RoundTime(1000)
	for _, d := range []time.Duration{-time.Hour, time.Hour} {
		_, err := newTestClient(t, chain.Quicknet, addr, fixedClock(at.Add(d))).Latest(t.Context())
		if !errors.Is(err, ErrWithheld) {
			t.Errorf("at round time %+v: error = %v; want ErrWithheld", d, err)
		}
	}
}

func TestLatestNegativeAnswerReadsNoClock(t *testing.T) {
	addr := startLatestGateway(t, nil, nil, false)
	clock := func() time.Time {
		t.Error("Latest read the clock for a negative answer")
		return time.Time{}
	}
	_, _ = newTestClient(t, chain.Quicknet, addr, clock).Latest(t.Context())
}

func TestLatestModifiedSignatureReturnsErrInvalid(t *testing.T) {
	v := quicknetVector(t)
	flip := func(r *chain.Round) { r.Sig[len(r.Sig)-1] ^= 1 }
	addr := startLatestGateway(t, []testVector{v}, flip, true)
	_, err := newTestClient(t, v.chain, addr, fixedClock(v.chain.RoundTime(v.number))).Latest(t.Context())
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Latest error = %v; want ErrInvalid", err)
	}
}

func TestLatestJunkAndValidRecordReturnsRound(t *testing.T) {
	v := quicknetVector(t)
	r := fixedResolver(t, []string{"junk"}, v.txt)
	got, err := newTestClient(t, v.chain, r.addr, fixedClock(v.chain.RoundTime(v.number))).Latest(t.Context())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Randomness != v.randomness {
		t.Errorf("randomness = %x; want %x", got.Randomness, v.randomness)
	}
}

func TestLatestServfailReturnsErrTransport(t *testing.T) {
	r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeServerFailure) })
	_, err := newTestClient(t, chain.Quicknet, r.addr, nil).Latest(t.Context())
	if !errors.Is(err, ErrTransport) {
		t.Errorf("Latest error = %v; want ErrTransport", err)
	}
	if n := len(r.queries()); n != 1 {
		t.Errorf("resolver received %d queries; want 1", n)
	}
}
