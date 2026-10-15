package services

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// printerIP and movedIP are the addresses used by the connection tests
var (
	printerIP = netip.MustParseAddr("192.168.2.152")
	movedIP   = netip.MustParseAddr("192.168.2.160")
)

// TestPrinterServiceConnect checks the connection workflow and the lookup by serial number
func TestPrinterServiceConnect(t *testing.T) {
	// The error the driver gives for the old address of the printer
	refused := printer.ConnectFailed(printerIP.String(), "refused")
	failAtOldAddress := func(ip netip.Addr, _ int) error {
		if ip == printerIP {
			return refused
		}
		return nil
	}

	testCases := []struct {
		name      string
		req       printer.ConnectRequest
		connect   func(ip netip.Addr, attempt int) error
		found     []printer.Discovered
		wantErr   error
		wantIP    netip.Addr
		wantLogOf string
	}{
		{name: "invalid address", req: printer.ConnectRequest{}, wantErr: printer.ErrInvalidIP},
		{name: "connects", req: printer.ConnectRequest{IP: printerIP}, wantIP: printerIP, wantLogOf: "connected to 192.168.2.152"},
		{
			name:    "fails without serial number",
			req:     printer.ConnectRequest{IP: printerIP},
			connect: func(netip.Addr, int) error { return refused },
			wantErr: refused,
		},
		{
			name:      "finds the printer at its new address by serial number",
			req:       printer.ConnectRequest{IP: printerIP, Info: printer.Info{SN: "SN1"}},
			connect:   failAtOldAddress,
			found:     []printer.Discovered{{IP: movedIP, Name: "CP4100-16DD", SN: "SN1"}},
			wantIP:    movedIP,
			wantLogOf: "searching LAN for SN SN1",
		},
		{
			name:    "no lookup when a wake window was asked",
			req:     printer.ConnectRequest{IP: printerIP, Info: printer.Info{SN: "SN1"}, WakeWindow: 8 * time.Second},
			connect: failAtOldAddress,
			found:   []printer.Discovered{{IP: movedIP, SN: "SN1"}},
			wantErr: refused,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Service over a fake driver that behaves as the case says
			rec := &recorder{}
			svc := NewPrinterService(&fakeDriver{connect: tc.connect, found: tc.found}, rec, testConfig())
			defer svc.Disconnect(context.Background())

			// Connect
			state, err := svc.Connect(context.Background(), tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}

			// Connected at the expected address, with the expected log line
			if !state.Connected || state.IP != tc.wantIP {
				t.Errorf("state = %+v, want connected to %v", state, tc.wantIP)
			}
			rec.mu.Lock()
			logs := slices.Clone(rec.logs)
			rec.mu.Unlock()
			if !slices.Contains(logs, tc.wantLogOf) {
				t.Errorf("logs = %v, want %q", logs, tc.wantLogOf)
			}
		})
	}
}

