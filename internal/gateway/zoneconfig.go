package gateway

import (
	"log/slog"

	"github.com/Crank-Git/entropy53/chain"
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
	// Log receives the diagnostics of the DNS listeners. A nil value selects slog.Default.
	Log *slog.Logger
}
