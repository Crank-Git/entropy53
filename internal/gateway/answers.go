package gateway

import (
	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// handler answers DNS questions for the zone.
type handler struct {
	zone   string
	chains []chain.Chain
	src    Source
	clock  clock
}

// newHandler returns a handler for the zone in cfg. The handler reads rounds from src.
func newHandler(cfg ZoneConfig, src Source) *handler {
	return &handler{
		zone:   dns.CanonicalName(cfg.Zone),
		chains: cfg.Chains,
		src:    src,
		clock:  realClock{},
	}
}

// ServeDNS writes the answer to r. It never panics on a malformed question.
func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	if len(r.Question) != 1 {
		m.Rcode = dns.RcodeFormatError
		_ = w.WriteMsg(m)
		return
	}
	m.Authoritative = true
	m.Rcode = h.answer(r.Question[0])
	_ = w.WriteMsg(m)
}

// answer returns the RCODE for q.
// The question keeps its original letter case in the reply, because ServeDNS copies it unchanged.
func (h *handler) answer(q dns.Question) int {
	p := parseName(h.zone, h.chains, q.Name)
	switch p.kind {
	case nameOutside:
		return dns.RcodeRefused
	case nameNone:
		return dns.RcodeNameError
	case nameRound, nameLatest:
		if q.Qtype == dns.TypeTXT {
			// TODO(#23): Answer a round name with a round record.
			// TODO(#24): Answer a latest name with the latest round record.
			return dns.RcodeServerFailure
		}
	}
	return dns.RcodeSuccess
}
