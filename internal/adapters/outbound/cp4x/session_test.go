package cp4x

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// fakePrinter is an in process printer on loopback used to test the adapter
//
// Concurrency: one goroutine serves the data connection and another the command connection,
// the job reports run in a third one, so the mutable state is guarded by mu
type fakePrinter struct {
	t       *testing.T
	data    net.Listener
	command net.Listener

	// Behaviour of the printer, set before connecting and only read afterwards
	stages     []printer.Stage
	errorFlags [3]byte
	foreign    [2]byte
	silent     bool
	serial     string
	slowData   bool

	// mu guards the fields below
	mu       sync.Mutex
	cmdConn  net.Conn
	token    [2]byte
	stage    printer.Stage
	received bytes.Buffer
	conns    []net.Conn
}

// newFakePrinter starts the two listeners of a fake printer
func newFakePrinter(t *testing.T) *fakePrinter {
	t.Helper()
	f := &fakePrinter{t: t, serial: "FAKE0001"}

	// Random free ports on loopback for the data and command connections
	var err error
	if f.data, err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if f.command, err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}

	// Everything is closed when the test ends
	t.Cleanup(f.close)
	return f
}

// config returns short timings that point the adapter to the fake printer
func (f *fakePrinter) config() Config {
	// The ports of the listeners, knocks go to a port nobody uses
	cfg := DefaultConfig()
	cfg.DataPort = f.data.Addr().(*net.TCPAddr).Port
	cfg.CommandPort = f.command.Addr().(*net.TCPAddr).Port
	cfg.DiscoveryPort = 1

	// Timings in milliseconds so the tests run fast
	cfg.WakeWindow = 2 * time.Second
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.HeartbeatTimeout = 400 * time.Millisecond
	cfg.ClaimTimeout = time.Second
	cfg.ClaimResend = 200 * time.Millisecond
	cfg.PrintTimeout = 5 * time.Second
	cfg.SilentPrintDone = 300 * time.Millisecond
	return cfg
}

// close stops the listeners and every accepted connection
func (f *fakePrinter) close() {
	// Closing the listeners ends the Accept calls of the serving goroutines
	f.data.Close()
	f.command.Close()

	// Closing the connections ends their Read calls
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
}

// serve accepts one data and one command connection and plays the printer side
func (f *fakePrinter) serve(imageSize int) {
	// Goroutine of the data connection: store the image and start the job once it is complete
	go func() {
		conn, err := f.data.Accept()
		if err != nil {
			return
		}
		f.track(conn)

		// A printer that never reads makes the sender block once the socket buffers are full
		if f.slowData {
			return
		}

		buf := make([]byte, 64*1024)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}

			// The buffer is read by the test goroutine, so it is written under the lock
			f.mu.Lock()
			f.received.Write(buf[:n])
			done := f.received.Len() >= imageSize
			f.mu.Unlock()

			// The job reports run in their own goroutine so this one keeps reading
			if done {
				go f.runJob()
			}
		}
	}()

	// Goroutine of the command connection: answer every command the session sends
	go func() {
		conn, err := f.command.Accept()
		if err != nil {
			return
		}
		f.track(conn)

		// runJob writes its pushes on this connection
		f.mu.Lock()
		f.cmdConn = conn
		f.mu.Unlock()

		var p Parser
		buf := make([]byte, 1024)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			for _, msg := range p.Push(buf[:n]) {
				f.answer(conn, msg)
			}
		}
	}()
}

// track remembers a connection so close can end it
func (f *fakePrinter) track(c net.Conn) {
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.mu.Unlock()
}

// answer replies to the status, serial number and firmware commands
func (f *fakePrinter) answer(conn net.Conn, msg Message) {
	switch msg.Cmd {
	case CmdStatus:
		// A silent printer never answers, to test the heartbeat timeout
		if f.silent {
			return
		}

		// A non-zero token is a reservation request, granted unless another device holds the printer
		f.mu.Lock()
		if tok := [2]byte(msg.Payload[:2]); tok != heartbeatToken && f.foreign == ([2]byte{}) {
			f.token = tok
		}
		token := f.token
		if f.foreign != ([2]byte{}) {
			token = f.foreign
		}
		block := statusBlock(87, f.stage, f.errorFlags, token)
		f.mu.Unlock()

		conn.Write(EncodeCommand(CmdStatus, block))

	case CmdQuerySN:
		conn.Write(EncodeCommand(CmdQuerySN, []byte(f.serial)))

	case CmdQueryFirmware:
		conn.Write(EncodeCommand(CmdQueryFirmware, []byte{4, 3, 2, 1}))
	}
}

