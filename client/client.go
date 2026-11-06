package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// defaultTimeout is the time limit of one call when Config.Timeout is not set.
const defaultTimeout = 5 * time.Second

// Config holds the settings of a Client.
type Config struct {
	Zone     string
	Chain    chain.Chain
	Resolver string // "host:port". Empty means the system resolver.
	Timeout  time.Duration
	Clock    func() time.Time
}

// Round is one verified round.
type Round struct {
	Chain             string
	Number            uint64
	Randomness        [32]byte
	Signature         []byte
	PreviousSignature []byte
}

// The errors of the client library. Each returned error wraps one of these values.
var (
	ErrNotPublished = errors.New("round not published")
	ErrWithheld     = errors.New("round withheld")
	ErrInvalid      = errors.New("no record verifies")
	ErrStale        = errors.New("latest round is stale")
	ErrTransport    = errors.New("dns query failed")
)

// Client reads rounds from one resolver. It holds no state between calls.
type Client struct {
	cfg      Config
	zone     string // Fully qualified.
	resolver string // "host:port".
	timeout  time.Duration
	clock    func() time.Time
}

// New returns a Client for cfg.
// It uses the first system resolver when cfg.Resolver is empty.
// It returns an error for a malformed zone or resolver, or when no system resolver exists.
func New(cfg Config) (*Client, error) {
	return newClient(cfg, "/etc/resolv.conf")
}

// newClient is New with the path of the resolver file as an argument, so that a test can set the path.
func newClient(cfg Config, resolvConfPath string) (*Client, error) {
	if _, ok := dns.IsDomainName(cfg.Zone); !ok || dns.Fqdn(cfg.Zone) == "." {
		return nil, fmt.Errorf("client: zone %q is not a valid domain name", cfg.Zone)
	}
	resolver := cfg.Resolver
	if resolver == "" {
		var err error
		if resolver, err = systemResolver(resolvConfPath); err != nil {
			return nil, err
		}
	} else if err := checkHostPort(resolver); err != nil {
		return nil, fmt.Errorf("client: resolver %q: %w", resolver, err)
	}
	c := &Client{cfg: cfg, zone: dns.Fqdn(cfg.Zone), resolver: resolver, timeout: cfg.Timeout, clock: cfg.Clock}
	if c.timeout <= 0 {
		c.timeout = defaultTimeout
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	return c, nil
}

// checkHostPort returns an error unless addr is "host:port" with a host and a numeric port.
func checkHostPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("the host is empty")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("the port %q is not a number from 0 to 65535", port)
	}
	return nil
}

// systemResolver returns the first nameserver of the resolv.conf file at path as "host:port".
// It returns an error when the file is unreadable or holds no nameserver line.
func systemResolver(path string) (string, error) {
	conf, err := dns.ClientConfigFromFile(path)
	if err != nil {
		return "", fmt.Errorf("client: no system resolver: %w", err)
	}
	if len(conf.Servers) == 0 {
		return "", fmt.Errorf("client: no system resolver: %s has no nameserver line", path)
	}
	return net.JoinHostPort(conf.Servers[0], conf.Port), nil
}

// Round queries the round record for round number n.
// It returns an error that wraps ErrTransport when the query fails.
// Verification is not complete yet, so a successful query returns ErrInvalid and no round.
func (c *Client) Round(ctx context.Context, n uint64) (Round, error) {
	name := fmt.Sprintf("round.%d.%s.%s", n, c.cfg.Chain.BeaconID, c.zone)
	if _, err := c.query(ctx, name); err != nil {
		return Round{}, err
	}
	return Round{}, fmt.Errorf("%w: the client library does not verify records yet", ErrInvalid)
}
