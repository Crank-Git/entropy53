package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

// Listeners serves the zone over UDP and TCP on one port.
type Listeners struct {
	tcp net.Listener
	udp net.PacketConn
	h   *handler
	// udpH serves the UDP socket. It is h, or h behind the UDP limit.
	udpH  dns.Handler
	clock clock
	// cancelQueries ends the context of each Source call.
	cancelQueries context.CancelFunc
}

const (
	// shutdownDrain is the time that Run lets in-flight queries finish after the context ends.
	shutdownDrain = 3 * time.Second
	// shutdownClose is the time that Run waits for the servers to close after it cancels the queries.
	shutdownClose = 1 * time.Second
)

// NewListeners binds the TCP and UDP sockets of cfg.Listen and returns Listeners that answer from src.
// It binds TCP first, so that port 0 gives one port to both sockets.
// The caller runs cfg.Validate first. NewListeners returns an error when a socket cannot bind. It starts no goroutine.
func NewListeners(cfg ZoneConfig, src Source) (*Listeners, error) {
	tcp, udp, err := bindPair(cfg.Listen, func(addr string) (net.PacketConn, error) {
		return net.ListenPacket("udp", addr)
	})
	if err != nil {
		return nil, err
	}
	h := newHandler(cfg, src)
	queryCtx, cancelQueries := context.WithCancel(context.Background())
	h.queryCtx = queryCtx
	var udpH dns.Handler = h
	if cfg.UDPRate > 0 {
		udpH = &limitHandler{limit: newUDPLimit(cfg.UDPRate, udpLimitCapacity, realClock{}), next: h}
	}
	return &Listeners{tcp: tcp, udp: udp, h: h, udpH: udpH, clock: realClock{}, cancelQueries: cancelQueries}, nil
}

// maxBindAttempts is the number of times that bindPair tries a port that the OS assigns.
const maxBindAttempts = 5

// bindPair binds TCP on listen, then binds UDP on the port of that TCP socket with bindUDP.
// Another process can already hold the UDP port that the OS assigned to TCP.
// When listen has port 0 and the UDP port is in use, bindPair closes the TCP socket and tries again with a new port.
// It makes at most maxBindAttempts attempts. An explicit port fails at once.
func bindPair(listen string, bindUDP func(addr string) (net.PacketConn, error)) (net.Listener, net.PacketConn, error) {
	_, port, _ := net.SplitHostPort(listen)
	attempts := 1
	if port == "0" {
		attempts = maxBindAttempts
	}
	for attempt := 1; ; attempt++ {
		tcp, err := net.Listen("tcp", listen)
		if err != nil {
			return nil, nil, fmt.Errorf("listen on TCP %s: %w", listen, err)
		}
		udp, err := bindUDP(tcp.Addr().String())
		if err == nil {
			return tcp, udp, nil
		}
		_ = tcp.Close()
		if attempt >= attempts || !errors.Is(err, syscall.EADDRINUSE) {
			return nil, nil, fmt.Errorf("listen on UDP %s: %w", tcp.Addr(), err)
		}
	}
}

// Addr returns the address of both sockets, for example "127.0.0.1:5353".
func (l *Listeners) Addr() string {
	return l.tcp.Addr().String()
}

// Run serves queries until ctx ends, then stops both servers and waits for them.
// After ctx ends, in-flight queries have shutdownDrain to finish. Then Run cancels the Source calls that still wait,
// and the servers have shutdownClose to close (FR-answers-44).
// A server that still waits for a Source that ignores its context after shutdownClose does not delay Run.
// Run writes one log line that names each such server and returns nil, so the process exits within 5 seconds.
// It returns nil after a stop that ctx caused. A server that ends on its own stops the other one, and Run returns the cause.
func (l *Listeners) Run(ctx context.Context) error {
	defer l.cancelQueries()
	started := make(chan struct{}, 2)
	notify := func() { started <- struct{}{} }
	tcp := &dns.Server{Listener: l.tcp, Handler: l.h, MsgAcceptFunc: acceptQuery, NotifyStartedFunc: notify}
	udp := &dns.Server{PacketConn: l.udp, Handler: l.udpH, MsgAcceptFunc: acceptQuery, NotifyStartedFunc: notify}
	servers := []*dns.Server{tcp, udp}
	pending := map[*dns.Server]string{tcp: "tcp", udp: "udp"}
	type endResult struct {
		srv *dns.Server
		err error
	}
	ended := make(chan endResult, len(servers))
	for _, s := range servers {
		go func() { ended <- endResult{s, s.ActivateAndServe()} }()
	}

	// ShutdownContext fails on a server that has not started, so Run waits for both start signals.
	var runErr error
	for n := 0; n < len(servers) && runErr == nil; {
		select {
		case <-started:
			n++
		case e := <-ended:
			delete(pending, e.srv)
			runErr = endedError(e.err)
		}
	}
	if runErr == nil {
		select {
		case <-ctx.Done():
		case e := <-ended:
			delete(pending, e.srv)
			runErr = endedError(e.err)
		}
	}

	abandoned, shutErr := l.shutdown(servers)
	runErr = errors.Join(runErr, shutErr)
	// A server that shutdown abandoned can wait on a Source forever, so Run does not wait for it.
	var unfinished []string
	for _, s := range servers {
		if _, ok := abandoned[s]; ok {
			if name, wait := pending[s]; wait {
				unfinished = append(unfinished, name)
			}
			delete(pending, s)
		}
	}
	for len(pending) > 0 {
		e := <-ended
		delete(pending, e.srv)
		if e.err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("DNS server: %w", e.err))
		}
	}
	if len(unfinished) > 0 {
		l.h.log.Warn("DNS servers did not finish within the close limit", "servers", unfinished)
	}
	return runErr
}

// shutdown stops the servers within shutdownDrain plus shutdownClose.
// It returns the servers that did not finish within that limit. That outcome is no error.
// ShutdownContext closes the UDP socket when it returns, so the cancel of the queries must come first.
// A TCP connection that waits for a query ends at once, because ShutdownContext sets its read deadline.
func (l *Listeners) shutdown(servers []*dns.Server) (map[*dns.Server]struct{}, error) {
	stop, forceStop := context.WithCancel(context.Background())
	defer forceStop()
	finished := make(chan struct{})
	defer close(finished)
	drain := l.clock.After(shutdownDrain)
	go func() {
		select {
		case <-drain:
		case <-finished:
			return
		}
		l.cancelQueries()
		select {
		case <-l.clock.After(shutdownClose):
			forceStop()
		case <-finished:
		}
	}()

	type stopResult struct {
		srv *dns.Server
		err error
	}
	results := make(chan stopResult, len(servers))
	for _, s := range servers {
		go func() { results <- stopResult{s, s.ShutdownContext(stop)} }()
	}
	var err error
	abandoned := make(map[*dns.Server]struct{})
	for range servers {
		r := <-results
		switch {
		case r.err == nil:
		case errors.Is(r.err, context.Canceled):
			abandoned[r.srv] = struct{}{}
		default:
			err = errors.Join(err, fmt.Errorf("shut down DNS server: %w", r.err))
		}
	}
	return abandoned, err
}

// endedError turns the end of a server before ctx ends into an error.
func endedError(err error) error {
	if err == nil {
		return errors.New("a DNS server ended before the context ended")
	}
	return fmt.Errorf("DNS server: %w", err)
}
