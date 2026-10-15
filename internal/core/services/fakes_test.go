package services

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// fakeSession is an in memory printer session driven by the tests
type fakeSession struct {
	ip netip.Addr
	// events is buffered so the tests can push events without a reader waiting
	events chan printer.SessionEvent
	// done is closed once, by Close or by lose
	done chan struct{}
	once sync.Once
	// print runs when Print is called, nil means the job succeeds at once
	print func(ctx context.Context, s *fakeSession) (printer.PrintResult, error)
}

// newFakeSession returns an open session for ip
func newFakeSession(ip netip.Addr) *fakeSession {
	return &fakeSession{ip: ip, events: make(chan printer.SessionEvent, 64), done: make(chan struct{})}
}

// Address returns the address of the fake printer
func (s *fakeSession) Address() netip.Addr { return s.ip }

// Status returns a fixed healthy status
func (s *fakeSession) Status() (printer.Status, bool) { return printer.Status{Battery: 80}, true }

// Link returns no heartbeat statistics
func (s *fakeSession) Link() *printer.LinkStats { return nil }

// Events delivers the events pushed by the tests
func (s *fakeSession) Events() <-chan printer.SessionEvent { return s.events }

// Done is closed by Close or by lose
func (s *fakeSession) Done() <-chan struct{} { return s.done }

// SerialNumber answers a fixed serial number
func (s *fakeSession) SerialNumber(context.Context) (string, error) { return "SN-FAKE", nil }

// FirmwareVersion answers a fixed firmware version
func (s *fakeSession) FirmwareVersion(context.Context) (string, error) { return "1.0.9.0", nil }

// Print runs the job behaviour chosen by the test
func (s *fakeSession) Print(ctx context.Context, _ *photo.Photo) (printer.PrintResult, error) {
	if s.print == nil {
		return printer.PrintResult{}, nil
	}
	return s.print(ctx, s)
}

// Close ends the session
func (s *fakeSession) Close() error {
	// sync.Once allows Close from several goroutines without closing done twice
	s.once.Do(func() { close(s.done) })
	return nil
}

// lose ends the session as a dropped connection would
func (s *fakeSession) lose() {
	// Report the cause first and then end the session, like the real adapter
	s.events <- printer.SessionEvent{Kind: printer.SessionLost, Err: printer.ErrConnectionLost}
	s.Close()
}

// fakeDriver hands out fake sessions and records the connection attempts
type fakeDriver struct {
	// mu guards every field, the service connects from the test and from its reconnection goroutine
	mu sync.Mutex
	// connect decides the result of each attempt, nil means always succeed
	connect  func(ip netip.Addr, attempt int) error
	found    []printer.Discovered
	attempts int
	sessions []*fakeSession
	// setup prepares every new session before it is returned
	setup func(s *fakeSession)
}

// Discover returns the printers set by the test
func (d *fakeDriver) Discover(context.Context, netip.Addr) ([]printer.Discovered, error) {
	return d.found, nil
}

// Connect returns a new fake session unless the test makes the attempt fail
func (d *fakeDriver) Connect(_ context.Context, ip netip.Addr, _ time.Duration) (ports.PrinterSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Let the test decide whether this attempt fails
	d.attempts++
	if d.connect != nil {
		if err := d.connect(ip, d.attempts); err != nil {
			return nil, err
		}
	}

	// Hand out a new session prepared by the test
	s := newFakeSession(ip)
	if d.setup != nil {
		d.setup(s)
	}
	d.sessions = append(d.sessions, s)
	return s, nil
}

// last returns the newest session handed out
func (d *fakeDriver) last() *fakeSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[len(d.sessions)-1]
}

// count returns how many sessions were handed out
func (d *fakeDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sessions)
}

// recorder is a publisher that keeps everything it receives
type recorder struct {
	// mu guards the fields below, the service publishes from several goroutines
	mu     sync.Mutex
	jobs   []printer.JobEvent
	logs   []string
	states int
}

// PublishState counts the published snapshots
func (r *recorder) PublishState(printer.State) {
	r.mu.Lock()
	r.states++
	r.mu.Unlock()
}

// PublishJob keeps the job events
func (r *recorder) PublishJob(e printer.JobEvent) {
	r.mu.Lock()
	r.jobs = append(r.jobs, e)
	r.mu.Unlock()
}

// Log keeps the log lines
func (r *recorder) Log(message string) {
	r.mu.Lock()
	r.logs = append(r.logs, message)
	r.mu.Unlock()
}

// phases returns the phases of the job events received so far
func (r *recorder) phases() []printer.JobPhase {
	// Copy under the lock, the service may still be publishing
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]printer.JobPhase, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j.Phase)
	}
	return out
}

// fakeInspector is a network inspector with fixed answers
type fakeInspector struct {
	pinged netip.Addr
}

// Ping records the target and answers a perfect link
func (f *fakeInspector) Ping(_ context.Context, ip netip.Addr) *network.PingReport {
	f.pinged = ip
	return &network.PingReport{Sent: 20}
}

// HostWifi answers a fixed network
func (f *fakeInspector) HostWifi(context.Context) *network.HostWifi {
	return &network.HostWifi{SSID: "Home"}
}

// Gateway answers no network
func (f *fakeInspector) Gateway(context.Context) (*network.Gateway, error) {
	return nil, network.ErrNoWifiNetwork
}

// ScanWifi answers no networks
func (f *fakeInspector) ScanWifi(context.Context) ([]network.WifiNetwork, error) { return nil, nil }

// fakeJobs is a job runner that records the photos it receives
type fakeJobs struct {
	printed   int
	cancelled bool
}

// RunJob counts the printed photos
func (f *fakeJobs) RunJob(context.Context, *photo.Photo) (printer.PrintResult, error) {
	f.printed++
	return printer.PrintResult{}, nil
}

// CancelJob records the cancel request
func (f *fakeJobs) CancelJob() bool {
	f.cancelled = true
	return true
}

// testConfig returns short timings for the printer service
func testConfig() PrinterConfig {
	cfg := DefaultPrinterConfig()
	cfg.ReconnectPause = 10 * time.Millisecond
	cfg.RetryWait = time.Second
	cfg.SerialSearch = 10 * time.Millisecond
	return cfg
}

// eventually waits until cond is true or fails after one second
func eventually(cond func() bool) bool {
	// Poll every few milliseconds, the service works in its own goroutines
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// jpeg is a minimal valid print file
var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}
