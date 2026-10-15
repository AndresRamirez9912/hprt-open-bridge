package services

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Compile time checks that the service implements its inbound port and the job runner
var (
	_ ports.PrinterUseCase = &PrinterService{}
	_ jobRunner            = &PrinterService{}
)

// PrinterConfig holds the timings of the connection workflow
type PrinterConfig struct {
	// MinWake and MaxWake bound the wake window a client may ask for
	MinWake time.Duration
	MaxWake time.Duration
	// ReconnectPause is the wait between two reconnection attempts
	ReconnectPause time.Duration
	// RetryWait is how long a job interrupted before sending waits for the reconnection
	RetryWait time.Duration
	// SerialSearch is how long to look for the printer by serial number when its IP changed
	SerialSearch time.Duration
	// InfoQuery bounds the serial number and firmware queries made after connecting
	InfoQuery time.Duration
	// MaxDiscovery bounds the discovery time a client may ask for
	MaxDiscovery time.Duration
}

// DefaultPrinterConfig returns the timings of the Node bridge
func DefaultPrinterConfig() PrinterConfig {
	return PrinterConfig{
		// Limits of what a client may ask for
		MinWake:      3 * time.Second,
		MaxWake:      45 * time.Second,
		MaxDiscovery: 20 * time.Second,

		// Reconnection and retry of an interrupted job
		ReconnectPause: 5 * time.Second,
		RetryWait:      90 * time.Second,

		// Work done right after connecting
		SerialSearch: 8 * time.Second,
		InfoQuery:    2 * time.Second,
	}
}

// PrinterService keeps one printer connected, reconnects it when the link drops and runs the jobs
//
// Concurrency: HTTP handlers, the watch goroutine of each session and the reconnection loop
// all touch the current connection, so every read and write of conn and of the guarded
// fields of connection and job happens with mu held
type PrinterService struct {
	driver ports.PrinterDriver
	events publisher
	cfg    PrinterConfig

	// mu guards conn and the fields of connection and job marked as guarded
	mu   sync.Mutex
	conn *connection
}

// connection is the printer chosen by the client, it outlives the sessions of each reconnection
type connection struct {
	ip   netip.Addr
	info printer.Info
	wake time.Duration

	// ctx is cancelled when the client disconnects, which stops the reconnection loop and the running job
	ctx    context.Context
	cancel context.CancelFunc

	// session is the live session, nil while reconnecting (guarded by PrinterService.mu)
	session      ports.PrinterSession
	reconnecting bool
	// ready is closed when a session is attached, a retried job waits on it (guarded)
	ready chan struct{}
	// job is the running print job, nil when idle (guarded)
	job *job
}

// job is the print job running on a connection, its fields are guarded by PrinterService.mu
type job struct {
	cancel     context.CancelFunc
	size       int
	sent       bool
	cancelled  bool
	lastStage  *printer.Stage
	started    time.Time
	lastSent   int
	lastLogged int
}

// NewPrinterService returns a service that connects through driver and publishes on events
func NewPrinterService(driver ports.PrinterDriver, events publisher, cfg PrinterConfig) *PrinterService {
	return &PrinterService{driver: driver, events: events, cfg: cfg}
}

// Discover looks for printers for the given time, an invalid target means broadcast
func (s *PrinterService) Discover(ctx context.Context, timeout time.Duration, target netip.Addr) ([]printer.Discovered, error) {
	// The printers only speak IPv4
	if target.IsValid() && !target.Is4() {
		return nil, printer.ErrInvalidTarget
	}

	// The context deadline is what ends the search, bounded between one second and MaxDiscovery
	ctx, cancel := context.WithTimeout(ctx, min(max(timeout, time.Second), s.cfg.MaxDiscovery))
	defer cancel()

	return s.driver.Discover(ctx, target)
}

// Connect opens a session with a printer, looking it up by serial number when its IP changed
func (s *PrinterService) Connect(ctx context.Context, req printer.ConnectRequest) (printer.State, error) {
	// Validate the request and keep the wake window inside its limits
	if !req.IP.Is4() {
		return printer.State{}, printer.ErrInvalidIP
	}
	if req.WakeWindow > 0 {
		req.WakeWindow = min(max(req.WakeWindow, s.cfg.MinWake), s.cfg.MaxWake)
	}

	// Only one printer at a time: close the previous connection first
	s.Disconnect(ctx)

	// Try the address the client gave
	err := s.open(ctx, req)
	if err == nil {
		return s.State(), nil
	}

	// A short wake window means hotspot mode, where looking around the LAN is pointless
	if req.Info.SN == "" || req.WakeWindow > 0 {
		return printer.State{}, err
	}

	// The printer may have a new IP from DHCP: look it up on the LAN by its serial number
	s.events.Log("searching LAN for SN " + req.Info.SN)
	found, _ := s.Discover(ctx, s.cfg.SerialSearch, netip.Addr{})
	for _, f := range found {
		if f.SN != req.Info.SN || f.IP == req.IP {
			continue
		}

		// Same printer at another address: connect there
		req.IP, req.Info.Name, req.Info.SN = f.IP, f.Name, f.SN
		if err := s.open(ctx, req); err != nil {
			return printer.State{}, err
		}
		return s.State(), nil
	}

	return printer.State{}, err
}

