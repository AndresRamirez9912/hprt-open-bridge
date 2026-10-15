package printer

import "time"

// EventKind identifies what an Event carries for the live clients
type EventKind string

// Kinds of live events published by the bridge
const (
	EventState EventKind = "state"
	EventJob   EventKind = "job"
	EventLog   EventKind = "log"
)

// Event is one live notification for the clients, only the field of its kind is set
type Event struct {
	Kind  EventKind
	State State
	Job   JobEvent
	Log   LogEntry
}

// LogEntry is one line of the protocol log
type LogEntry struct {
	Time    time.Time
	Message string
}
