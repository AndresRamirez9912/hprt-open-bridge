//go:build !unix

package cp4x

import (
	"context"
	"net/netip"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// Discover is only implemented on Unix systems such as Linux and macOS
func (d *Driver) Discover(context.Context, netip.Addr) ([]printer.Discovered, error) {
	return nil, printer.ErrDiscovery.Wrap("not supported on this platform")
}