// open connects to the printer of req and makes it the current connection
func (s *PrinterService) open(ctx context.Context, req printer.ConnectRequest) error {
	// Open the session, ctx is the one of the request so the client can give up while waiting
	session, err := s.driver.Connect(ctx, req.IP, req.WakeWindow)
	if err != nil {
		return err
	}

	// The connection lives on its own context, independent from the HTTP request that created it
	connCtx, cancel := context.WithCancel(context.Background())
	c := &connection{ip: req.IP, info: req.Info, wake: req.WakeWindow, ctx: connCtx, cancel: cancel, ready: make(chan struct{})}

	// Publish the connection under the lock so the other goroutines see a complete value
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()

	// Start following the session
	s.attach(c, session)
	s.events.Log("connected to " + req.IP.String())
	return nil
}

// Disconnect closes the session, stops any reconnection and cancels the running job
func (s *PrinterService) Disconnect(context.Context) printer.State {
	// Detach the connection under the lock, from now on nobody else can reach it
	s.mu.Lock()
	c := s.conn
	s.conn = nil
	s.mu.Unlock()
	if c == nil {
		return printer.Disconnected
	}

	// Cancelling the context stops the reconnection loop and the running job
	c.cancel()

	// Close the live session, read under the lock because the reconnection loop replaces it
	s.mu.Lock()
	session := c.session
	s.mu.Unlock()
	if session != nil {
		session.Close()
	}

	s.events.PublishState(printer.Disconnected)
	return printer.Disconnected
}

// State returns the current snapshot of the connection
func (s *PrinterService) State() printer.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

// stateLocked builds the snapshot, the caller holds s.mu
func (s *PrinterService) stateLocked() printer.State {
	// No printer chosen
	c := s.conn
	if c == nil {
		return printer.Disconnected
	}

	// What the service knows by itself
	st := printer.State{IP: c.ip, Info: c.info, Reconnecting: c.reconnecting, Printing: c.job != nil}
	if c.session == nil {
		return st
	}

	// A closed Done channel means the session already ended, a non blocking receive checks it
	select {
	case <-c.session.Done():
	default:
		st.Connected = true
	}

	// What the printer reported
	if status, ok := c.session.Status(); ok {
		st.Status = &status
		st.Reserved = status.Reserved()
	}
	st.Link = c.session.Link()
	return st
}

// publishState sends the current snapshot to the clients
func (s *PrinterService) publishState() {
	s.events.PublishState(s.State())
}

// attach makes session the live session of c and starts following it
func (s *PrinterService) attach(c *connection, session ports.PrinterSession) {
	s.mu.Lock()

	// The client may have disconnected while this session was being opened
	if c.ctx.Err() != nil {
		s.mu.Unlock()
		session.Close()
		return
	}

	// Install the session and wake every job waiting for the reconnection by closing ready
	c.session = session
	c.reconnecting = false
	close(c.ready)
	s.mu.Unlock()

	// One goroutine follows the events of this session, another reads the printer identity
	go s.watch(c, session)
	go s.readInfo(c, session)

	s.publishState()
}

// readInfo asks the new session for the serial number and firmware of the printer
func (s *PrinterService) readInfo(c *connection, session ports.PrinterSession) {
	// Serial number, the fields of c are written under the lock because State reads them
	ctx, cancel := context.WithTimeout(c.ctx, s.cfg.InfoQuery)
	sn, err := session.SerialNumber(ctx)
	cancel()
	if err == nil && sn != "" {
		s.mu.Lock()
		c.info.SN = sn
		s.mu.Unlock()
	}

	// Full firmware version
	ctx, cancel = context.WithTimeout(c.ctx, s.cfg.InfoQuery)
	fw, err := session.FirmwareVersion(ctx)
	cancel()
	if err == nil {
		s.mu.Lock()
		c.info.Firmware = fw
		s.mu.Unlock()
	}

	s.publishState()
}

