package gateway

import (
	"log/slog"
	"net"

	"github.com/Crank-Git/entropy53/chain"
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
	case nameApex:
		h.answerApex(m, qtype)
	case nameRound, nameLatest:
		if qtype == dns.TypeTXT {
			// TODO(#23): Answer a round name with a round record.
			// TODO(#24): Answer a latest name with the latest round record.
			m.Rcode = dns.RcodeServerFailure
		}
	}
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
	m.RecursionAvailable = false
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
