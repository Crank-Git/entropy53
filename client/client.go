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
// It returns an error for a malformed zone or resolver, a chain with no public key or a period below 1,
// or when no system resolver exists.
func New(cfg Config) (*Client, error) {
	return newClient(cfg, "/etc/resolv.conf")
}

// newClient is New with the path of the resolver file as an argument, so that a test can set the path.
func newClient(cfg Config, resolvConfPath string) (*Client, error) {
	if _, ok := dns.IsDomainName(cfg.Zone); !ok || dns.Fqdn(cfg.Zone) == "." {
		return nil, fmt.Errorf("client: zone %q is not a valid domain name", cfg.Zone)
	}
	if len(cfg.Chain.PublicKey) == 0 {
		return nil, fmt.Errorf("client: chain %q has no public key", cfg.Chain.BeaconID)
	}
	if cfg.Chain.Period < 1 {
		return nil, fmt.Errorf("client: chain %q has a period below 1 second", cfg.Chain.BeaconID)
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

// Round returns the verified round with number n.
// It returns an argument error, with no query sent, when n is outside 1 to chain.MaxRound.
// It returns an error that wraps ErrTransport when the query fails.
// It returns ErrNotPublished or ErrWithheld for a negative answer, by the clock and the grace time.
// It returns ErrInvalid when no TXT record in the answer verifies as round n.
func (c *Client) Round(ctx context.Context, n uint64) (Round, error) {
	if n < 1 || n > chain.MaxRound {
		return Round{}, fmt.Errorf("client: round %d is outside 1 to %d", n, chain.MaxRound)
	}
	name := fmt.Sprintf("round.%d.%s.%s", n, c.cfg.Chain.BeaconID, c.zone)
	answer, err := c.query(ctx, name)
	if err != nil {
		return Round{}, err
	}
	if len(answer) == 0 {
		return Round{}, c.negative(n)
	}
	return c.verifyRecords(answer, n)
}
