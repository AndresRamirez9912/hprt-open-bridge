//go:build unix

package cp4x

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// Discover sends the probe every DiscoveryInterval and collects the replies until ctx ends
//
// Printers answer to the port the probe came from with the text name,serial and the
// printer address is the sender address of the reply
func (d *Driver) Discover(ctx context.Context, target netip.Addr) ([]printer.Discovered, error) {
	// Printers answer to the discovery port itself, so the probe must be sent from it
	conn, err := d.listenDiscovery(ctx)
	if err != nil {
		return nil, printer.ErrDiscovery.Wrap(err.Error())
	}
	defer conn.Close()

	// Probe one address or every broadcast address of this machine
	targets := broadcastAddresses()
	if target.IsValid() {
		targets = []netip.Addr{target.Unmap()}
	}

	// A goroutine sends the probes and closes conn when ctx ends, which unblocks the read loop below
	go d.sendProbes(ctx, conn, targets)

	// Collect the replies, one entry per printer address
	found := map[netip.Addr]printer.Discovered{}
	buf := make([]byte, 1024)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			break // the socket was closed because ctx ended
		}
		if p, ok := parseDiscoveryReply(buf[:n], from.Addr()); ok {
			found[p.IP] = p
		}
	}

	// Return the printers in a slice
	out := make([]printer.Discovered, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	return out, nil
}

// listenDiscovery opens the discovery port with SO_REUSEADDR so other programs can share it
func (d *Driver) listenDiscovery(ctx context.Context) (*net.UDPConn, error) {
	// Set SO_REUSEADDR on the raw socket before it is bound
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var sockErr error
		err := raw.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		})
		if err != nil {
			return err
		}
		return sockErr
	}}

	// Bind the discovery port on every interface
	pc, err := lc.ListenPacket(ctx, "udp4", ":"+strconv.Itoa(d.cfg.DiscoveryPort))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

// sendProbes sends the probe now and every DiscoveryInterval, then closes conn when ctx ends
//
// It runs in its own goroutine during the whole discovery
func (d *Driver) sendProbes(ctx context.Context, conn *net.UDPConn, targets []netip.Addr) {
	ticker := time.NewTicker(d.cfg.DiscoveryInterval)
	defer ticker.Stop()

	for {
		// One probe to every target
		for _, addr := range targets {
			dst := netip.AddrPortFrom(addr, uint16(d.cfg.DiscoveryPort))
			_, _ = conn.WriteToUDPAddrPort([]byte(DiscoveryProbe), dst)
		}

		// Wait for the next round, or close the socket when the discovery time is over
		select {
		case <-ctx.Done():
			conn.Close()
			return
		case <-ticker.C:
		}
	}
}

// parseDiscoveryReply decodes name,serial and ignores our own probes and those of other clients
func parseDiscoveryReply(msg []byte, from netip.Addr) (printer.Discovered, bool) {
	// The probes of every client also arrive on the shared port
	text := string(msg)
	if strings.Contains(text, DiscoveryProbe) {
		return printer.Discovered{}, false
	}

	// The address of the printer is the sender of the reply
	ip := from.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() {
		return printer.Discovered{}, false
	}

	// The reply is name,serial and the serial may be missing
	name, sn, _ := strings.Cut(text, ",")
	return printer.Discovered{IP: ip, Name: strings.TrimSpace(name), SN: strings.TrimSpace(sn)}, true
}

// broadcastAddresses returns the limited broadcast address plus the one of every IPv4 interface
func broadcastAddresses() []netip.Addr {
	// The limited broadcast address reaches the local segment of the default interface
	out := []netip.Addr{netip.AddrFrom4([4]byte{255, 255, 255, 255})}

	// Add the directed broadcast address of every active IPv4 network
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}

			// Broadcast address = network address with every host bit set
			ip, mask := ipNet.IP.To4(), ipNet.Mask
			var bcast [4]byte
			for i := range bcast {
				bcast[i] = ip[i] | ^mask[i]
			}
			out = append(out, netip.AddrFrom4(bcast))
		}
	}
	return out
}
