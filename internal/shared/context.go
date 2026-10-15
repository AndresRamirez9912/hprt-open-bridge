package shared

import (
	"context"
)

// contextKey defines a custom type used to define
// the type for keys on context
type contextKey string

// Supported context keys
const (
	loggerKey    contextKey = "logger"
	requestIDKey contextKey = "request_id"
)

// WithLogger stores on the provided context the logger handler
func WithLogger(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// GetLogger returns the logger stored on the context
func GetLogger(ctx context.Context) (Logger, bool) {
	logger, ok := ctx.Value(loggerKey).(Logger)
	return logger, ok
}

// MustLogger returns the logger stored on the context
func MustLogger(ctx context.Context) Logger {
	logger, ok := ctx.Value(loggerKey).(Logger)
	if !ok {
		panic("logger not set")
	}

	return logger
}

// WithRequestID stores on the provided context the id that traces one request
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// GetRequestID returns the request id stored on the context, empty when there is none
func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