// runJob reports the configured stages as pushes on the command connection
func (f *fakePrinter) runJob() {
	for _, st := range f.stages {
		// A little time between stages, like a real printer
		time.Sleep(20 * time.Millisecond)

		// The current stage is also answered in the status blocks
		f.mu.Lock()
		f.stage = st
		cmd := f.cmdConn
		f.mu.Unlock()

		// Push the stage as the printer does on its own
		if cmd != nil {
			payload := make([]byte, 4)
			binary.LittleEndian.PutUint32(payload, uint32(st))
			cmd.Write(EncodeCommand(CmdStagePush, payload))
		}
	}
}

// connect opens a session with the fake printer through the driver port
func connect(t *testing.T, f *fakePrinter, imageSize int) ports.PrinterSession {
	t.Helper()

	// Start the printer side, then connect through the port interface
	f.serve(imageSize)
	var drv ports.PrinterDriver = NewDriver(f.config(), nil)
	s, err := drv.Connect(context.Background(), netip.MustParseAddr("127.0.0.1"), 0)
	if err != nil {
		t.Fatal(err)
	}

	// The session is closed when the test ends
	t.Cleanup(func() { s.Close() })
	return s
}

// testPhoto is a small valid print file
var testPhoto = func() *photo.Photo {
	p, err := photo.New(append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte("photo"), 10000)...))
	if err != nil {
		panic(err)
	}
	return p
}()

// TestPrint checks every way a print job can end
func TestPrint(t *testing.T) {
	// The stages of a normal dye sublimation print
	printing := []printer.Stage{printer.StageReceiving, printer.StageYellow, printer.StageMagenta, printer.StageCyan, printer.StageLaminating, printer.StageFinishing}

	testCases := []struct {
		name            string
		setup           func(f *fakePrinter)
		cancelAfter     time.Duration
		wantErr         error
		wantIs          func(error) bool
		wantUnconfirmed bool
		wantImage       bool
	}{
		{name: "completes with stage 7", setup: func(f *fakePrinter) { f.stages = append(append([]printer.Stage{}, printing...), printer.StageComplete) }, wantImage: true},
		{name: "completes back to idle", setup: func(f *fakePrinter) { f.stages = append(append([]printer.Stage{}, printing...), printer.StageIdle) }, wantImage: true},
		{name: "printer reports nothing", setup: func(f *fakePrinter) {}, wantUnconfirmed: true, wantImage: true},
		{name: "busy with another device", setup: func(f *fakePrinter) { f.foreign = [2]byte{9, 9} }, wantErr: printer.ErrBusy},
		{name: "refused when out of paper", setup: func(f *fakePrinter) { f.errorFlags = [3]byte{0x02} }, wantIs: printer.IsJobFailed},
		{name: "paper jam while printing", setup: func(f *fakePrinter) { f.stages = []printer.Stage{printer.StageYellow, printer.StagePaperJam} }, wantIs: printer.IsJobFailed, wantImage: true},
		{name: "cancelled on the printer", setup: func(f *fakePrinter) { f.stages = []printer.Stage{printer.StageYellow, printer.StageCancelled} }, wantErr: printer.ErrCancelled, wantImage: true},
		{name: "cancelled by the client while sending", setup: func(f *fakePrinter) { f.slowData = true }, cancelAfter: 300 * time.Millisecond, wantErr: printer.ErrCancelled},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Printer with the behaviour of the case
			f := newFakePrinter(t)
			tc.setup(f)
			s := connect(t, f, testPhoto.Size())

			// A client cancel arrives as context.Canceled, AfterFunc fires it from its own goroutine
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelAfter > 0 {
				time.AfterFunc(tc.cancelAfter, cancel)
			}

			// A printer that never reads needs an image larger than the socket buffers to block
			p := testPhoto
			if f.slowData {
				big, err := photo.New(append([]byte{0xFF, 0xD8}, make([]byte, 20<<20)...))
				if err != nil {
					t.Fatal(err)
				}
				p = big
			}

			// Print and check the outcome
			result, err := s.Print(ctx, p)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantIs != nil:
				if !tc.wantIs(err) {
					t.Fatalf("err = %v is not the expected kind", err)
				}
			default:
				if err != nil {
					t.Fatalf("Print: %v", err)
				}
			}
			if result.Unconfirmed != tc.wantUnconfirmed {
				t.Errorf("Unconfirmed = %v, want %v", result.Unconfirmed, tc.wantUnconfirmed)
			}

			// The image reached the printer only when the job got that far
			f.mu.Lock()
			gotImage := bytes.Equal(f.received.Bytes(), testPhoto.Bytes())
			f.mu.Unlock()
			if gotImage != tc.wantImage {
				t.Errorf("image received = %v, want %v", gotImage, tc.wantImage)
			}
		})
	}
}

