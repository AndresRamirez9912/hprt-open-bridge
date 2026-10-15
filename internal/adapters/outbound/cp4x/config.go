package cp4x

import "time"

// Config holds the ports and timings of the adapter, zero fields take their default value
type Config struct {
	// DataPort receives the JPEG, CommandPort carries commands and DiscoveryPort the UDP probe
	DataPort      int
	CommandPort   int
	DiscoveryPort int

	// ConnectAttemptTimeout bounds each TCP connection attempt
	ConnectAttemptTimeout time.Duration
	// WakeWindow is how long Connect keeps retrying while the printer wakes its Wi-Fi
	WakeWindow time.Duration
	// KnockInterval is how often a small UDP packet is sent to the printer while connecting
	KnockInterval time.Duration

	// HeartbeatInterval is how often a status query keeps the session alive
	HeartbeatInterval time.Duration
	// HeartbeatTimeout ends the session when the printer has been silent this long
	HeartbeatTimeout time.Duration
	// PingLost counts a status query without answer after this long as a lost heartbeat
	PingLost time.Duration
	// LinkSamples is how many heartbeats the link statistics cover
	LinkSamples int

	// ClaimTimeout bounds the wait for the print reservation and ClaimResend repeats the request
	ClaimTimeout time.Duration
	ClaimResend  time.Duration

	// PrintTimeout bounds a whole job, from the reservation to the end of printing
	PrintTimeout time.Duration
	// SendStall gives up when not a single byte of the image could be sent for this long
	SendStall time.Duration
	// SilentPrintDone ends a job as unconfirmed when the printer reports nothing for this long
	SilentPrintDone time.Duration
	// ChunkSize is the size of each write of the image
	ChunkSize int

	// DiscoveryInterval is how often the discovery probe is sent
	DiscoveryInterval time.Duration
	// EventBuffer is the capacity of the session events channel
	EventBuffer int
}

// DefaultConfig returns the values tested with a real CP4100
func DefaultConfig() Config {
	return Config{
		// Ports of the printer
		DataPort:      9100,
		CommandPort:   9901,
		DiscoveryPort: 6000,

		// Opening a session
		ConnectAttemptTimeout: 3 * time.Second,
		WakeWindow:            45 * time.Second,
		KnockInterval:         250 * time.Millisecond,

		// Keeping it alive and measuring the link
		HeartbeatInterval: 3 * time.Second,
		HeartbeatTimeout:  20 * time.Second,
		PingLost:          10 * time.Second,
		LinkSamples:       40,

		// Print jobs
		ClaimTimeout:    15 * time.Second,
		ClaimResend:     3 * time.Second,
		PrintTimeout:    240 * time.Second,
		SendStall:       120 * time.Second,
		SilentPrintDone: 90 * time.Second,
		ChunkSize:       8 * 1024,

		// Discovery and events
		DiscoveryInterval: 3 * time.Second,
		EventBuffer:       64,
	}
}

// withDefaults fills every zero field with its default value
func (c Config) withDefaults() Config {
	// Helpers that replace a zero or negative value with its default
	d := DefaultConfig()
	setInt := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	setDur := func(v *time.Duration, def time.Duration) {
		if *v <= 0 {
			*v = def
		}
	}

	// Every field of the config
	setInt(&c.DataPort, d.DataPort)
	setInt(&c.CommandPort, d.CommandPort)
	setInt(&c.DiscoveryPort, d.DiscoveryPort)
	setDur(&c.ConnectAttemptTimeout, d.ConnectAttemptTimeout)
	setDur(&c.WakeWindow, d.WakeWindow)
	setDur(&c.KnockInterval, d.KnockInterval)
	setDur(&c.HeartbeatInterval, d.HeartbeatInterval)
	setDur(&c.HeartbeatTimeout, d.HeartbeatTimeout)
	setDur(&c.PingLost, d.PingLost)
	setInt(&c.LinkSamples, d.LinkSamples)
	setDur(&c.ClaimTimeout, d.ClaimTimeout)
	setDur(&c.ClaimResend, d.ClaimResend)
	setDur(&c.PrintTimeout, d.PrintTimeout)
	setDur(&c.SendStall, d.SendStall)
	setDur(&c.SilentPrintDone, d.SilentPrintDone)
	setInt(&c.ChunkSize, d.ChunkSize)
	setDur(&c.DiscoveryInterval, d.DiscoveryInterval)
	setInt(&c.EventBuffer, d.EventBuffer)
	return c
}

// Tracer receives one line for every raw protocol exchange, for the protocol log
type Tracer func(line string)
