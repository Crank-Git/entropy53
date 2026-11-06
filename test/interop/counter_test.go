//go:build interop

package interop

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// forwardTimeout limits one exchange between the counter and the gateway.
const forwardTimeout = 5 * time.Second

// exchange is one query that the counter forwarded to the gateway.
// The names keep the letter case of the bytes on the wire.
type exchange struct {
	network string
	// query is the question name of the forwarded query, and reply is the question name of the answer.
	query, reply string
	// sentSize is the EDNS UDP payload size of the forwarded query. It is 0 when the query has no OPT record.
	sentSize uint16
}

// counter forwards the queries of a resolver to the gateway and counts them.
// The harness has this counter and the gateway does not, so that the gateway code stays free of test hooks.
type counter struct {
	port   int
	target string
	// ednsSize replaces the EDNS UDP payload size of each forwarded query. Zero keeps the query as it is.
	ednsSize uint16
	// truncateUDP makes the counter return only the header, the question and the TC bit for each UDP answer.
	// A real gateway never truncates a round answer, because the answer is smaller than the 512 bytes that every client accepts.
	truncateUDP bool

	mu    sync.Mutex
	log   []*exchange
	wg    sync.WaitGroup
	conns []io.Closer
}

// counterOption changes a counter.
type counterOption func(*counter)

// withEDNSSize makes the counter set the EDNS UDP payload size of each forwarded query.
func withEDNSSize(size uint16) counterOption {
	return func(c *counter) { c.ednsSize = size }
}

// withTruncatedUDP makes the counter answer each UDP query with a TC answer, so that the resolver must retry over TCP.
// The gateway still receives and answers the query.
func withTruncatedUDP() counterOption {
	return func(c *counter) { c.truncateUDP = true }
}

// truncatedReply returns r with the TC bit set and with no record in any answer section.
// The OPT record stays out too, as in the TC answer of the gateway limits.
func truncatedReply(r *dnsmessage.Message) ([]byte, error) {
	r.Truncated = true
	r.Answers, r.Authorities, r.Additionals = nil, nil, nil
	out, err := r.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack truncated reply: %w", err)
	}
	return out, nil
}

// newCounter listens on UDP and TCP on one port of ip and forwards each query to target.
// The cleanup closes the sockets and waits for the forwards that run.
func newCounter(t *testing.T, ip, target string, opts ...counterOption) *counter {
	t.Helper()
	tcp, udp, err := listenPair(ip)
	if err != nil {
		t.Fatalf("listen for the counter on %s: %v", ip, err)
	}
	c := &counter{port: tcp.Addr().(*net.TCPAddr).Port, target: target, conns: []io.Closer{tcp, udp}}
	for _, o := range opts {
		o(c)
	}
	c.wg.Add(2)
	go c.serveUDP(udp)
	go c.serveTCP(tcp)
	t.Cleanup(func() {
		c.mu.Lock()
		for _, cl := range c.conns {
			_ = cl.Close()
		}
		c.mu.Unlock()
		c.wg.Wait()
	})
	return c
}

// count returns the number of queries that reached the counter since the last reset.
func (c *counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.log)
}

// exchanges returns a copy of the queries since the last reset.
func (c *counter) exchanges() []exchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]exchange, len(c.log))
	for i, e := range c.log {
		out[i] = *e
	}
	return out
}

// reset forgets the queries so far. The harness calls it after the readiness probe of the resolver.
func (c *counter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log = nil
}

func (c *counter) serveUDP(pc net.PacketConn) {
	defer c.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		query := append([]byte(nil), buf[:n]...)
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			if reply, err := c.forward("udp", query); err == nil {
				_, _ = pc.WriteTo(reply, from)
			}
		}()
	}
}

func (c *counter) serveTCP(l net.Listener) {
	defer c.wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		c.mu.Lock()
		c.conns = append(c.conns, conn)
		c.mu.Unlock()
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer func() { _ = conn.Close() }()
			for {
				query, err := readFrame(conn)
				if err != nil {
					return
				}
				reply, err := c.forward("tcp", query)
				if err != nil {
					return
				}
				if err := writeFrame(conn, reply); err != nil {
					return
				}
			}
		}()
	}
}