// watch follows the events of one session and reconnects when it ends on its own
//
// It runs in its own goroutine for the whole life of the session
func (s *PrinterService) watch(c *connection, session ports.PrinterSession) {
	reason := "socket closed"

	// Handle events until the Done channel of the session is closed
	for done := false; !done; {
		select {
		case e := <-session.Events():
			if e.Kind == printer.SessionLost && e.Err != nil {
				reason = e.Err.Error()
			}
			s.handleSessionEvent(c, e)
		case <-session.Done():
			done = true
		}
	}

	// The last events may still be queued when Done closes: drain them without blocking
	for drained := false; !drained; {
		select {
		case e := <-session.Events():
			if e.Kind == printer.SessionLost && e.Err != nil {
				reason = e.Err.Error()
			}
		default:
			drained = true
		}
	}

	// A session that ended without a disconnect from the client is a dropped link
	if c.ctx.Err() == nil {
		s.reconnect(c, reason)
	}
}

// handleSessionEvent turns a session notification into client events and log lines
func (s *PrinterService) handleSessionEvent(c *connection, e printer.SessionEvent) {
	switch e.Kind {
	case printer.SessionStatus:
		// Every status refreshes the clients and may carry a new stage of the job
		s.onStage(c, e.Status.Stage)
		s.publishState()

	case printer.SessionStage:
		s.onStage(c, e.Stage)

	case printer.SessionErrors:
		// The printer pushed new error flags
		labels := strings.Join(printer.ErrorLabels(e.Errors), ", ")
		if labels == "" {
			labels = "no errors"
		}
		s.events.Log("printer reports: " + labels)
		s.publishState()

	case printer.SessionSending:
		s.onSending(c, e.Progress)

	case printer.SessionSent:
		// From now on the stages belong to this job and a dropped link cannot be retried
		s.mu.Lock()
		if c.job != nil {
			c.job.sent = true
			c.job.lastStage = nil
		}
		s.mu.Unlock()
		s.publishJob(printer.JobEvent{Phase: printer.PhaseSent, Progress: 100, Label: "Data sent, printing…"})
	}
}

// onStage publishes a stage event when the stage of the running job changes
func (s *PrinterService) onStage(c *connection, stage printer.Stage) {
	// Decide under the lock, publish after releasing it
	s.mu.Lock()
	j := c.job
	changed := j != nil && (j.lastStage == nil || *j.lastStage != stage)
	if changed {
		j.lastStage = &stage
	}
	s.mu.Unlock()

	if changed {
		s.publishJob(printer.JobEvent{Phase: printer.PhaseStage, Stage: &stage, Label: stage.Label()})
	}
}

// onSending publishes the sending progress with the transfer rate
func (s *PrinterService) onSending(c *connection, progress int) {
	s.mu.Lock()

	// No job means a late event of a job that already ended
	j := c.job
	if j == nil {
		s.mu.Unlock()
		return
	}

	// The session reports every chunk, the clients only need each new percentage
	if progress == j.lastSent {
		s.mu.Unlock()
		return
	}
	j.lastSent = progress

	// Transfer rate since the job started, at least half a second to avoid absurd first values
	elapsed := max(time.Since(j.started).Seconds(), 0.5)
	rate := int(float64(j.size*progress/100) / 1024 / elapsed)

	// The log only gets one line every ten percent
	logIt := progress-j.lastLogged >= 10
	if logIt {
		j.lastLogged = progress
	}
	s.mu.Unlock()

	// Publish after releasing the lock
	s.events.PublishJob(printer.JobEvent{Phase: printer.PhaseSending, Progress: progress, RateKBps: rate, Label: "Sending"})
	if logIt {
		s.events.Log(fmt.Sprintf("job: sending %d%% · %d KB/s", progress, rate))
	}
}

// publishJob sends a job event to the clients and writes it to the protocol log
func (s *PrinterService) publishJob(e printer.JobEvent) {
	s.events.PublishJob(e)

	// Same log line as the Node bridge: job: phase · label · error
	line := "job: " + string(e.Phase)
	if e.Label != "" {
		line += " · " + e.Label
	}
	if e.Error != "" {
		line += " · " + e.Error
	}
	s.events.Log(line)
}

