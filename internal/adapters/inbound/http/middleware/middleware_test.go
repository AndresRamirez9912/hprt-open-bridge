package middleware

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// testLogger discards everything below errors
var testLogger = shared.NewLogger("test", shared.ERROR, true, true)

// orderMiddleware records when it runs and can stop the chain
type orderMiddleware struct {
	name string
	log  *[]string
	stop bool
}

// Name identifies the test link
func (m orderMiddleware) Name() string { return m.name }

// Handle records the visit and passes the request on unless the link stops the chain
func (m orderMiddleware) Handle(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// Remember that this link ran
	*m.log = append(*m.log, m.name)

	// A stopping link answers itself and never calls next
	if m.stop {
		w.WriteHeader(http.StatusTeapot)
		return
	}

	next.ServeHTTP(w, r)
}

// TestChain checks that each link passes the request to the next one in order
func TestChain(t *testing.T) {
	// A full pass, and a link in the middle that stops the chain
	testCases := []struct {
		name      string
		stopAt    string
		wantOrder []string
		wantCode  int
	}{
		{name: "every link runs in order", wantOrder: []string{"first", "second", "third", "final"}, wantCode: http.StatusOK},
		{name: "a link stops the chain", stopAt: "second", wantOrder: []string{"first", "second"}, wantCode: http.StatusTeapot},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Three links, the first given to NewChain and the others added with Append
			var order []string
			links := []Middleware{}
			for _, n := range []string{"first", "second", "third"} {
				links = append(links, orderMiddleware{name: n, log: &order, stop: n == tc.stopAt})
			}
			chain := NewChain(links[:1]...).Append(links[1:]...)

			// The final handler records that the request reached the end of the chain
			final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "final") })

			// Send one request through the chain
			rec := httptest.NewRecorder()
			chain.Then(final).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

			// Order of the visits, answer and names of the links
			if !slices.Equal(order, tc.wantOrder) {
				t.Errorf("order = %v, want %v", order, tc.wantOrder)
			}
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if !slices.Equal(chain.Names(), []string{"first", "second", "third"}) {
				t.Errorf("Names() = %v", chain.Names())
			}
		})
	}
}

// TestMiddlewares checks the behaviour of each link of the bridge chain
func TestMiddlewares(t *testing.T) {
	// Final handlers: one that answers 200 and one that panics
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	panics := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })

	// A wanted header of * means any non empty value, an empty one means the header is absent
	testCases := []struct {
		name       string
		middleware Middleware
		final      http.Handler
		method     string
		header     map[string]string
		wantCode   int
		wantHeader map[string]string
	}{
		{name: "recovery turns a panic into a 500", middleware: NewRecovery(testLogger), final: panics, method: "GET", wantCode: 500},
		{name: "request id is created", middleware: NewRequestID(), final: ok, method: "GET", wantCode: 200, wantHeader: map[string]string{RequestIDHeader: "*"}},
		{name: "request id is kept", middleware: NewRequestID(), final: ok, method: "GET", header: map[string]string{RequestIDHeader: "abc"}, wantCode: 200, wantHeader: map[string]string{RequestIDHeader: "abc"}},
		{name: "cors answers the preflight", middleware: NewCORS(true, []string{"*"}, []string{"GET", "POST"}, []string{"Content-Type"}), final: panics, method: "OPTIONS", wantCode: 204, wantHeader: map[string]string{"Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "GET,POST"}},
		{name: "cors echoes an allowed origin", middleware: NewCORS(true, []string{"http://app.local"}, nil, nil), final: ok, method: "GET", header: map[string]string{"Origin": "http://app.local"}, wantCode: 200, wantHeader: map[string]string{"Access-Control-Allow-Origin": "http://app.local"}},
		{name: "cors ignores other origins", middleware: NewCORS(true, []string{"http://app.local"}, nil, nil), final: ok, method: "GET", header: map[string]string{"Origin": "http://evil.local"}, wantCode: 200, wantHeader: map[string]string{"Access-Control-Allow-Origin": ""}},
		{name: "disabled cors passes through", middleware: NewCORS(false, []string{"*"}, nil, nil), final: ok, method: "OPTIONS", wantCode: 200, wantHeader: map[string]string{"Access-Control-Allow-Origin": ""}},
		{name: "logging keeps the status", middleware: NewLogging(testLogger), final: http.NotFoundHandler(), method: "GET", wantCode: 404},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Request with the headers of the case
			req := httptest.NewRequest(tc.method, "/api/state", nil)
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}

			// A chain with only the link under test
			rec := httptest.NewRecorder()
			NewChain(tc.middleware).Then(tc.final).ServeHTTP(rec, req)

			// Status code and headers of the answer
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			for k, want := range tc.wantHeader {
				got := rec.Header().Get(k)
				if (want == "*" && got == "") || (want != "*" && got != want) {
					t.Errorf("header %s = %q, want %q", k, got, want)
				}
			}
		})
	}
}
