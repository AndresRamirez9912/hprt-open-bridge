package printer

import (
	"net/netip"
	"time"
)

// Info holds the identity of a printer as far as the bridge knows it
type Info struct {
	Name     string
	SN       string
	Model    string
	Firmware string
}

// Discovered is a printer that answered the discovery probe on the local network
type Discovered struct {
	IP   netip.Addr
	Name string
	SN   string
}

// ConnectRequest describes which printer to connect to and how long to wait for it
type ConnectRequest struct {
	IP   netip.Addr
	Info Info
	// WakeWindow limits how long to keep trying while the printer wakes up, zero means default
	WakeWindow time.Duration
}

// State is the snapshot of the bridge connection that clients display
type State struct {
	Connected    bool
	Reconnecting bool
	IP           netip.Addr
	Info         Info
	Status       *Status
	Link         *LinkStats
	Reserved     bool
	Printing     bool
}

// Disconnected is the state reported when no printer session exists
var Disconnected = State{}
