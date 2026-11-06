package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	tcp, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen on TCP %s: %w", cfg.Listen, err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("listen on UDP %s: %w", tcp.Addr(), err)
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

// Addr returns the address of both sockets, for example "127.0.0.1:5353".
func (l *Listeners) Addr() string {
	return l.tcp.Addr().String()
}

// Run serves queries until ctx ends, then stops both servers and waits for them.
// After ctx ends, in-flight queries have shutdownDrain to finish. Then Run cancels the Source calls that still wait,
// and the servers have shutdownClose to close (FR-answers-44).
// It returns nil after a stop that ctx caused. A server that ends on its own stops the other one, and Run returns the cause.
func (l *Listeners) Run(ctx context.Context) error {
	defer l.cancelQueries()
	started := make(chan struct{}, 2)
	notify := func() { started <- struct{}{} }
	servers := []*dns.Server{
		{Listener: l.tcp, Handler: l.h, MsgAcceptFunc: acceptQuery, NotifyStartedFunc: notify},
		{PacketConn: l.udp, Handler: l.udpH, MsgAcceptFunc: acceptQuery, NotifyStartedFunc: notify},
	}
	ended := make(chan error, len(servers))
	for _, s := range servers {
		go func() { ended <- s.ActivateAndServe() }()
	}
	running := len(servers)

	// ShutdownContext fails on a server that has not started, so Run waits for both start signals.
	var runErr error
	for n := 0; n < len(servers) && runErr == nil; {
		select {
		case <-started:
			n++
		case err := <-ended:
			running--
			runErr = endedError(err)
		}
	}
	if runErr == nil {
		select {
		case <-ctx.Done():
		case err := <-ended:
			running--
			runErr = endedError(err)
		}
	}

	runErr = errors.Join(runErr, l.shutdown(servers))
	for ; running > 0; running-- {
		if err := <-ended; err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("DNS server: %w", err))
		}
	}
	return runErr
}

// shutdown stops the servers within shutdownDrain plus shutdownClose.
// ShutdownContext closes the UDP socket when it returns, so the cancel of the queries must come first.
// A TCP connection that waits for a query ends at once, because ShutdownContext sets its read deadline.
func (l *Listeners) shutdown(servers []*dns.Server) error {
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

	errs := make(chan error, len(servers))
	for _, s := range servers {
		go func() { errs <- s.ShutdownContext(stop) }()
	}
	var err error
	for range servers {
		if e := <-errs; e != nil {
			err = errors.Join(err, fmt.Errorf("shut down DNS server: %w", e))
		}
	}
	return err
}

// endedError turns the end of a server before ctx ends into an error.
func endedError(err error) error {
	if err == nil {
		return errors.New("a DNS server ended before the context ended")
	}
	return fmt.Errorf("DNS server: %w", err)
}
