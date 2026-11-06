package gateway

import "github.com/Crank-Git/entropy53/chain"

// ZoneConfig holds the DNS settings of the gateway.
type ZoneConfig struct {
	// Zone is the DNS domain that the parent delegates to the gateway, for example "beacon.example.".
	Zone string
	// Chains holds the chains that the gateway serves. A name under another chain returns NXDOMAIN.
	Chains []chain.Chain
}
