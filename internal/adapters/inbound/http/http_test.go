package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/middleware"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// fakeUseCases implements every inbound port with fixed answers for the handler tests
type fakeUseCases struct {
	state     printer.State
	connectIP netip.Addr
	printErr  error
}

// Discover answers one printer
func (f *fakeUseCases) Discover(context.Context, time.Duration, netip.Addr) ([]printer.Discovered, error) {
	return []printer.Discovered{{IP: netip.MustParseAddr("192.168.2.152"), Name: "CP4100-7d87", SN: "SN1"}}, nil
}

// Connect records the address and answers a connected state
func (f *fakeUseCases) Connect(_ context.Context, req printer.ConnectRequest) (printer.State, error) {
	f.connectIP = req.IP
	return printer.State{Connected: true, IP: req.IP}, nil
}

// Disconnect answers the disconnected state
func (f *fakeUseCases) Disconnect(context.Context) printer.State { return printer.Disconnected }

// State answers the state chosen by the test
func (f *fakeUseCases) State() printer.State { return f.state }

// PrintPhoto validates like the real service and answers the error chosen by the test
func (f *fakeUseCases) PrintPhoto(_ context.Context, data []byte) (printer.PrintResult, error) {
	// Same validation as the photo service, then the outcome chosen by the test
	if _, err := photo.New(data); err != nil {
		return printer.PrintResult{}, err
	}
	return printer.PrintResult{}, f.printErr
}

// CancelPrint answers that nothing was running
func (f *fakeUseCases) CancelPrint(context.Context) bool { return false }

// LinkTest answers an empty report for the address
func (f *fakeUseCases) LinkTest(_ context.Context, ip netip.Addr) (*network.LinkReport, error) {
	// No connected printer in the fake, so an address is required
	if !ip.IsValid() {
		return nil, printer.ErrNoPrinterIP
	}
	return &network.LinkReport{IP: ip}, nil
}

// Gateway answers that there is no Wi-Fi network
func (f *fakeUseCases) Gateway(context.Context) (*network.Gateway, error) {
	return nil, network.ErrNoWifiNetwork
}

// ScanWifi answers that scanning is not possible
func (f *fakeUseCases) ScanWifi(context.Context) ([]network.WifiNetwork, error) {
	return nil, network.ErrScanUnavailable
}

// Subscribe answers a channel that closes with ctx
func (f *fakeUseCases) Subscribe(ctx context.Context) <-chan printer.Event {
	// Unbuffered and never written, a goroutine closes it when the stream ends
	ch := make(chan printer.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch
}

// Logs answers one log line
func (f *fakeUseCases) Logs() []printer.LogEntry {
	return []printer.LogEntry{{Time: time.UnixMilli(1000), Message: "cmd tx 7e0300050000"}}
}

// testLogger discards everything below errors
var testLogger = shared.NewLogger("test", shared.ERROR, true, true)

// newTestRouter builds the real router with the real chain over fake use cases
func newTestRouter(f *fakeUseCases) http.Handler {
	// The same links as the application, in the same order
	h := NewHandler(f, f, f, f)
	chain := middleware.NewChain(
		middleware.NewRecovery(testLogger),
		middleware.NewRequestID(),
		middleware.NewLogging(testLogger),
		middleware.NewCORS(true, []string{"*"}, []string{"GET", "POST", "OPTIONS"}, []string{"Content-Type"}),
	)
	return NewRouter(h, chain)
}

// TestHandlers checks the status code and the JSON of every endpoint
func TestHandlers(t *testing.T) {
	// The smallest file that starts like a JPEG
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}

	// One case per endpoint and per error the endpoint can answer
	testCases := []struct {
		name       string
		method     string
		path       string
		body       []byte
		printErr   error
		wantStatus int
		wantJSON   string
	}{
		{name: "state without printer", method: "GET", path: "/api/state", wantStatus: 200, wantJSON: `{"connected":false}`},
		{name: "logs", method: "GET", path: "/api/logs", wantStatus: 200, wantJSON: `[{"t":1000,"msg":"cmd tx 7e0300050000"}]`},
		{name: "discover", method: "GET", path: "/api/discover?timeout=100", wantStatus: 200, wantJSON: `[{"ip":"192.168.2.152","name":"CP4100-7d87","sn":"SN1"}]`},
		{name: "discover with bad target", method: "GET", path: "/api/discover?target=nope", wantStatus: 400, wantJSON: `"error":"Invalid target"`},
		{name: "connect", method: "POST", path: "/api/connect", body: []byte(`{"ip":"192.168.2.152"}`), wantStatus: 200, wantJSON: `"connected":true`},
		{name: "connect with bad ip", method: "POST", path: "/api/connect", body: []byte(`{"ip":"999.1.1.1"}`), wantStatus: 400, wantJSON: `"error":"Invalid IP address","module":"printer","internal_code":1001`},
		{name: "connect with broken json", method: "POST", path: "/api/connect", body: []byte(`{`), wantStatus: 400, wantJSON: `"error":"Invalid JSON body"`},
		{name: "disconnect", method: "POST", path: "/api/disconnect", wantStatus: 200, wantJSON: `{"connected":false}`},
		{name: "print", method: "POST", path: "/api/print", body: jpeg, wantStatus: 200, wantJSON: `{"ok":true}`},
		{name: "print not a jpeg", method: "POST", path: "/api/print", body: []byte("hello"), wantStatus: 400, wantJSON: `"ok":false,"error":"Body must be a JPEG image"`},
		{name: "print without printer", method: "POST", path: "/api/print", body: jpeg, printErr: printer.ErrNoPrinter, wantStatus: 409, wantJSON: `"error":"No printer connected"`},
		{name: "print job fails", method: "POST", path: "/api/print", body: jpeg, printErr: printer.JobFailed(printer.StagePaperJam, nil), wantStatus: 500, wantJSON: `"ok":false,"error":"Paper jam"`},
		{name: "cancel", method: "POST", path: "/api/cancel", wantStatus: 200, wantJSON: `{"cancelled":false}`},
		{name: "link without printer", method: "GET", path: "/api/link", wantStatus: 400, wantJSON: `"error":"No printer IP"`},
		{name: "link with ip", method: "GET", path: "/api/link?ip=192.168.2.152", wantStatus: 200, wantJSON: `"ip":"192.168.2.152"`},
		{name: "gateway without network", method: "GET", path: "/api/gateway", wantStatus: 404, wantJSON: `"error":"This PC has no IPv4 network on Wi-Fi yet"`},
		{name: "wifi scan unavailable", method: "GET", path: "/api/wifi/scan", wantStatus: 503, wantJSON: `"module":"network"`},
		{name: "unknown path", method: "GET", path: "/api/nope", wantStatus: 404, wantJSON: `"error":"Not found"`},
		{name: "wrong method", method: "GET", path: "/api/print", wantStatus: 404, wantJSON: `"error":"Not found"`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Send the request through the whole router, middlewares included
			router := newTestRouter(&fakeUseCases{printErr: tc.printErr})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body)))

			// Status code and content type of the answer
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}

			// The part of the JSON contract the case is about
			if !strings.Contains(rec.Body.String(), tc.wantJSON) {
				t.Errorf("body = %s, want it to contain %s", rec.Body.String(), tc.wantJSON)
			}
		})
	}
}

