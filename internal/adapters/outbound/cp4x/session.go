package cp4x

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// Session implements ports.PrinterSession, one connection made of a command and a data channel
//
// Concurrency: three goroutines run while the session is open. readCommands decodes what the
// printer sends, keepAlive sends the heartbeat and drainData discards what arrives on the data
// channel. They share the session state through mu, the atomics and the channels below
type Session struct {
	cfg   Config
	trace Tracer
	ip    netip.Addr
	// events is buffered and written without blocking, so a slow reader never stalls the session
	events chan printer.SessionEvent

	data    net.Conn
	command net.Conn
	// writeMu keeps the frames of two goroutines from mixing on the command connection
	writeMu sync.Mutex

	// mu guards status, lastReply, waiters and link
	mu        sync.Mutex
	status    *printer.Status
	lastReply time.Time
	waiters   []*waiter
	link      linkMeter

	// sending pauses the heartbeat while a job reserves the printer and sends the image
	sending atomic.Bool
	// printing is set while a Print call runs, CompareAndSwap allows only one at a time
	printing atomic.Bool

	// closed is closed once when the session ends, every goroutine and waiter watches it
	closed    chan struct{}
	closeOnce sync.Once
}

// waiter receives the messages that match a condition for code waiting on a reply
type waiter struct {
	match func(Message) bool
	// ch is buffered and written without blocking by readCommands
	ch chan Message
}

// heartbeatToken is the token of a plain status query that reserves nothing
var heartbeatToken = [2]byte{0, 0}

// startSession wraps two open connections and starts the background goroutines
func startSession(cfg Config, trace Tracer, ip netip.Addr, data, command net.Conn) *Session {
	s := &Session{
		cfg:       cfg,
		trace:     trace,
		ip:        ip,
		events:    make(chan printer.SessionEvent, cfg.EventBuffer),
		data:      data,
		command:   command,
		lastReply: time.Now(),
		link:      linkMeter{size: cfg.LinkSamples, lostAfter: cfg.PingLost},
		closed:    make(chan struct{}),
	}

	// The three goroutines of the session, they all end when closed is closed
	go s.readCommands()
	go s.keepAlive()
	go s.drainData()

	// Ask for a first status right away instead of waiting for the first heartbeat
	_ = s.sendStatusQuery(heartbeatToken)
	return s
}

// Address returns the IPv4 address of the printer
func (s *Session) Address() netip.Addr { return s.ip }

// Events delivers the session notifications, new events are dropped when nobody reads
func (s *Session) Events() <-chan printer.SessionEvent { return s.events }

// Done is closed when the session ends
func (s *Session) Done() <-chan struct{} { return s.closed }

// Close ends the session without reporting a lost connection
func (s *Session) Close() error {
	// sync.Once makes Close safe to call from several goroutines and more than once
	s.closeOnce.Do(func() {
		// Closing the channel wakes every goroutine and waiter that selects on it
		close(s.closed)

		// Closing the connections makes the blocked Read calls of the goroutines return
		s.data.Close()
		s.command.Close()
	})
	return nil
}

// Status returns the last status block, ok is false until the first one arrives
func (s *Session) Status() (printer.Status, bool) {
	// status is written by readCommands, so it is read under the lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// No status block has arrived yet
	if s.status == nil {
		return printer.Status{}, false
	}
	return *s.status, true
}

// Link returns the statistics of the recent heartbeats
func (s *Session) Link() *printer.LinkStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.link.stats(time.Now())
}

// emit publishes an event without ever blocking the session
func (s *Session) emit(e printer.SessionEvent) {
	// A non blocking send: when the buffer is full the event is dropped
	select {
	case s.events <- e:
	default:
	}
}

// fail ends the session because of an unexpected problem and reports it
func (s *Session) fail(err error) {
	// A non blocking receive on closed tells whether the session already ended
	select {
	case <-s.closed:
		return
	default:
	}

	// Report the cause first, then close so the reader sees the event before Done
	s.emit(printer.SessionEvent{Kind: printer.SessionLost, Err: err})
	s.Close()
}

// readCommands decodes everything the printer sends on the command channel
//
// It runs in its own goroutine until the command connection fails or is closed
func (s *Session) readCommands() {
	var parser Parser
	buf := make([]byte, 4096)
	for {
		// Read blocks until the printer sends something or the connection is closed
		n, err := s.command.Read(buf)
		if err != nil {
			s.fail(printer.ErrConnectionLost.Wrapf("command channel: %v", err))
			return
		}

		// Trace the raw bytes and decode every complete message
		s.trace("cmd rx " + hex.EncodeToString(buf[:n]))
		for _, msg := range parser.Push(buf[:n]) {
			s.handleMessage(msg)
		}
	}
}