// TestPrinterServiceRunJob checks the outcome and the events of print jobs
func TestPrinterServiceRunJob(t *testing.T) {
	p, _ := photo.New(jpeg)

	// print is the behaviour of the first session, the reconnected one prints fine
	testCases := []struct {
		name       string
		noPrinter  bool
		print      func(ctx context.Context, s *fakeSession) (printer.PrintResult, error)
		cancel     bool
		wantErr    error
		wantPhases []printer.JobPhase
		wantRetry  bool
	}{
		{name: "no printer connected", noPrinter: true, wantErr: printer.ErrNoPrinter},
		{
			name:       "prints",
			print:      func(context.Context, *fakeSession) (printer.PrintResult, error) { return printer.PrintResult{}, nil },
			wantPhases: []printer.JobPhase{printer.PhaseClaim, printer.PhaseDone},
		},
		{
			name: "printer fails the job",
			print: func(context.Context, *fakeSession) (printer.PrintResult, error) {
				return printer.PrintResult{}, printer.ErrBusy
			},
			wantErr:    printer.ErrBusy,
			wantPhases: []printer.JobPhase{printer.PhaseClaim, printer.PhaseFailed},
		},
		{
			name: "cancelled by the client",
			print: func(ctx context.Context, _ *fakeSession) (printer.PrintResult, error) {
				// Block until the job context is cancelled
				<-ctx.Done()
				return printer.PrintResult{}, printer.ErrCancelled
			},
			cancel:     true,
			wantErr:    printer.ErrCancelled,
			wantPhases: []printer.JobPhase{printer.PhaseClaim, printer.PhaseFailed},
		},
		{
			name: "link drops before sending and the job is retried",
			print: func(_ context.Context, s *fakeSession) (printer.PrintResult, error) {
				s.lose()
				return printer.PrintResult{}, printer.ErrConnectionLost
			},
			wantRetry:  true,
			wantPhases: []printer.JobPhase{printer.PhaseClaim, printer.PhaseClaim, printer.PhaseClaim, printer.PhaseDone},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Only the first session runs the behaviour of the case, setup runs with the driver lock held
			rec := &recorder{}
			drv := &fakeDriver{}
			drv.setup = func(s *fakeSession) {
				if len(drv.sessions) == 0 {
					s.print = tc.print
				}
			}
			svc := NewPrinterService(drv, rec, testConfig())
			defer svc.Disconnect(context.Background())

			// Connect unless the case tests the missing printer
			if !tc.noPrinter {
				if _, err := svc.Connect(context.Background(), printer.ConnectRequest{IP: printerIP}); err != nil {
					t.Fatal(err)
				}
			}

			// A goroutine cancels the job as soon as it is running
			if tc.cancel {
				go func() {
					eventually(func() bool { return svc.State().Printing })
					svc.CancelJob()
				}()
			}

			// Run the job
			_, err := svc.RunJob(context.Background(), p)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			// The job events the clients received, and the reconnection when expected
			if got := rec.phases(); !slices.Equal(got, tc.wantPhases) {
				t.Errorf("phases = %v, want %v", got, tc.wantPhases)
			}
			if tc.wantRetry && drv.count() < 2 {
				t.Errorf("sessions = %d, want a reconnection", drv.count())
			}
		})
	}
}

// TestPrinterServiceLifecycle checks the reconnection and the disconnection
func TestPrinterServiceLifecycle(t *testing.T) {
	// act changes something, check tells when the service reached the expected state
	testCases := []struct {
		name  string
		act   func(svc *PrinterService, drv *fakeDriver)
		check func(svc *PrinterService, drv *fakeDriver) bool
	}{
		{
			name:  "reconnects after the link drops",
			act:   func(_ *PrinterService, drv *fakeDriver) { drv.last().lose() },
			check: func(svc *PrinterService, drv *fakeDriver) bool { return drv.count() == 2 && svc.State().Connected },
		},
		{
			name:  "reads serial number and firmware",
			act:   func(*PrinterService, *fakeDriver) {},
			check: func(svc *PrinterService, _ *fakeDriver) bool { return svc.State().Info.Firmware == "1.0.9.0" },
		},
		{
			name: "disconnect stops everything",
			act:  func(svc *PrinterService, _ *fakeDriver) { svc.Disconnect(context.Background()) },
			check: func(svc *PrinterService, drv *fakeDriver) bool {
				return !svc.State().IP.IsValid() && drv.count() == 1
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// A connected service
			drv := &fakeDriver{}
			svc := NewPrinterService(drv, &recorder{}, testConfig())
			defer svc.Disconnect(context.Background())
			if _, err := svc.Connect(context.Background(), printer.ConnectRequest{IP: printerIP}); err != nil {
				t.Fatal(err)
			}

			// Act and wait for the goroutines of the service to settle
			tc.act(svc, drv)
			if !eventually(func() bool { return tc.check(svc, drv) }) {
				t.Errorf("state = %+v, sessions = %d", svc.State(), drv.count())
			}
		})
	}
}