// TestEventsStream checks that the stream starts with the current state
func TestEventsStream(t *testing.T) {
	// The first event of the stream is always the current state
	testCases := []struct {
		name  string
		state printer.State
		want  string
	}{
		{name: "no printer", state: printer.Disconnected, want: "event: state\ndata: {\"connected\":false}\n\n"},
		{name: "connected printer", state: printer.State{Connected: true, IP: netip.MustParseAddr("10.0.0.5")}, want: `"ip":"10.0.0.5","connected":true`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The stream lasts until the timeout of ctx ends it
			router := newTestRouter(&fakeUseCases{state: tc.state})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx))

			// Server sent events with the state as the first event
			if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
				t.Errorf("Content-Type = %q", ct)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("stream = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// TestRoutes checks that the route table is complete and consistent
func TestRoutes(t *testing.T) {
	// The real route table over fake use cases
	routes := Routes(NewHandler(&fakeUseCases{}, &fakeUseCases{}, &fakeUseCases{}, &fakeUseCases{}))

	// Every route must pass every rule, the maps remember what earlier routes used
	seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
	for _, r := range routes {
		key := r.Method + " " + r.Path
		testCases := []struct {
			name string
			ok   bool
		}{
			{name: "has an operation id", ok: r.OperationID != ""},
			{name: "operation id is unique", ok: !seenIDs[r.OperationID]},
			{name: "method and path are unique", ok: !seenPaths[key]},
			{name: "has a summary and a tag", ok: r.Summary != "" && r.Tag != ""},
			{name: "has a handler", ok: r.Handler != nil},
			{name: "documents a success answer", ok: len(r.Responses) > 0 && r.Responses[0].Status == http.StatusOK},
			{name: "path is under /api", ok: strings.HasPrefix(r.Path, "/api/")},
		}

		for _, tc := range testCases {
			t.Run(key+" "+tc.name, func(t *testing.T) {
				if !tc.ok {
					t.Errorf("route %+v", r)
				}
			})
		}

		// Remember this route for the uniqueness rules of the next ones
		seenIDs[r.OperationID], seenPaths[key] = true, true
	}

	// The JSON schemas of the table must be serializable for a future OpenAPI generator
	for _, r := range routes {
		for _, resp := range r.Responses {
			if _, err := json.Marshal(resp.Body.Schema); err != nil {
				t.Errorf("%s schema: %v", r.OperationID, err)
			}
		}
	}
}

// TestDTOs checks the mapping of the job events to the Node contract
func TestDTOs(t *testing.T) {
	// One event of each phase, with the exact JSON of the Node bridge
	stage := printer.StageCyan
	testCases := []struct {
		name  string
		event printer.JobEvent
		want  string
	}{
		{name: "claim", event: printer.JobEvent{Phase: printer.PhaseClaim, Label: "Requesting printer"}, want: `{"phase":"claim","label":"Requesting printer"}`},
		{name: "sending", event: printer.JobEvent{Phase: printer.PhaseSending, Progress: 0, RateKBps: 0, Label: "Sending"}, want: `{"phase":"sending","label":"Sending","progress":0}`},
		{name: "stage", event: printer.JobEvent{Phase: printer.PhaseStage, Stage: &stage, Label: stage.Label()}, want: `{"phase":"stage","label":"Printing cyan","stageCode":4,"key":"printing"}`},
		{name: "done", event: printer.JobEvent{Phase: printer.PhaseDone, OK: true, Unconfirmed: true}, want: `{"phase":"done","ok":true,"unconfirmed":true}`},
		{name: "failed", event: printer.JobEvent{Phase: printer.PhaseFailed, Error: "Paper jam"}, want: `{"phase":"failed","ok":false,"error":"Paper jam"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fields that do not belong to the phase must be absent
			got, _ := json.Marshal(toJobDTO(tc.event))
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
