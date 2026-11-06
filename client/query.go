package client

import (
	"context"
	"fmt"
	"strconv"

	"github.com/miekg/dns"
)

// ednsSize is the UDP size that a query advertises. It avoids IP fragmentation on most paths.
const ednsSize = 1232

// query sends one TXT query for name to the resolver and returns the answer records.
// It retries once over TCP when the answer has the TC bit. It sends no other query.
// The caller sets the time limit with ctx.
// A negative answer returns no records and no error.
// Each failure, and each RCODE other than NOERROR and NXDOMAIN, returns an error that wraps ErrTransport.
func (c *Client) query(ctx context.Context, name string) ([]dns.RR, error) {
	m := new(dns.Msg)
	m.SetQuestion(name, dns.TypeTXT)
	m.SetEdns0(ednsSize, false)

	r, err := c.exchange(ctx, "udp", m)
	// The library returns the header of a packet that it cannot unpack, so a cut TC answer also retries over TCP.
	if r != nil && r.Truncated {
		r, err = c.exchange(ctx, "tcp", m)
	}
	if err != nil {
		// The context error is the cause when the caller or the timeout ended the call.
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	switch r.Rcode {
	case dns.RcodeSuccess, dns.RcodeNameError:
		return r.Answer, nil
	default:
		name, ok := dns.RcodeToString[r.Rcode]
		if !ok {
			name = strconv.Itoa(r.Rcode)
		}
		return nil, fmt.Errorf("%w: resolver answered %s", ErrTransport, name)
	}
}

// exchange sends m to the resolver over network ("udp" or "tcp") and returns the answer.
// A cancel or a deadline of ctx closes the connection, because the library reads ctx.Deadline only.
func (c *Client) exchange(ctx context.Context, network string, m *dns.Msg) (*dns.Msg, error) {
	// The library read timeout defaults to 2 seconds, so the client sets its own limit.
	cl := &dns.Client{Net: network, Timeout: c.timeout, Dialer: c.dialer}
	co, err := cl.DialContext(ctx, c.resolver)
	if err != nil {
		return nil, err
	}
	defer func() { _ = co.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = co.Close() })
	defer stop()
	r, _, err := cl.ExchangeWithConnContext(context.Background(), m, co)
	return r, err
}