// forward sends one query to the target over network and returns the answer.
// It counts the query before it contacts the target.
func (c *counter) forward(network string, query []byte) ([]byte, error) {
	var m dnsmessage.Message
	if err := m.Unpack(query); err != nil || len(m.Questions) != 1 {
		return nil, fmt.Errorf("unpack query: %v", err)
	}
	if c.ednsSize != 0 {
		setEDNSSize(&m, c.ednsSize)
		var err error
		if query, err = m.Pack(); err != nil {
			return nil, fmt.Errorf("pack query: %w", err)
		}
	}
	e := &exchange{network: network, query: m.Questions[0].Name.String(), sentSize: ednsSize(&m)}
	c.mu.Lock()
	c.log = append(c.log, e)
	c.mu.Unlock()

	conn, err := net.DialTimeout(network, c.target, forwardTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(forwardTimeout)); err != nil {
		return nil, err
	}
	var reply []byte
	if network == "tcp" {
		if err := writeFrame(conn, query); err != nil {
			return nil, err
		}
		reply, err = readFrame(conn)
	} else {
		if _, err = conn.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		var n int
		n, err = conn.Read(buf)
		reply = buf[:max(n, 0)]
	}
	if err != nil {
		return nil, err
	}
	var r dnsmessage.Message
	if err := r.Unpack(reply); err == nil && len(r.Questions) == 1 {
		c.mu.Lock()
		e.reply = r.Questions[0].Name.String()
		c.mu.Unlock()
		if c.truncateUDP && network == "udp" {
			return truncatedReply(&r)
		}
	}
	return reply, nil
}

// setEDNSSize sets the UDP payload size in the OPT record of m, and adds the record when m has none.
func setEDNSSize(m *dnsmessage.Message, size uint16) {
	for i := range m.Additionals {
		if m.Additionals[i].Header.Type == dnsmessage.TypeOPT {
			m.Additionals[i].Header.Class = dnsmessage.Class(size)
			return
		}
	}
	var h dnsmessage.ResourceHeader
	_ = h.SetEDNS0(int(size), dnsmessage.RCodeSuccess, false)
	m.Additionals = append(m.Additionals, dnsmessage.Resource{Header: h, Body: &dnsmessage.OPTResource{}})
}

// ednsSize returns the UDP payload size in the OPT record of m, or 0 when m has none.
func ednsSize(m *dnsmessage.Message) uint16 {
	for _, a := range m.Additionals {
		if a.Header.Type == dnsmessage.TypeOPT {
			return uint16(a.Header.Class)
		}
	}
	return 0
}

func readFrame(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeFrame(w io.Writer, msg []byte) error {
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
	_, err := w.Write(append(frame, msg...))
	return err
}

func TestCounterForwardsAndCountsUDPAndTCP(t *testing.T) {
	g := startGateway(t)
	c := newCounter(t, "127.0.0.1", g.listeners.Addr())
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port))
	for _, network := range []string{"udp", "tcp"} {
		m, err := dnsQuery(network, addr, "BeAcOn.example.", dnsmessage.TypeSOA)
		if err != nil {
			t.Fatalf("%s query: %v", network, err)
		}
		if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 1 {
			t.Errorf("%s: rcode %v with %d answers, want NOERROR and 1 SOA", network, m.RCode, len(m.Answers))
		}
	}
	got := c.exchanges()
	if len(got) != 2 || c.count() != 2 {
		t.Fatalf("exchanges = %+v, want 2", got)
	}
	for i, network := range []string{"udp", "tcp"} {
		if got[i].network != network || got[i].query != "BeAcOn.example." || got[i].reply != got[i].query {
			t.Errorf("exchange %d = %+v, want %s with the query case kept in the answer", i, got[i], network)
		}
	}
	c.reset()
	if c.count() != 0 {
		t.Errorf("count after reset = %d, want 0", c.count())
	}
}

func TestCounterTruncatesUDPAndKeepsTCPWhenAsked(t *testing.T) {
	g := startGateway(t)
	c := newCounter(t, "127.0.0.1", g.listeners.Addr(), withTruncatedUDP())
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port))
	udp, err := dnsQuery("udp", addr, "beacon.example.", dnsmessage.TypeSOA)
	if err != nil {
		t.Fatalf("udp query: %v", err)
	}
	if !udp.Truncated || len(udp.Answers) != 0 || len(udp.Questions) != 1 {
		t.Errorf("udp: TC %v, %d answers, %d questions, want TC, 0 and 1", udp.Truncated, len(udp.Answers), len(udp.Questions))
	}
	tcp, err := dnsQuery("tcp", addr, "beacon.example.", dnsmessage.TypeSOA)
	if err != nil {
		t.Fatalf("tcp query: %v", err)
	}
	if tcp.Truncated || len(tcp.Answers) != 1 {
		t.Errorf("tcp: TC %v, %d answers, want no TC and 1 SOA", tcp.Truncated, len(tcp.Answers))
	}
}

func TestCounterSetsEDNSSizeWhenAsked(t *testing.T) {
	g := startGateway(t)
	plain := newCounter(t, "127.0.0.1", g.listeners.Addr())
	sized := newCounter(t, "127.0.0.1", g.listeners.Addr(), withEDNSSize(512))
	for _, c := range []*counter{plain, sized} {
		if _, err := dnsQuery("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port)), "beacon.example.", dnsmessage.TypeSOA); err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if got := plain.exchanges()[0].sentSize; got != 0 {
		t.Errorf("default counter sent EDNS size %d, want the query unchanged", got)
	}
	if got := sized.exchanges()[0].sentSize; got != 512 {
		t.Errorf("counter with withEDNSSize(512) sent EDNS size %d, want 512", got)
	}
}
