package gateway

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/record"
	"github.com/miekg/dns"
)

const (
	// zoneTTL is the TTL of the SOA and NS records in an answer at the zone apex.
	zoneTTL = 3600
	// apexSOAMinimum is the SOA MINIMUM in an answer to an SOA query at the apex.
	apexSOAMinimum = 60
	// ednsUDPSize is the UDP payload size that the gateway announces in an OPT record.
	// It stays under the common path MTU, so that a UDP answer does not fragment.
	ednsUDPSize = 1232
	// ttlRound is the TTL of a round record. The round never changes (FR-answers-11).
	ttlRound = 86400
	// ttlNXDomain is the negative TTL of a name that the name table does not list (FR-answers-22).
	ttlNXDomain = 3600
	// ttlOtherType is the negative TTL for a type with no record, and for an empty non-terminal (FR-answers-23, FR-answers-24).
	ttlOtherType = 60
	// ttlDue is the negative TTL of a round that an upstream does not hold yet (FR-answers-14).
	// The round can appear at any moment.
	ttlDue = 1
	// ttlFutureMax is the longest negative TTL of a future round (FR-answers-13).
	ttlFutureMax = 300
)

// handler answers DNS questions for the zone.
type handler struct {
	zone     string
	chains   []chain.Chain
	ns       []string
	soaRname string
	src      Source
	clock    clock
	log      *slog.Logger
	// queryCtx is the context of each Source call.
	queryCtx context.Context
}

// newHandler returns a handler for the zone in cfg. The handler reads rounds from src.
// The caller supplies at least one NS value, because the SOA MNAME is the first one.
func newHandler(cfg ZoneConfig, src Source) *handler {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &handler{
		zone:     dns.CanonicalName(cfg.Zone),
		chains:   cfg.Chains,
		ns:       cfg.NS,
		soaRname: cfg.SOARname,
		src:      src,
		clock:    realClock{},
		log:      log,
		queryCtx: context.Background(),
	}
}

// acceptQuery tells the server to pass each query to the handler.
// The default function rejects some queries before the handler runs.
// That path drops the OPT record, and it bypasses the limiter of the UDP rate.
// A response (QR set) is still ignored.
func acceptQuery(dh dns.Header) dns.MsgAcceptAction {
	switch a := dns.DefaultMsgAcceptFunc(dh); a {
	case dns.MsgReject, dns.MsgRejectNotImplemented:
		return dns.MsgAccept
	default:
		return a
	}
}

// ServeDNS writes the answer to r. It never panics on a malformed question.
func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	h.answer(r, m)
	h.finish(w, r, m)
	if err := w.WriteMsg(m); err != nil {
		h.log.Warn("write of a DNS answer failed", "remote", w.RemoteAddr().String(), "error", err)
	}
}

// answer sets the RCODE, the AA bit and the answer records of m for the query r.
// The gate runs in a fixed order: question count, opcode, class, zone transfer, then names.
// The question keeps its original letter case in the reply, because SetReply copies it unchanged.
func (h *handler) answer(r, m *dns.Msg) {
	if len(r.Question) != 1 {
		m.Rcode = dns.RcodeFormatError
		return
	}
	if r.Opcode != dns.OpcodeQuery {
		m.Rcode = dns.RcodeNotImplemented
		return
	}
	q := r.Question[0]
	if q.Qclass != dns.ClassINET {
		m.Rcode = dns.RcodeRefused
		return
	}
	if q.Qtype == dns.TypeAXFR || q.Qtype == dns.TypeIXFR {
		m.Rcode = dns.RcodeRefused
		return
	}
	p := parseName(h.zone, h.chains, q.Name)
	if p.kind == nameOutside {
		m.Rcode = dns.RcodeRefused
		return
	}
	m.Authoritative = true
	qtype := effectiveType(p.kind, q.Qtype)
	switch p.kind {
	case nameNone:
		m.Rcode = dns.RcodeNameError
		m.Ns = append(m.Ns, h.soa(ttlNXDomain, ttlNXDomain))
	case nameApex:
		h.answerApex(m, qtype)
	case nameChain, nameRoundParent:
		h.setNoData(m, ttlOtherType)
	case nameRound:
		h.answerRound(m, q.Name, p, qtype)
	case nameLatest:
		if qtype != dns.TypeTXT {
			h.setNoData(m, ttlOtherType)
			break
		}
		h.answerLatest(m, q.Name, p)
	}
}

// answerLatest answers a TXT query at the latest name that owner spells and p describes.
// It reads the latest round from Source and sends no fetch, because a latest name is for convenience only.
// The TTL ends at the next round time (FR-answers-18). With no round in the store it returns NODATA with TTL 1 (FR-answers-19).
// A failure to build the record returns SERVFAIL.
func (h *handler) answerLatest(m *dns.Msg, owner string, p parsedName) {
	rd, ok := h.src.Latest(p.chain)
	if !ok {
		h.setNoData(m, ttlDue)
		return
	}
	// A follower that is behind gives a next round time in the past, and clampTTL raises it to 1.
	ttl := clampTTL(p.chain.RoundTime(rd.Number+1).Sub(h.clock.Now()), 1, uint32(p.chain.Period))
	if err := setRoundRecord(m, owner, p.chain, rd, ttl); err != nil {
		h.log.Error("latest record encode failed", "beacon_id", p.chain.BeaconID, "round", rd.Number, "error", err)
		m.Rcode = dns.RcodeServerFailure
	}
}