// TestSession checks the queries, the second job and the end of the session
func TestSession(t *testing.T) {
	testCases := []struct {
		name  string
		setup func(f *fakePrinter)
		check func(t *testing.T, s ports.PrinterSession)
	}{
		{
			name:  "serial number and firmware",
			setup: func(f *fakePrinter) {},
			check: func(t *testing.T, s ports.PrinterSession) {
				// Both queries answer within a second
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				if sn, err := s.SerialNumber(ctx); err != nil || sn != "FAKE0001" {
					t.Errorf("SerialNumber = %q, %v", sn, err)
				}
				if fw, err := s.FirmwareVersion(ctx); err != nil || fw != "1.2.3.4" {
					t.Errorf("FirmwareVersion = %q, %v", fw, err)
				}
			},
		},
		{
			name:  "status and link after the first heartbeats",
			setup: func(f *fakePrinter) {},
			check: func(t *testing.T, s ports.PrinterSession) {
				// Let a few heartbeats go by
				time.Sleep(200 * time.Millisecond)

				st, ok := s.Status()
				if !ok || st.Battery != 87 {
					t.Errorf("Status = %+v, %v", st, ok)
				}
				if l := s.Link(); l == nil || l.Samples == 0 || l.LossPct != 0 {
					t.Errorf("Link = %+v", l)
				}
			},
		},
		{
			name:  "only one job at a time",
			setup: func(f *fakePrinter) { f.stages = []printer.Stage{printer.StageYellow, printer.StageComplete} },
			check: func(t *testing.T, s ports.PrinterSession) {
				// The first job runs in a goroutine and hands its result over a buffered channel
				first := make(chan error, 1)
				go func() { _, err := s.Print(context.Background(), testPhoto); first <- err }()
				time.Sleep(20 * time.Millisecond)

				// A second job while the first one runs is refused at once
				if _, err := s.Print(context.Background(), testPhoto); !errors.Is(err, printer.ErrJobRunning) {
					t.Errorf("second Print err = %v, want ErrJobRunning", err)
				}

				// The first job still completes
				if err := <-first; err != nil {
					t.Errorf("first Print: %v", err)
				}
			},
		},
		{
			name:  "silent printer ends the session",
			setup: func(f *fakePrinter) { f.silent = true },
			check: func(t *testing.T, s ports.PrinterSession) {
				// Done closes after HeartbeatTimeout
				select {
				case <-s.Done():
				case <-time.After(2 * time.Second):
					t.Fatal("the session did not end")
				}

				// The cause was reported before Done closed
				lost := false
				for len(s.Events()) > 0 {
					if e := <-s.Events(); e.Kind == printer.SessionLost && errors.Is(e.Err, printer.ErrConnectionLost) {
						lost = true
					}
				}
				if !lost {
					t.Error("no SessionLost event")
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Printer with the behaviour of the case, then the checks of the case
			f := newFakePrinter(t)
			tc.setup(f)
			tc.check(t, connect(t, f, testPhoto.Size()))
		})
	}
}

// TestConnectErrors checks the messages of the connections that fail
func TestConnectErrors(t *testing.T) {
	// A port that was free a moment ago refuses connections
	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	testCases := []struct {
		name    string
		ip      netip.Addr
		port    int
		wantErr error
		message string
	}{
		{name: "not IPv4", ip: netip.MustParseAddr("::1"), port: refusedPort, wantErr: printer.ErrInvalidIP, message: "Invalid IP address"},
		{name: "nothing listening", ip: netip.MustParseAddr("127.0.0.1"), port: refusedPort, message: "Could not connect to 127.0.0.1: 127.0.0.1 refused the connection. Is this a CP4x printer?"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Connect to the refusing port
			cfg := DefaultConfig()
			cfg.DataPort, cfg.CommandPort, cfg.DiscoveryPort = tc.port, tc.port, 1
			_, err := NewDriver(cfg, nil).Connect(context.Background(), tc.ip, time.Second)

			// The kind of error and the exact text the web app translates
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil || err.Error() != tc.message {
				t.Errorf("message = %v, want %q", err, tc.message)
			}
		})
	}
}
