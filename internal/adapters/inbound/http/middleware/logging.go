package middleware

import (
	"net/http"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Compile time check that the link implements Middleware
var _ Middleware = &Logging{}

// Logging logs every request with its status and execution time
type Logging struct {
	logger *shared.Logger
}

// NewLogging returns the request logging link
func NewLogging(logger *shared.Logger) *Logging {
	return &Logging{logger: logger}
}

// Name identifies the link
func (m *Logging) Name() string { return "logging" }

// Handle stores a request logger on the context, passes the request on and logs the result
func (m *Logging) Handle(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// A logger with the request fields, so every line of this request can be found together
	start := time.Now()
	reqLogger := m.logger.With("request_id", shared.GetRequestID(r.Context()), "method", r.Method, "path", r.URL.Path)

	// Wrap the writer to learn the status code the rest of the chain sends
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	next.ServeHTTP(rec, r.WithContext(shared.WithLogger(r.Context(), *reqLogger)))

	// Log once the request is fully handled
	reqLogger.Info("request handled", "status", rec.status, "duration", time.Since(start).String())
}

// statusRecorder remembers the status code and keeps streaming working for server sent events
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before sending it
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap gives http.ResponseController access to the original writer to flush streams
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