// answerRound answers a query at the round name that owner spells and p describes.
// A future round returns NODATA without a Source call, so that a client cannot cause an upstream request (FR-answers-16).
// A Source status of Unavailable, and any failure to build the record, return SERVFAIL.
func (h *handler) answerRound(m *dns.Msg, owner string, p parsedName, qtype uint16) {
	if qtype != dns.TypeTXT {
		h.setNoData(m, ttlOtherType)
		return
	}
	now := h.clock.Now()
	current, err := p.chain.RoundAt(now)
	if err != nil {
		m.Rcode = dns.RcodeServerFailure
		return
	}
	if p.round > current {
		h.setNoData(m, clampTTL(p.chain.RoundTime(p.round).Sub(now), 1, ttlFutureMax))
		return
	}
	// NotPublished from Source cannot tell a future round from a due round, so the comparison above decides.
	rd, status := h.src.Round(h.queryCtx, p.chain, p.round)
	switch status {
	case Found:
		if err := setRoundRecord(m, owner, p.chain, rd, ttlRound); err != nil {
			h.log.Error("round record encode failed", "beacon_id", p.chain.BeaconID, "round", p.round, "error", err)
			m.Rcode = dns.RcodeServerFailure
		}
	case NotPublished:
		h.setNoData(m, ttlDue)
	default:
		m.Rcode = dns.RcodeServerFailure
	}
}

// setRoundRecord adds one TXT record that carries round rd of chain c to the answer section of m.
// The owner is the name as the client wrote it. ttl is the record TTL in seconds.
// It returns an error when the round does not encode, and then leaves m unchanged.
func setRoundRecord(m *dns.Msg, owner string, c chain.Chain, rd chain.Round, ttl uint32) error {
	raw, err := record.Encode(c, record.Body{Version: 1, Number: rd.Number, Sig: rd.Sig, PrevSig: rd.PrevSig})
	if err != nil {
		return err
	}
	m.Answer = append(m.Answer, &dns.TXT{
		Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttl},
		Txt: record.ToStrings(raw),
	})
	return nil
}

// setNoData makes m a NODATA answer with the zone SOA in the authority section.
// The SOA TTL and the SOA MINIMUM both equal ttl, which is the negative TTL (RFC 2308 section 3).
func (h *handler) setNoData(m *dns.Msg, ttl uint32) {
	m.Ns = append(m.Ns, h.soa(ttl, ttl))
}

// clampTTL returns d in whole seconds, rounded up and limited to the range lo to hi.
// A TTL must not extend past the round time, so it never rounds down.
// time.Time.Sub saturates for a distant round, and the saturated value falls to hi.
func clampTTL(d time.Duration, lo, hi uint32) uint32 {
	secs := int64(d / time.Second)
	if d%time.Second > 0 {
		secs++
	}
	return uint32(min(max(secs, int64(lo)), int64(hi)))
}

// answerApex adds the SOA or the NS records to m when qtype asks for them.
func (h *handler) answerApex(m *dns.Msg, qtype uint16) {
	switch qtype {
	case dns.TypeSOA:
		m.Answer = append(m.Answer, h.soa(zoneTTL, apexSOAMinimum))
	case dns.TypeNS:
		for _, ns := range h.ns {
			m.Answer = append(m.Answer, &dns.NS{
				Hdr: dns.RR_Header{Name: h.zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: zoneTTL},
				Ns:  dns.Fqdn(ns),
			})
		}
	default:
		h.setNoData(m, ttlOtherType)
	}
}

// soa returns the zone SOA record with the given TTL and MINIMUM.
func (h *handler) soa(ttl, minimum uint32) *dns.SOA {
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: h.zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: ttl},
		Ns:      dns.Fqdn(h.ns[0]),
		Mbox:    dns.Fqdn(h.soaRname),
		Serial:  1,
		Refresh: 3600,
		Retry:   600,
		Expire:  604800,
		Minttl:  minimum,
	}
}

// effectiveType returns the QTYPE that the gateway answers.
// ANY at a round name or a latest name becomes TXT (RFC 8482 section 4.1).
func effectiveType(kind nameKind, qtype uint16) uint16 {
	if qtype == dns.TypeANY && (kind == nameRound || kind == nameLatest) {
		return dns.TypeTXT
	}
	return qtype
}

// finish adds the OPT record when the query r has one, and truncates a UDP answer to the size that the client permits.
// The DO bit stays clear, because the zone has no DNSSEC.
func (h *handler) finish(w dns.ResponseWriter, r, m *dns.Msg) {
	size := dns.MinMsgSize
	if opt := r.IsEdns0(); opt != nil {
		m.SetEdns0(ednsUDPSize, false)
		size = int(opt.UDPSize())
	}
	if _, isUDP := w.RemoteAddr().(*net.UDPAddr); isUDP {
		m.Truncate(size)
	}
	// Truncate clears Compress when the message fits uncompressed. Compression keeps UDP answers small.
	m.Compress = true
}
