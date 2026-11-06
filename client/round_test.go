package client

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
	"github.com/miekg/dns"
)

// quicknetRound1001Signature is the signature of real round 1001 of quicknet.
const quicknetRound1001Signature = "b33bf3667cbd5a82de3a24b4e0e9fe5513cc1a0e840368c6e31f5fcfa79bea03f73896b25883abf2853d10337fb8fa41"

// multiTXTReply returns a NOERROR reply to req with one TXT record for each element of records.
func multiTXTReply(req *dns.Msg, records ...[]string) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	for _, strs := range records {
		resp.Answer = append(resp.Answer, &dns.TXT{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
			Txt: strs,
		})
	}
	return resp
}

// fixedResolver starts a test resolver that always answers with the given TXT records.
func fixedResolver(t *testing.T, records ...[]string) *testResolver {
	t.Helper()
	return startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return multiTXTReply(req, records...) })
}

func TestRoundReturnsVectorRandomnessThroughGateway(t *testing.T) {
	vectors := loadVectors(t)
	addr := startGateway(t, vectorSource(vectors, nil), chain.Quicknet, chain.Default, chain.Evmnet)
	for _, v := range vectors {
		t.Run(v.chain.BeaconID, func(t *testing.T) {
			got, err := newTestClient(t, v.chain, addr, nil).Round(t.Context(), v.number)
			if err != nil {
				t.Fatalf("Round(%d): %v", v.number, err)
			}
			if got.Chain != v.chain.BeaconID || got.Number != v.number {
				t.Errorf("chain and number = %q, %d; want %q, %d", got.Chain, got.Number, v.chain.BeaconID, v.number)
			}
			if got.Randomness != v.randomness {
				t.Errorf("randomness = %x; want %x", got.Randomness, v.randomness)
			}
			if !bytes.Equal(got.Signature, v.sig) {
				t.Errorf("signature = %x; want %x", got.Signature, v.sig)
			}
			if !bytes.Equal(got.PreviousSignature, v.prevSig) {
				t.Errorf("previous signature = %x; want %x", got.PreviousSignature, v.prevSig)
			}
		})
	}
}

func TestRoundTCRetryReturnsVerifiedRound(t *testing.T) {
	v := loadVectors(t)[0]
	r := startResolver(t, func(network string, req *dns.Msg) *dns.Msg {
		if network == "udp" {
			resp := rcodeReply(req, dns.RcodeSuccess)
			resp.Truncated = true
			return resp
		}
		return txtReply(req, v.txt)
	})
	got, err := newTestClient(t, v.chain, r.addr, nil).Round(t.Context(), v.number)
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if got.Randomness != v.randomness {
		t.Errorf("randomness = %x; want %x", got.Randomness, v.randomness)
	}
}

func TestRoundModifiedSignatureReturnsErrInvalid(t *testing.T) {
	vectors := loadVectors(t)
	flip := func(r *chain.Round) { r.Sig[len(r.Sig)-1] ^= 1 }
	addr := startGateway(t, vectorSource(vectors, flip), chain.Quicknet)
	v := vectors[0]
	_, err := newTestClient(t, v.chain, addr, nil).Round(t.Context(), v.number)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Round error = %v; want ErrInvalid", err)
	}
}

func TestRoundOtherValidRoundReturnsErrInvalid(t *testing.T) {
	sig, err := hex.DecodeString(quicknetRound1001Signature)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := chain.Quicknet.Verify(1001, sig, nil); err != nil {
		t.Fatalf("round 1001 does not verify, so the test proves nothing: %v", err)
	}
	// The resolver answers the name of round 1000 with the valid record of round 1001.
	body, err := record.Encode(chain.Quicknet, record.Body{Version: 1, Number: 1001, Sig: sig})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	r := fixedResolver(t, record.ToStrings(body))
	_, err = newTestClient(t, chain.Quicknet, r.addr, nil).Round(t.Context(), 1000)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Round error = %v; want ErrInvalid", err)
	}
}

func TestRoundJunkAndValidRecordReturnsRound(t *testing.T) {
	v := loadVectors(t)[0]
	junk := []string{"junk"}
	for name, records := range map[string][][]string{
		"junk first":  {junk, v.txt},
		"junk second": {v.txt, junk},
	} {
		t.Run(name, func(t *testing.T) {
			r := fixedResolver(t, records...)
			got, err := newTestClient(t, v.chain, r.addr, nil).Round(t.Context(), v.number)
			if err != nil {
				t.Fatalf("Round: %v", err)
			}
			if got.Randomness != v.randomness {
				t.Errorf("randomness = %x; want %x", got.Randomness, v.randomness)
			}
		})
	}
}

func TestRoundAllRecordsFailReturnsErrInvalid(t *testing.T) {
	v := loadVectors(t)[0]
	r := fixedResolver(t, []string{"junk"}, []string{"AAAA"})
	_, err := newTestClient(t, v.chain, r.addr, nil).Round(t.Context(), v.number)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Round error = %v; want ErrInvalid", err)
	}
}

func TestRoundOtherChainRecordReturnsErrInvalid(t *testing.T) {
	var evm testVector
	for _, v := range loadVectors(t) {
		if v.chain.BeaconID == "evmnet" {
			evm = v
		}
	}
	r := fixedResolver(t, evm.txt)
	_, err := newTestClient(t, chain.Quicknet, r.addr, nil).Round(t.Context(), evm.number)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Round error = %v; want ErrInvalid", err)
	}
}

