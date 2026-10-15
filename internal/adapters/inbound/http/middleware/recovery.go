package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/response"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Compile time check that the link implements Middleware
var _ Middleware = &Recovery{}

// Recovery catches panics so one failing request never stops the bridge
type Recovery struct {
	logger *shared.Logger
}

// NewRecovery returns the panic recovery link
func NewRecovery(logger *shared.Logger) *Recovery {
	return &Recovery{logger: logger}
}

// Name identifies the link
func (m *Recovery) Name() string { return "recovery" }

// Handle passes the request on and answers with a 500 if anything after it panics
func (m *Recovery) Handle(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// The deferred function runs after the rest of the chain, also when it panics
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}

		// Log the cause with its stack trace and answer a generic error
		m.logger.Error("panic recovered", "error", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
		response.Error(w, response.ErrInternal)
	}()

	// Continue the chain
	next.ServeHTTP(w, r)
}
