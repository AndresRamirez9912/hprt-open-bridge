package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// keepAliveEvery is how often a comment line keeps idle proxies from closing the stream
const keepAliveEvery = 30 * time.Second

// Events streams the live events as server sent events, starting with the current state
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	// Headers of a server sent events stream, sent right away so the client knows it is open
	ctx := r.Context()
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Subscribe before sending the first state so no change in between is lost
	// The channel is closed by the activity service when ctx ends
	events := h.activity.Subscribe(ctx)
	if writeSSE(w, rc, string(printer.EventState), toStateDTO(h.printers.State())) != nil {
		return
	}

	// The ticker fires the keep alive comments while no event arrives
	ticker := time.NewTicker(keepAliveEvery)
	defer ticker.Stop()

	// Wait for whichever comes first: the client leaving, the keep alive tick or a new event
	for {
		select {
		case <-ctx.Done():
			// The client closed the stream or the server is shutting down
			return

		case <-ticker.C:
			// A comment line keeps the connection busy without being an event
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}

		case e, ok := <-events:
			// A closed channel means the subscription ended
			if !ok {
				return
			}
			if writeSSE(w, rc, string(e.Kind), eventPayload(e)) != nil {
				return
			}
		}
	}
}

// eventPayload maps a live event to the JSON of its kind
func eventPayload(e printer.Event) any {
	// Each kind carries its own payload, state is the default
	switch e.Kind {
	case printer.EventJob:
		return toJobDTO(e.Job)
	case printer.EventLog:
		return toLogDTO(e.Log)
	default:
		return toStateDTO(e.State)
	}
}

// writeSSE writes one event in the text/event-stream format and flushes it
func writeSSE(w http.ResponseWriter, rc *http.ResponseController, event string, data any) error {
	// Each event is one JSON line
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// Write the event and push it out now instead of waiting for the buffer to fill
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	return rc.Flush()
}
