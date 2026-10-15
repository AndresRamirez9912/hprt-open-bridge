package middleware

import (
	"net/http"
	"strings"
)

// Compile time check that the link implements Middleware
var _ Middleware = &CORS{}

// CORS adds the CORS headers and answers the preflight requests
type CORS struct {
	enabled bool
	origins []string
	methods string
	headers string
}

// NewCORS returns the CORS link, a disabled one passes every request on untouched
func NewCORS(enabled bool, origins, methods, headers []string) *CORS {
	return &CORS{
		enabled: enabled,
		origins: origins,
		methods: strings.Join(methods, ","),
		headers: strings.Join(headers, ","),
	}
}

// Name identifies the link
func (m *CORS) Name() string { return "cors" }

// Handle sets the CORS headers and stops the chain on a preflight request
func (m *CORS) Handle(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// A disabled link only passes the request on
	if !m.enabled {
		next.ServeHTTP(w, r)
		return
	}

	// Allow the origin of the caller when the configuration accepts it
	if origin := m.allowedOrigin(r.Header.Get("Origin")); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		if origin != "*" {
			w.Header().Add("Vary", "Origin")
		}
	}

	// A preflight request is answered here: the chain stops at this link
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", m.methods)
		w.Header().Set("Access-Control-Allow-Headers", m.headers)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Any other request continues the chain
	next.ServeHTTP(w, r)
}

// allowedOrigin returns the value of Access-Control-Allow-Origin for origin, empty when not allowed
func (m *CORS) allowedOrigin(origin string) string {
	for _, o := range m.origins {
		// A wildcard accepts every origin
		if o == "*" {
			return "*"
		}

		// Otherwise the origin of the request must be listed
		if origin != "" && strings.EqualFold(o, origin) {
			return origin
		}
	}
	return ""
}
