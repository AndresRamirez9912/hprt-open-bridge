package services

import (
	"context"
	"sync"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Compile time check that the service implements its inbound port
var _ ports.ActivityUseCase = &ActivityService{}

// subscriberBuffer is how many events a slow client may fall behind before events are dropped
const subscriberBuffer = 256

// publisher is what the other services use to reach the live clients
type publisher interface {
	// PublishState sends the current connection snapshot to every client
	PublishState(state printer.State)
	// PublishJob sends a print job event to every client
	PublishJob(event printer.JobEvent)
	// Log adds a line to the protocol log and sends it to every client
	Log(message string)
}

// ActivityService keeps the protocol log and fans the live events out to the subscribers
//
// Concurrency: many goroutines publish and many clients subscribe at the same time, so the
// log and the set of subscribers are guarded by mu and every subscriber has a buffered channel
type ActivityService struct {
	logger  *shared.Logger
	maxLogs int

	// mu guards logs and subscribers
	mu   sync.Mutex
	logs []printer.LogEntry
	// subscribers holds one buffered channel per connected client
	subscribers map[chan printer.Event]struct{}
}

// NewActivityService returns a service that keeps the last maxLogs log lines
func NewActivityService(logger *shared.Logger, maxLogs int) *ActivityService {
	return &ActivityService{logger: logger, maxLogs: max(1, maxLogs), subscribers: map[chan printer.Event]struct{}{}}
}

// Subscribe delivers live events until ctx ends, then the channel is closed
func (a *ActivityService) Subscribe(ctx context.Context) <-chan printer.Event {
	// The buffer lets a client fall behind a little without slowing the publishers
	ch := make(chan printer.Event, subscriberBuffer)
	a.mu.Lock()
	a.subscribers[ch] = struct{}{}
	a.mu.Unlock()

	// A goroutine waits for the end of ctx to unsubscribe and close the channel
	// The channel is removed under the lock first, so broadcast never sends on a closed channel
	go func() {
		<-ctx.Done()
		a.mu.Lock()
		delete(a.subscribers, ch)
		a.mu.Unlock()
		close(ch)
	}()

	return ch
}

// Logs returns a copy of the recent protocol log, oldest first
func (a *ActivityService) Logs() []printer.LogEntry {
	// Copy under the lock so the caller never sees the slice change
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]printer.LogEntry(nil), a.logs...)
}

// PublishState sends the current connection snapshot to every client
func (a *ActivityService) PublishState(state printer.State) {
	a.broadcast(printer.Event{Kind: printer.EventState, State: state})
}

// PublishJob sends a print job event to every client
func (a *ActivityService) PublishJob(event printer.JobEvent) {
	a.broadcast(printer.Event{Kind: printer.EventJob, Job: event})
}

// Log adds a line to the protocol log and sends it to every client
func (a *ActivityService) Log(message string) {
	// Keep the line, dropping the oldest ones beyond maxLogs
	entry := printer.LogEntry{Time: time.Now(), Message: message}
	a.mu.Lock()
	a.logs = append(a.logs, entry)
	if len(a.logs) > a.maxLogs {
		a.logs = a.logs[len(a.logs)-a.maxLogs:]
	}
	a.mu.Unlock()

	// Debug output for the console and the live clients
	a.logger.Debug(message)
	a.broadcast(printer.Event{Kind: printer.EventLog, Log: entry})
}

// broadcast sends an event to every subscriber without waiting for slow ones
func (a *ActivityService) broadcast(e printer.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// A non blocking send: when the buffer of a client is full the event is dropped for that client
	for ch := range a.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}
