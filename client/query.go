package client

import (
	"context"
	"fmt"

	"github.com/miekg/dns"
)

// ednsSize is the UDP size that a query advertises. It avoids IP fragmentation on most paths.
const ednsSize = 1232

// query sends one TXT query for name to the resolver and returns the answer records.
// It retries once over TCP when the answer has the TC bit. It sends no other query.
// A negative answer returns no records and no error.
// Each failure, and each RCODE other than NOERROR and NXDOMAIN, returns an error that wraps ErrTransport.
func (c *Client) query(ctx context.Context, name string) ([]dns.RR, error) {
	// One timeout covers the UDP query and the TCP retry.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	m := new(dns.Msg)
	m.SetQuestion(name, dns.TypeTXT)
	m.SetEdns0(ednsSize, false)

	r, err := c.exchange(ctx, "udp", m)
	if err == nil && r.Truncated {
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
		return nil, fmt.Errorf("%w: resolver answered %s", ErrTransport, dns.RcodeToString[r.Rcode])
	}
}

// exchange sends m to the resolver over network ("udp" or "tcp") and returns the answer.
// A cancel or a deadline of ctx closes the connection, because the library reads ctx.Deadline only.
func (c *Client) exchange(ctx context.Context, network string, m *dns.Msg) (*dns.Msg, error) {
	// The library read timeout defaults to 2 seconds, so the client sets its own limit.
	cl := &dns.Client{Net: network, Timeout: c.timeout}
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
