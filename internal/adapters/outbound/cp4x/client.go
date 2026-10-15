package cp4x

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Compile time checks that the adapter implements the outbound ports
var (
	_ ports.PrinterDriver  = &Driver{}
	_ ports.PrinterSession = &Session{}
)

// Driver implements ports.PrinterDriver over the Wi-Fi protocol of the CP4x printers
type Driver struct {
	cfg   Config
	trace Tracer
}

// NewDriver returns a driver, zero fields of cfg take their default and trace may be nil
func NewDriver(cfg Config, trace Tracer) *Driver {
	// A no-op tracer avoids nil checks everywhere
	if trace == nil {
		trace = func(string) {}
	}
	return &Driver{cfg: cfg.withDefaults(), trace: trace}
}

// Connect opens the data and command connections and starts a session
//
// The printer saves power by sleeping its Wi-Fi radio, so while connecting the driver keeps
// knocking with small UDP packets and retries TCP until the wake window ends
func (d *Driver) Connect(ctx context.Context, ip netip.Addr, wake time.Duration) (ports.PrinterSession, error) {
	// The printers only speak IPv4
	ip = ip.Unmap()
	if !ip.Is4() {
		return nil, printer.ErrInvalidIP
	}
	if wake <= 0 {
		wake = d.cfg.WakeWindow
	}

	// The wake window bounds every attempt below, and the knocking stops with it
	ctx, cancel := context.WithTimeout(ctx, wake)
	defer cancel()
	stopKnocking := d.knock(ctx, ip)
	defer stopKnocking()

	// The data connection is opened first, then the command connection
	data, err := d.dialWithRetry(ctx, ip, d.cfg.DataPort)
	if err != nil {
		return nil, err
	}
	command, err := d.dialWithRetry(ctx, ip, d.cfg.CommandPort)
	if err != nil {
		data.Close()
		return nil, err
	}

	return startSession(d.cfg, d.trace, ip, data, command), nil
}

// dialWithRetry tries to open a TCP connection until it succeeds or ctx ends
func (d *Driver) dialWithRetry(ctx context.Context, ip netip.Addr, port int) (net.Conn, error) {
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	dialer := net.Dialer{Timeout: d.cfg.ConnectAttemptTimeout, KeepAlive: 15 * time.Second}
	reason := fmt.Sprintf("Timeout connecting to %s", addr)

	for attempt := 1; ctx.Err() == nil; attempt++ {
		// One attempt, bounded by ConnectAttemptTimeout
		conn, err := dialer.DialContext(ctx, "tcp4", addr)
		if err == nil {
			if attempt > 1 {
				d.trace(fmt.Sprintf("connected to %s after %d attempts", addr, attempt))
			}
			return conn, nil
		}

		// Something answered at that address but it does not accept the connection: give up now
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, printer.ConnectFailed(ip.String(), fmt.Sprintf("%s refused the connection. Is this a CP4x printer?", ip))
		}

		// Remember the best explanation for the final error, with the text the web app translates
		if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
			reason = fmt.Sprintf("%s is unreachable (no ARP reply). The printer is asleep, on another network, or the router isolates 2.4 GHz and 5 GHz clients.", ip)
		} else if ctx.Err() == nil {
			reason = fmt.Sprintf("Timeout connecting to %s", addr)
		}

		// Trace the first attempt and then one in five
		if attempt == 1 || attempt%5 == 0 {
			d.trace(fmt.Sprintf("waiting for %s (attempt %d): %s", addr, attempt, reason))
		}

		// Pause before the next attempt, the end of ctx ends the wait at once
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}

	return nil, printer.ConnectFailed(ip.String(), reason)
}

// knock sends the discovery probe straight to the printer until ctx ends or stop is called
func (d *Driver) knock(ctx context.Context, ip netip.Addr) (stop func()) {
	// Knocking only helps: when the UDP socket cannot be opened there is nothing to stop
	target := net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, uint16(d.cfg.DiscoveryPort)))
	conn, err := net.DialUDP("udp4", nil, target)
	if err != nil {
		return func() {}
	}

	// A goroutine sends one probe per tick until its context is cancelled by stop or by ctx
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(d.cfg.KnockInterval)
		defer ticker.Stop()
		for {
			_, _ = conn.Write([]byte(DiscoveryProbe))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// stop ends the goroutine and releases the socket
	return func() {
		cancel()
		conn.Close()
	}
}
