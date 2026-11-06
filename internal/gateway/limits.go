package gateway

import (
	"container/list"
	"net"
	"net/netip"
	"sync"

	"github.com/miekg/dns"
)

// udpLimitCapacity is the most source prefixes that a udpLimit tracks.
// Each entry is about 250 bytes, so the table stays near 16 MB.
const udpLimitCapacity = 65536

const (
	ipv4PrefixBits = 24
	ipv6PrefixBits = 56
)

// limitAction is the decision of udpLimit for one query.
type limitAction int

const (
	// limitAllow passes the query to the handler.
	limitAllow limitAction = iota
	// limitTruncate answers with only the header, the question and the TC bit.
	limitTruncate
	// limitDrop sends no answer.
	limitDrop
)

// limitEntry is the state of one source prefix.
type limitEntry struct {
	prefix netip.Prefix
	budget budget
	// excess counts the queries over the rate since the budget last had a token.
	excess int
}

// udpLimit limits the answers for each source prefix to rate per second (FR-answers-39, FR-answers-40).
// It keeps at most capacity prefixes and evicts the least recently used one.
type udpLimit struct {
	rate     int
	capacity int
	clk      clock

	mu      sync.Mutex
	order   *list.List // Front is the most recently used entry.
	entries map[netip.Prefix]*list.Element
}

// newUDPLimit returns a udpLimit for rate answers per second that tracks at most capacity prefixes.
func newUDPLimit(rate, capacity int, clk clock) *udpLimit {
	return &udpLimit{
		rate:     rate,
		capacity: capacity,
		clk:      clk,
		order:    list.New(),
		entries:  make(map[netip.Prefix]*list.Element),
	}
}

// prefixOf returns the /24 prefix of an IPv4 address and the /56 prefix of an IPv6 address.
// An IPv4-mapped IPv6 address uses the IPv4 prefix, so that one client has one budget on a dual-stack socket.
func prefixOf(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap().WithZone("")
	bits := ipv6PrefixBits
	if addr.Is4() {
		bits = ipv4PrefixBits
	}
	return netip.PrefixFrom(addr, bits).Masked()
}

// decide returns the action for one query from addr.
// Over the rate, the first excess query is truncated, the second is dropped, and so on (FR-answers-41, FR-answers-42).
func (l *udpLimit) decide(addr netip.Addr) limitAction {
	p := prefixOf(addr)
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	var e *limitEntry
	if el, ok := l.entries[p]; ok {
		l.order.MoveToFront(el)
		e = el.Value.(*limitEntry)
	} else {
		e = &limitEntry{prefix: p, budget: budget{rate: float64(l.rate)}}
		l.entries[p] = l.order.PushFront(e)
		if l.order.Len() > l.capacity {
			oldest := l.order.Back()
			l.order.Remove(oldest)
			delete(l.entries, oldest.Value.(*limitEntry).prefix)
		}
	}
	if e.budget.take(now) {
		e.excess = 0
		return limitAllow
	}
	e.excess++
	if e.excess%2 == 1 {
		return limitTruncate
	}
	return limitDrop
}

// limitHandler applies a udpLimit before it calls next. It serves the UDP socket only (FR-answers-43).
type limitHandler struct {
	limit *udpLimit
	next  dns.Handler
}

// ServeDNS passes the query to next, answers it with TC, or drops it.
// A query with no UDP source address has no key for the limit, so it passes to next.
func (h *limitHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	if udp, ok := w.RemoteAddr().(*net.UDPAddr); ok {
		if addr, ok := netip.AddrFromSlice(udp.IP); ok {
			switch h.limit.decide(addr) {
			case limitTruncate:
				m := new(dns.Msg).SetReply(r)
				m.Truncated = true
				// A failed write of a truncated answer has no recovery: the client retries over TCP.
				_ = w.WriteMsg(m)
				return
			case limitDrop:
				return
			}
		}
	}
	h.next.ServeDNS(w, r)
}
