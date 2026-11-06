package gateway

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/miekg/dns"
)

// ZoneConfig holds the DNS settings of the gateway.
type ZoneConfig struct {
	// Zone is the DNS domain that the parent delegates to the gateway, for example "beacon.example.".
	Zone string
	// Chains holds the chains that the gateway serves. A name under another chain returns NXDOMAIN.
	Chains []chain.Chain
	// NS holds the name server host names of the zone. The first value is the SOA MNAME.
	NS []string
	// SOARname is the SOA RNAME, the mailbox of the zone contact.
	SOARname string
	// Listen is the address for the UDP and TCP sockets, for example "0.0.0.0:53".
	// Port 0 selects a port that the OS assigns, and both sockets share it.
	Listen string
	// UDPRate is the most UDP answers per second for each source prefix. Zero sets no limit.
	UDPRate int
	// Log receives the diagnostics of the DNS listeners. A nil value selects slog.Default.
	Log *slog.Logger
}

// Validate reports why cfg cannot start a gateway, or nil when it can.
// It requires a zone, an SOA RNAME and at least one NS value, and it rejects an NS host name inside the zone (FR-answers-27).
// An in-zone name server needs glue records that the gateway does not serve.
func (c ZoneConfig) Validate() error {
	if c.Zone == "" {
		return errors.New("the zone is required")
	}
	if c.SOARname == "" {
		return errors.New("the SOA RNAME is required")
	}
	if len(c.NS) == 0 {
		return errors.New("the zone needs at least one NS value")
	}
	// A malformed name starts the gateway, and then every SOA and NS answer fails to pack.
	if _, ok := dns.IsDomainName(c.Zone); !ok {
		return fmt.Errorf("the zone %q is not a valid domain name", c.Zone)
	}
	if _, ok := dns.IsDomainName(c.SOARname); !ok {
		return fmt.Errorf("the SOA RNAME %q is not a valid domain name", c.SOARname)
	}
	for _, ns := range c.NS {
		if _, ok := dns.IsDomainName(ns); !ok {
			return fmt.Errorf("the NS host name %q is not a valid domain name", ns)
		}
		if dns.IsSubDomain(dns.CanonicalName(c.Zone), dns.CanonicalName(ns)) {
			return fmt.Errorf("the NS host name %q is inside the zone %q", ns, c.Zone)
		}
	}
	return nil
}