// reconnect keeps trying to reopen the session of c until it works or the client disconnects
func (s *PrinterService) reconnect(c *connection, reason string) {
	// Mark the connection as reconnecting, ready gets a fresh channel that attach will close
	s.mu.Lock()
	c.session = nil
	c.reconnecting = true
	c.ready = make(chan struct{})
	s.mu.Unlock()

	s.events.Log(fmt.Sprintf("connection lost (%s), reconnecting…", reason))
	s.publishState()

	// Retry until a session opens or the client disconnects, which cancels c.ctx
	for c.ctx.Err() == nil {
		session, err := s.driver.Connect(c.ctx, c.ip, c.wake)
		if err == nil {
			s.attach(c, session)
			s.events.Log("reconnected")
			return
		}
		s.events.Log("reconnect failed: " + err.Error())

		// Wait before the next attempt, a disconnect ends the wait at once
		select {
		case <-c.ctx.Done():
		case <-time.After(s.cfg.ReconnectPause):
		}
	}
}

// RunJob prints one photo and, when the link dropped before sending, retries once after reconnecting
func (s *PrinterService) RunJob(_ context.Context, p *photo.Photo) (printer.PrintResult, error) {
	// Check under the lock that there is a live session and no other job
	s.mu.Lock()
	c := s.conn
	switch {
	case c == nil || c.session == nil:
		s.mu.Unlock()
		return printer.PrintResult{}, printer.ErrNoPrinter
	case c.job != nil:
		s.mu.Unlock()
		return printer.PrintResult{}, printer.ErrJobRunning
	}
	s.mu.Unlock()

	// The job belongs to the connection: it goes on when the HTTP client leaves and ends on Disconnect
	s.events.Log(fmt.Sprintf("printing %d bytes", p.Size()))
	result, retry, err := s.printOnce(c, p)

	// The printer discards half received images, so a job lost before sending is retried once
	if retry {
		s.publishJob(printer.JobEvent{Phase: printer.PhaseClaim, Label: "Link dropped — waiting for the printer to retry"})
		if s.waitReady(c) {
			s.events.Log("retrying print after reconnect")
			result, _, err = s.printOnce(c, p)
		}
	}

	// Final event of the job and a fresh snapshot
	if err != nil {
		s.publishJob(printer.JobEvent{Phase: printer.PhaseFailed, Error: err.Error()})
	} else {
		s.publishJob(printer.JobEvent{Phase: printer.PhaseDone, OK: true, Unconfirmed: result.Unconfirmed})
	}
	s.publishState()
	return result, err
}

// printOnce runs one attempt and reports whether it may be retried after a reconnection
func (s *PrinterService) printOnce(c *connection, p *photo.Photo) (printer.PrintResult, bool, error) {
	// The job context derives from the connection, so Disconnect and CancelJob both stop it
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	j := &job{cancel: cancel, size: p.Size(), started: time.Now(), lastSent: -1, lastLogged: -10}

	// Take the live session and register the job under the lock
	s.mu.Lock()
	session := c.session
	if session == nil {
		s.mu.Unlock()
		return printer.PrintResult{}, false, printer.ErrConnectionLost
	}
	c.job = j
	s.mu.Unlock()
	s.publishState()

	// Print, this blocks until the printer finishes or fails
	s.publishJob(printer.JobEvent{Phase: printer.PhaseClaim, Label: "Requesting printer"})
	result, err := session.Print(ctx, p)

	// Unregister the job and decide under the lock whether it can be retried
	s.mu.Lock()
	c.job = nil
	retry := err != nil && errors.Is(err, printer.ErrConnectionLost) && !j.sent && !j.cancelled && c.ctx.Err() == nil
	if j.cancelled {
		err = printer.ErrCancelled
	}
	s.mu.Unlock()

	return result, retry, err
}

// waitReady waits for the reconnection of c, giving the printer a moment to send its first status
func (s *PrinterService) waitReady(c *connection) bool {
	// Read the channel under the lock because reconnect replaces it
	s.mu.Lock()
	ready := c.ready
	s.mu.Unlock()

	// Wait for whichever comes first: the reconnection, a disconnect or the retry limit
	select {
	case <-ready:
	case <-c.ctx.Done():
		return false
	case <-time.After(s.cfg.RetryWait):
		return false
	}

	// Give the new session time to receive its first status block
	select {
	case <-c.ctx.Done():
		return false
	case <-time.After(1500 * time.Millisecond):
		return true
	}
}

// CancelJob aborts the running job and reports whether there was one
func (s *PrinterService) CancelJob() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Nothing to cancel
	if s.conn == nil || s.conn.job == nil {
		return false
	}

	// Mark it first so printOnce reports a cancel and not a lost connection, then stop it
	s.conn.job.cancelled = true
	s.conn.job.cancel()
	return true
}
