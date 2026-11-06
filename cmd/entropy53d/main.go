// Command entropy53d is the gateway. It serves drand rounds over DNS.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/internal/gateway"
	"github.com/Crank-Git/entropy53/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// defaultUpstreams is the upstream list when no --upstream flag is set.
var defaultUpstreams = []string{
	"https://api.drand.sh",
	"https://api2.drand.sh",
	"https://api3.drand.sh",
	"https://drand.cloudflare.com",
}

// errBadFlag marks a flag parse failure. The flag package already wrote the message.
var errBadFlag = errors.New("bad flag")

// stringList is a flag that collects each value in order.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// settings holds the parsed command line.
type settings struct {
	showVersion bool
	zone        gateway.ZoneConfig
	upstream    gateway.Config
}

// parseFlags parses args and writes the usage and flag errors to errOut.
// It returns flag.ErrHelp for a help request and an error that wraps errBadFlag for a bad flag.
// Any other error is a value that no gateway can start with.
// It does not check the required flags, because --version and --help need none.
func parseFlags(args []string, errOut io.Writer) (settings, error) {
	var s settings
	var ns, upstreams stringList
	fs := flag.NewFlagSet("entropy53d", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.BoolVar(&s.showVersion, "version", false, "print the build version and exit")
	fs.StringVar(&s.zone.Zone, "zone", "", "the delegated zone (required)")
	fs.Var(&ns, "ns", "an NS host name of the zone, outside the zone (required, repeatable)")
	fs.StringVar(&s.zone.SOARname, "soa-rname", "", "the SOA RNAME, for example hostmaster.example.com. (required)")
	fs.StringVar(&s.zone.Listen, "listen", ":53", "the UDP and TCP listen address")
	chains := fs.String("chains", "quicknet,default,evmnet", "the chains to serve, separated by commas")
	fs.Var(&upstreams, "upstream", "an upstream base URL (repeatable, default: the four public drand relays)")
	fs.IntVar(&s.zone.UDPRate, "udp-rate", 2000, "the UDP answers per second for one source prefix")
	fs.IntVar(&s.upstream.Rate, "upstream-rate", 20, "the on-demand fetches per second for the gateway")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return s, err
		}
		return s, errors.Join(errBadFlag, err)
	}

	s.zone.NS = ns
	s.upstream.Upstreams = upstreams
	if len(upstreams) == 0 {
		s.upstream.Upstreams = defaultUpstreams
	}
	// Validate cannot reject a UDP rate of zero, because ZoneConfig reads zero as no limit.
	if s.zone.UDPRate < 1 {
		return s, fmt.Errorf("--udp-rate is %d, want 1 or more", s.zone.UDPRate)
	}
	for id := range strings.SplitSeq(*chains, ",") {
		c, err := chain.Lookup(id)
		if err != nil {
			return s, fmt.Errorf("--chains: %w", err)
		}
		s.zone.Chains = append(s.zone.Chains, c)
	}
	return s, nil
}

// run parses args, writes the result to out and returns the exit code.
// The flag package writes its own error and the usage to errOut, so run does not repeat them.
// A help request prints the usage once and returns 0. A bad flag returns 2.
// A startup failure writes one log line to out and returns 1.
// SIGTERM or SIGINT stops the gateway and returns 0.
func run(args []string, out, errOut io.Writer) int {
	s, err := parseFlags(args, errOut)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errBadFlag):
		return 2
	}
	if s.showVersion {
		if _, err := fmt.Fprintln(out, version.String()); err != nil {
			// The write to out failed, so a message to errOut is the only report left.
			_, _ = fmt.Fprintln(errOut, err)
			return 2
		}
		return 0
	}

	log := slog.New(slog.NewJSONHandler(out, nil))
	if err != nil {
		log.Error("invalid flag value", "error", err)
		return 1
	}
	s.zone.Log = log
	s.upstream.Log = log
	if err := s.zone.Validate(); err != nil {
		log.Error("invalid zone settings", "error", err)
		return 1
	}

	// The handler is installed before the gateway logs that it listens, so a signal after that line always reaches it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	upstreams := gateway.NewUpstreams(s.upstream)
	listeners, err := gateway.NewListeners(s.zone, upstreams)
	if err != nil {
		log.Error("cannot start the gateway", "error", err)
		return 1
	}
	log.Info("gateway listening", "addr", listeners.Addr(), "zone", s.zone.Zone)

	var followers sync.WaitGroup
	followers.Go(func() { upstreams.Follow(ctx, s.zone.Chains) })
	runErr := listeners.Run(ctx)
	// Run can end on its own, and then the followers still run.
	stop()
	followers.Wait()
	if runErr != nil {
		log.Error("the gateway stopped with an error", "error", runErr)
		return 1
	}
	return 0
}