// handleMessage keeps the latest status, wakes the waiters and publishes the events
func (s *Session) handleMessage(msg Message) {
	// Update the shared state under the lock
	now := time.Now()
	s.mu.Lock()
	s.lastReply = now
	switch msg.Kind {
	case KindStatus:
		st := msg.Status
		s.status = &st
		s.link.pong(now)
	case KindErrors:
		// An error push updates the error flags of the last known status
		if s.status != nil {
			st := *s.status
			st.Errors = msg.Errors
			s.status = &st
		}
	}

	// Copy the waiters so they are notified after releasing the lock
	waiters := append([]*waiter(nil), s.waiters...)
	s.mu.Unlock()

	// Wake the waiters, a non blocking send never stalls the reader on a slow waiter
	for _, w := range waiters {
		if w.match(msg) {
			select {
			case w.ch <- msg:
			default:
			}
		}
	}

	// Publish the event of the message
	switch msg.Kind {
	case KindStatus:
		s.emit(printer.SessionEvent{Kind: printer.SessionStatus, Status: msg.Status})
	case KindStage:
		s.emit(printer.SessionEvent{Kind: printer.SessionStage, Stage: msg.Stage})
	case KindErrors:
		s.emit(printer.SessionEvent{Kind: printer.SessionErrors, Errors: msg.Errors})
	}
}

// keepAlive sends a status query every HeartbeatInterval, which also keeps the printer Wi-Fi awake
//
// It runs in its own goroutine until the session is closed
func (s *Session) keepAlive() {
	ticker := time.NewTicker(s.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		// Wait for the next tick, or stop when the session ends
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}

		// Never interleave commands with an image being sent
		if s.sending.Load() {
			continue
		}

		// A printer silent for too long is gone
		s.mu.Lock()
		silence := time.Since(s.lastReply)
		s.mu.Unlock()
		if silence > s.cfg.HeartbeatTimeout {
			s.fail(printer.ErrConnectionLost.Wrapf("no answer for %v", silence.Round(time.Second)))
			return
		}

		// Send the heartbeat, a write error means the connection is broken
		if err := s.sendStatusQuery(heartbeatToken); err != nil {
			s.fail(printer.ErrConnectionLost.Wrap(err.Error()))
			return
		}
	}
}

// drainData discards and traces anything the printer sends on the data channel
//
// It runs in its own goroutine, its Read also detects a data connection closed by the printer
func (s *Session) drainData() {
	buf := make([]byte, 1024)
	for {
		// Read blocks until the printer sends something or the connection is closed
		n, err := s.data.Read(buf)
		if err != nil {
			s.fail(printer.ErrConnectionLost.Wrapf("data channel: %v", err))
			return
		}

		// The printer is not expected to send anything here, trace it for the protocol log
		s.trace("data-port rx " + hex.EncodeToString(buf[:n]))
	}
}

// writeCommand sends one command frame
func (s *Session) writeCommand(cmd byte, payload []byte) error {
	frame := EncodeCommand(cmd, payload)

	// One writer at a time so frames from the heartbeat and from a job never mix
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// A deadline keeps a dead link from blocking the writer forever
	s.trace("cmd tx " + hex.EncodeToString(frame))
	_ = s.command.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := s.command.Write(frame)
	return err
}

// sendStatusQuery sends a status query, every one of them is also a link probe
func (s *Session) sendStatusQuery(token [2]byte) error {
	// Record the send time first so the answer can be paired with it
	s.mu.Lock()
	s.link.ping(time.Now())
	s.mu.Unlock()

	return s.writeCommand(CmdStatus, token[:])
}

// query sends a command and returns the payload of the first reply with the same id
func (s *Session) query(ctx context.Context, cmd byte) ([]byte, error) {
	// Register before sending so the reply cannot arrive unseen
	w := s.addWaiter(func(m Message) bool { return m.Kind == KindReply && m.Cmd == cmd })
	defer s.removeWaiter(w)

	if err := s.writeCommand(cmd, nil); err != nil {
		return nil, err
	}

	// Wait for whichever comes first: the reply, the caller giving up or the end of the session
	select {
	case msg := <-w.ch:
		return msg.Payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		return nil, printer.ErrConnectionLost
	}
}

