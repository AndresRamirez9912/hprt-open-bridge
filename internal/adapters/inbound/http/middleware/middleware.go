// Package middleware holds the chain of responsibility that every request goes through
package middleware

import (
	"net/http"
	"slices"
)

// Middleware is one link of the request chain of responsibility
//
// A link does its own work and then decides whether to pass the request to next
// or to answer it itself and stop the chain
type Middleware interface {
	// Name identifies the link in the logs and in the API documentation
	Name() string
	// Handle processes the request and passes it to next to continue the chain
	Handle(w http.ResponseWriter, r *http.Request, next http.Handler)
}

// Chain is an ordered list of middlewares, the first one receives the request first
type Chain struct {
	links []Middleware
}

// NewChain returns a chain with the given middlewares in order
func NewChain(links ...Middleware) Chain {
	return Chain{links: links}
}

// Append returns a new chain with more middlewares added at the end
func (c Chain) Append(more ...Middleware) Chain {
	// Clone first so the original chain is never modified through a shared array
	return Chain{links: append(slices.Clone(c.links), more...)}
}

// Names returns the names of the links in order
func (c Chain) Names() []string {
	names := make([]string, 0, len(c.links))
	for _, l := range c.links {
		names = append(names, l.Name())
	}
	return names
}

// Then links every middleware to the next one and the last one to final
func (c Chain) Then(final http.Handler) http.Handler {
	// Build from the end so each link already knows the handler that follows it
	next := final
	for i := len(c.links) - 1; i >= 0; i-- {
		next = &link{middleware: c.links[i], next: next}
	}

	// next is now the first link of the chain
	return next
}

// link connects one middleware with the handler that follows it in the chain
type link struct {
	middleware Middleware
	next       http.Handler
}

// ServeHTTP gives the request to the middleware together with the rest of the chain
func (l *link) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.middleware.Handle(w, r, l.next)
}
