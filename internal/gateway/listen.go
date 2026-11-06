package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/miekg/dns"
)

// Listeners serves the zone over UDP and TCP on one port.
type Listeners struct {
	tcp net.Listener
	udp net.PacketConn
	h   *handler
	// udpH serves the UDP socket. It is h, or h behind the UDP limit.
	udpH dns.Handler
}

// NewListeners binds the TCP and UDP sockets of cfg.Listen and returns Listeners that answer from src.
// It binds TCP first, so that port 0 gives one port to both sockets.
// It returns an error when a socket cannot bind. It starts no goroutine.
func NewListeners(cfg ZoneConfig, src Source) (*Listeners, error) {
	if len(cfg.NS) == 0 {
		return nil, errors.New("the zone needs at least one NS value")
	}
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
	var udpH dns.Handler = h
	if cfg.UDPRate > 0 {
		udpH = &limitHandler{limit: newUDPLimit(cfg.UDPRate, udpLimitCapacity, realClock{}), next: h}
	}
	return &Listeners{tcp: tcp, udp: udp, h: h, udpH: udpH}, nil
}

// Addr returns the address of both sockets, for example "127.0.0.1:5353".
func (l *Listeners) Addr() string {
	return l.tcp.Addr().String()
}

// Run serves queries until ctx ends, then stops both servers and waits for them.
// It returns nil after a stop that ctx caused. A server that ends on its own stops the other one, and Run returns the cause.
func (l *Listeners) Run(ctx context.Context) error {
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

	for _, s := range servers {
		if err := s.ShutdownContext(context.Background()); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shut down DNS server: %w", err))
		}
	}
	for ; running > 0; running-- {
		if err := <-ended; err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("DNS server: %w", err))
		}
	}
	return runErr
}

// endedError turns the end of a server before ctx ends into an error.
func endedError(err error) error {
	if err == nil {
		return errors.New("a DNS server ended before the context ended")
	}
	return fmt.Errorf("DNS server: %w", err)
}