// SerialNumber asks the printer for its serial number
func (s *Session) SerialNumber(ctx context.Context) (string, error) {
	payload, err := s.query(ctx, CmdQuerySN)
	if err != nil {
		return "", err
	}
	return cleanASCII(payload), nil
}

// FirmwareVersion asks the printer for its firmware, the 4 reply bytes are shown in reverse order
func (s *Session) FirmwareVersion(ctx context.Context) (string, error) {
	p, err := s.query(ctx, CmdQueryFirmware)
	if err != nil {
		return "", err
	}

	// Four bytes are expected, shown from the last to the first
	if len(p) < 4 {
		return "", printer.ErrShortReply.Wrapf("firmware reply %x", p)
	}
	return fmt.Sprintf("%d.%d.%d.%d", p[3], p[2], p[1], p[0]), nil
}

// addWaiter registers a waiter for the messages that match
func (s *Session) addWaiter(match func(Message) bool) *waiter {
	// The buffer keeps a few messages while the waiter is busy
	w := &waiter{match: match, ch: make(chan Message, 8)}

	s.mu.Lock()
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()
	return w
}

// removeWaiter forgets a waiter registered with addWaiter
func (s *Session) removeWaiter(w *waiter) {
	// waiters is read by readCommands, so it is changed under the lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove the waiter keeping the order of the others
	for i, x := range s.waiters {
		if x == w {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return
		}
	}
}

// claim reserves the printer for one job with a status query that carries ClaimToken
//
// A late answer to an earlier heartbeat is ignored and another non-zero token means
// that another device holds the printer, the request is repeated every ClaimResend
func (s *Session) claim(ctx context.Context) error {
	// Listen to every status block before asking
	token := ClaimToken(s.ip.As4())
	w := s.addWaiter(func(m Message) bool { return m.Kind == KindStatus })
	defer s.removeWaiter(w)

	// The claim has its own limit inside the limit of the whole job
	claimCtx, cancel := context.WithTimeout(ctx, s.cfg.ClaimTimeout)
	defer cancel()
	resend := time.NewTicker(s.cfg.ClaimResend)
	defer resend.Stop()

	// Ask for the reservation
	if err := s.sendStatusQuery(token); err != nil {
		return printer.ErrConnectionLost.Wrap(err.Error())
	}

	// Wait for the answer while repeating the request
	for {
		select {
		case msg := <-w.ch:
			switch msg.Status.Token {
			case token:
				// The printer echoed our token: the reservation is ours
				return nil
			case heartbeatToken:
				// A stale answer to a previous heartbeat, keep waiting
			default:
				return printer.ErrBusy
			}

		case <-resend.C:
			// The request or its answer may have been lost on a weak link
			if err := s.sendStatusQuery(token); err != nil {
				return printer.ErrConnectionLost.Wrap(err.Error())
			}

		case <-claimCtx.Done():
			// The job was cancelled, or the printer never answered in time
			if errors.Is(ctx.Err(), context.Canceled) {
				return printer.ErrCancelled
			}
			return printer.ErrNoClaimAnswer

		case <-s.closed:
			return printer.ErrConnectionLost
		}
	}
}

// Print reserves the printer, sends the photo and follows the printer until it finishes
func (s *Session) Print(ctx context.Context, p *photo.Photo) (printer.PrintResult, error) {
	// CompareAndSwap lets exactly one caller in, the others get ErrJobRunning at once
	if !s.printing.CompareAndSwap(false, true) {
		return printer.PrintResult{}, printer.ErrJobRunning
	}
	defer s.printing.Store(false)

	// The whole job has a time limit
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PrintTimeout)
	defer cancel()

	// Heartbeats pause from the reservation until the image is fully sent
	s.sending.Store(true)
	defer s.sending.Store(false)

	// Step 1: reserve the printer and check it can print
	if err := s.prepareJob(ctx); err != nil {
		return printer.PrintResult{}, err
	}

	// Subscribe before sending so no stage report can be missed
	progress := s.addWaiter(func(m Message) bool { return m.Kind == KindStatus || m.Kind == KindStage })
	defer s.removeWaiter(progress)

	// Step 2: send the image, half an image leaves the data port in a bad state so the session is rebuilt
	if err := s.sendImage(ctx, p.Bytes()); err != nil {
		s.fail(err)
		return printer.PrintResult{}, err
	}
	s.emit(printer.SessionEvent{Kind: printer.SessionSent, Progress: 100})

	// Sending may take long on a weak link: restart the heartbeat timeout and resume heartbeats
	s.mu.Lock()
	s.lastReply = time.Now()
	s.mu.Unlock()
	s.sending.Store(false)
	_ = s.sendStatusQuery(heartbeatToken)

	// Step 3: follow the printer until the job ends
	return s.waitForCompletion(ctx, progress)
}

