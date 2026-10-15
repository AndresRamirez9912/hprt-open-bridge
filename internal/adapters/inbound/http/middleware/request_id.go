package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Compile time check that the link implements Middleware
var _ Middleware = &RequestID{}

// RequestIDHeader is the header that carries the request id in both directions
const RequestIDHeader = "X-Request-ID"

// RequestID gives every request an id, taken from X-Request-ID or created
type RequestID struct{}

// NewRequestID returns the request id link
func NewRequestID() *RequestID { return &RequestID{} }

// Name identifies the link
func (m *RequestID) Name() string { return "request-id" }

// Handle stores the id on the context and echoes it in the response
func (m *RequestID) Handle(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// Reuse the id of the caller so one action can be followed across services
	id := r.Header.Get(RequestIDHeader)
	if id == "" {
		id = newID()
	}

	// Echo the id to the client and continue the chain with it on the context
	w.Header().Set(RequestIDHeader, id)
	next.ServeHTTP(w, r.WithContext(shared.WithRequestID(r.Context(), id)))
}

// newID returns 16 random hex characters
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