// TestPhotoService checks that only valid files reach the printer
func TestPhotoService(t *testing.T) {
	testCases := []struct {
		name        string
		data        []byte
		wantErr     error
		wantPrinted int
	}{
		{name: "valid jpeg", data: jpeg, wantPrinted: 1},
		{name: "not a jpeg", data: []byte("hello world"), wantErr: photo.ErrNotJPEG},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Service over a fake job runner
			jobs := &fakeJobs{}
			svc := NewPhotoService(jobs)

			// Only a valid file is printed
			if _, err := svc.PrintPhoto(context.Background(), tc.data); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if jobs.printed != tc.wantPrinted {
				t.Errorf("printed = %d, want %d", jobs.printed, tc.wantPrinted)
			}

			// Cancel is forwarded as it is
			if !svc.CancelPrint(context.Background()) || !jobs.cancelled {
				t.Error("CancelPrint was not forwarded")
			}
		})
	}
}

// TestDiagnosticsService checks which address the link test measures
func TestDiagnosticsService(t *testing.T) {
	testCases := []struct {
		name       string
		connected  bool
		ip         netip.Addr
		wantErr    error
		wantPinged netip.Addr
	}{
		{name: "nothing to measure", wantErr: printer.ErrNoPrinterIP},
		{name: "explicit address", ip: movedIP, wantPinged: movedIP},
		{name: "connected printer by default", connected: true, wantPinged: printerIP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// A printer service, connected when the case says so
			printers := NewPrinterService(&fakeDriver{}, &recorder{}, testConfig())
			defer printers.Disconnect(context.Background())
			if tc.connected {
				if _, err := printers.Connect(context.Background(), printer.ConnectRequest{IP: printerIP}); err != nil {
					t.Fatal(err)
				}
			}

			// Run the link test
			inspector := &fakeInspector{}
			report, err := NewDiagnosticsService(inspector, printers).LinkTest(context.Background(), tc.ip)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			// The right address was pinged and the Wi-Fi of the machine is in the report
			if err == nil && (inspector.pinged != tc.wantPinged || report.HostWifi == nil) {
				t.Errorf("pinged %v, report %+v", inspector.pinged, report)
			}
		})
	}
}

// TestActivityService checks the log buffer and the delivery of live events
func TestActivityService(t *testing.T) {
	logger := shared.NewLogger("test", shared.ERROR, true, true)

	testCases := []struct {
		name     string
		maxLogs  int
		lines    []string
		wantLogs []string
	}{
		{name: "keeps every line under the limit", maxLogs: 5, lines: []string{"a", "b"}, wantLogs: []string{"a", "b"}},
		{name: "drops the oldest lines", maxLogs: 2, lines: []string{"a", "b", "c"}, wantLogs: []string{"b", "c"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// A subscriber listening before anything is logged
			svc := NewActivityService(logger, tc.maxLogs)
			ctx, cancel := context.WithCancel(context.Background())
			events := svc.Subscribe(ctx)

			// Log the lines of the case
			for _, l := range tc.lines {
				svc.Log(l)
			}

			// The buffer keeps the newest lines only
			got := []string{}
			for _, e := range svc.Logs() {
				got = append(got, e.Message)
			}
			if !slices.Equal(got, tc.wantLogs) {
				t.Errorf("logs = %v, want %v", got, tc.wantLogs)
			}

			// The subscriber received every line, waiting in its buffered channel
			if len(events) != len(tc.lines) {
				t.Errorf("events = %d, want %d", len(events), len(tc.lines))
			}

			// Ending ctx closes the channel, a closed channel answers ok false once it is drained
			cancel()
			closed := eventually(func() bool {
				select {
				case _, ok := <-events:
					return !ok
				default:
					return false
				}
			})
			if !closed {
				t.Error("the subscription was not closed")
			}
		})
	}
}