// prepareJob reserves the printer and refuses the job when an error prevents printing
func (s *Session) prepareJob(ctx context.Context) error {
	if err := s.claim(ctx); err != nil {
		return err
	}

	// No paper, no ribbon or a jam: refuse before sending anything
	if st, ok := s.Status(); ok {
		if blocking := st.BlockingErrors(); len(blocking) > 0 {
			return printer.JobFailed(printer.StageIdle, blocking)
		}
	}
	return nil
}

// sendImage writes the JPEG in ChunkSize pieces and stops at once when ctx ends
func (s *Session) sendImage(ctx context.Context, jpeg []byte) error {
	// AfterFunc runs in its own goroutine when ctx ends: moving the deadline to now
	// makes a blocked Write return at once, which is how a cancel interrupts the send
	stop := context.AfterFunc(ctx, func() { _ = s.data.SetWriteDeadline(time.Now()) })
	defer stop()
	defer s.data.SetWriteDeadline(time.Time{})

	for start := 0; start < len(jpeg); start += s.cfg.ChunkSize {
		// Each chunk gets SendStall to go through, a lossy link may pause TCP for a long time
		end := min(start+s.cfg.ChunkSize, len(jpeg))
		if ctx.Err() == nil {
			_ = s.data.SetWriteDeadline(time.Now().Add(s.cfg.SendStall))
		}

		// A failed write is a cancel, a timeout of the job or a stalled link
		if _, err := s.data.Write(jpeg[start:end]); err != nil {
			switch {
			case errors.Is(ctx.Err(), context.Canceled):
				return printer.ErrCancelled
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				return printer.ErrPrintTimeout
			}
			return printer.SendStalled(int(s.cfg.SendStall.Seconds()))
		}

		// Report the progress of this chunk
		s.emit(printer.SessionEvent{Kind: printer.SessionSending, Progress: end * 100 / len(jpeg)})
	}
	return nil
}

// waitForCompletion follows the stages the printer reports
//
// A job ends with StageComplete, or with StageIdle after a printing stage. Some printers never
// report stages, then the job is assumed done after SilentPrintDone when no error showed up
func (s *Session) waitForCompletion(ctx context.Context, progress *waiter) (printer.PrintResult, error) {
	// The timer fires when the printer stays quiet for too long
	silence := time.NewTimer(s.cfg.SilentPrintDone)
	defer silence.Stop()

	last := printer.Stage(-1)
	reportedAnything, printingStarted := false, false
	for {
		// Wait for whichever comes first: a report, the silence timer, the job limit or the end of the session
		select {
		case msg := <-progress.ch:
			// A stage may come in a status block or in a stage push
			stage := msg.Stage
			if msg.Kind == KindStatus {
				stage = msg.Status.Stage
			}
			if stage == last {
				continue
			}
			last = stage
			reportedAnything = reportedAnything || stage != printer.StageIdle
			printingStarted = printingStarted || stage.IsPrinting()

			// Decide whether this stage ends the job
			switch {
			case stage == printer.StageComplete, stage == printer.StageIdle && printingStarted:
				s.emit(printer.SessionEvent{Kind: printer.SessionDone})
				return printer.PrintResult{}, nil
			case stage == printer.StageCancelled:
				return printer.PrintResult{}, printer.ErrCancelled
			case stage.IsFailure():
				st, _ := s.Status()
				return printer.PrintResult{}, printer.JobFailed(stage, st.Errors)
			}

		case <-silence.C:
			// A printer that reports progress is still working
			if reportedAnything {
				continue
			}

			// A silent printer with errors failed, without errors it is assumed done
			if st, ok := s.Status(); ok && len(st.Errors) > 0 {
				return printer.PrintResult{}, printer.JobFailed(printer.StageIdle, st.Errors)
			}
			s.emit(printer.SessionEvent{Kind: printer.SessionDone})
			return printer.PrintResult{Unconfirmed: true}, nil

		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return printer.PrintResult{}, printer.ErrPrintTimeout
			}
			return printer.PrintResult{}, printer.ErrCancelled

		case <-s.closed:
			return printer.PrintResult{}, printer.ErrConnectionLost
		}
	}
}