func TestRoundAcceptsOtherStringSplit(t *testing.T) {
	var def testVector
	for _, v := range loadVectors(t) {
		if v.chain.BeaconID == "default" {
			def = v
		}
	}
	joined := strings.Join(def.txt, "")
	r := fixedResolver(t, []string{joined[:50], joined[50:]})
	got, err := newTestClient(t, def.chain, r.addr, nil).Round(t.Context(), def.number)
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if got.Randomness != def.randomness {
		t.Errorf("randomness = %x; want %x", got.Randomness, def.randomness)
	}
}

func TestRoundNegativeBeforeGraceReturnsErrNotPublished(t *testing.T) {
	addr := startGateway(t, vectorSource(nil, nil), chain.Quicknet)
	at := chain.Quicknet.RoundTime(1000)
	for _, d := range []time.Duration{-time.Second, 5 * time.Second, 9 * time.Second} {
		_, err := newTestClient(t, chain.Quicknet, addr, fixedClock(at.Add(d))).Round(t.Context(), 1000)
		if !errors.Is(err, ErrNotPublished) {
			t.Errorf("at round time %+v: error = %v; want ErrNotPublished", d, err)
		}
	}
}

func TestRoundNegativeAtGraceReturnsErrWithheld(t *testing.T) {
	addr := startGateway(t, vectorSource(nil, nil), chain.Quicknet)
	at := chain.Quicknet.RoundTime(1000)
	for _, d := range []time.Duration{10 * time.Second, 11 * time.Second} {
		_, err := newTestClient(t, chain.Quicknet, addr, fixedClock(at.Add(d))).Round(t.Context(), 1000)
		if !errors.Is(err, ErrWithheld) {
			t.Errorf("at round time %+v: error = %v; want ErrWithheld", d, err)
		}
	}
}

func TestDefaultGraceIs60Seconds(t *testing.T) {
	addr := startGateway(t, vectorSource(nil, nil), chain.Default)
	at := chain.Default.RoundTime(1000)
	_, err := newTestClient(t, chain.Default, addr, fixedClock(at.Add(59*time.Second))).Round(t.Context(), 1000)
	if !errors.Is(err, ErrNotPublished) {
		t.Errorf("at +59 seconds: error = %v; want ErrNotPublished", err)
	}
	_, err = newTestClient(t, chain.Default, addr, fixedClock(at.Add(60*time.Second))).Round(t.Context(), 1000)
	if !errors.Is(err, ErrWithheld) {
		t.Errorf("at +60 seconds: error = %v; want ErrWithheld", err)
	}
}

func TestRoundNXDOMAINFollowsClockRule(t *testing.T) {
	r := startResolver(t, func(_ string, req *dns.Msg) *dns.Msg { return rcodeReply(req, dns.RcodeNameError) })
	at := chain.Quicknet.RoundTime(1000)
	_, err := newTestClient(t, chain.Quicknet, r.addr, fixedClock(at)).Round(t.Context(), 1000)
	if !errors.Is(err, ErrNotPublished) {
		t.Errorf("before grace: error = %v; want ErrNotPublished", err)
	}
	_, err = newTestClient(t, chain.Quicknet, r.addr, fixedClock(at.Add(10*time.Second))).Round(t.Context(), 1000)
	if !errors.Is(err, ErrWithheld) {
		t.Errorf("at grace: error = %v; want ErrWithheld", err)
	}
}

func TestRoundResultIgnoresClock(t *testing.T) {
	v := loadVectors(t)[0]
	r := fixedResolver(t, v.txt)
	for _, year := range []int{2000, 3000} {
		clock := fixedClock(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC))
		got, err := newTestClient(t, v.chain, r.addr, clock).Round(t.Context(), v.number)
		if err != nil || got.Randomness != v.randomness {
			t.Errorf("year %d: Round = %x, %v; want the vector randomness", year, got.Randomness, err)
		}
	}
}

func TestRoundOutsideRangeSendsNoQuery(t *testing.T) {
	v := loadVectors(t)[0]
	r := fixedResolver(t, v.txt)
	c := newTestClient(t, v.chain, r.addr, nil)
	for _, n := range []uint64{0, chain.MaxRound + 1} {
		_, err := c.Round(t.Context(), n)
		if err == nil {
			t.Errorf("Round(%d) returned no error", n)
		}
		for _, sentinel := range []error{ErrInvalid, ErrNotPublished, ErrWithheld, ErrStale, ErrTransport} {
			if errors.Is(err, sentinel) {
				t.Errorf("Round(%d) error wraps %v; want an argument error with no sentinel", n, sentinel)
			}
		}
	}
	if got := len(r.queries()); got != 0 {
		t.Errorf("the resolver received %d queries; want 0", got)
	}
}

func TestNewRejectsChainWithNoPublicKey(t *testing.T) {
	c := chain.Quicknet
	c.PublicKey = nil
	if _, err := New(Config{Zone: testZone, Chain: c, Resolver: "127.0.0.1:53"}); err == nil {
		t.Error("New accepted a chain with no public key")
	}
}

func TestNewRejectsPeriodBelowOne(t *testing.T) {
	c := chain.Quicknet
	c.Period = 0
	if _, err := New(Config{Zone: testZone, Chain: c, Resolver: "127.0.0.1:53"}); err == nil {
		t.Error("New accepted a chain with a period below 1")
	}
}

func TestDefaultRoundOneBodyReturnsErrInvalid(t *testing.T) {
	// A default body has a 96-byte previous signature. This body has 32 bytes.
	raw := make([]byte, 1+8+96+32)
	raw[0] = 1
	raw[8] = 1
	r := fixedResolver(t, record.ToStrings(raw))
	_, err := newTestClient(t, chain.Default, r.addr, nil).Round(t.Context(), 1)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Round error = %v; want ErrInvalid", err)
	}
}
