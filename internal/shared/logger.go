package shared

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// level is a custom type to define the log level
type level int8

const (
	DEBUG level = iota
	INFO
	WARN
	ERROR
	FATAL
	PANIC
)

// section_prefix defines the default prefix referring to the section
const section_prefix = "section"

// Logger wraps the zero logger as a custom logger
type Logger struct {
	section string
	logger  zerolog.Logger
}

// NewLogger creates a new instance of zerlog logger and wraps it to the custom
// logger
func NewLogger(section string, lvl level, jsonFormat, noColor bool) *Logger {
	var writer io.Writer

	// Define writer (json or plain text)
	if jsonFormat {
		writer = os.Stdout
	} else {
		writer = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
			NoColor:    noColor,
		}
	}

	// Create logger
	logger := zerolog.New(writer).
		With().
		Timestamp().
		Str(section_prefix, section).
		Logger()

	// Define logger level
	logger = logger.Level(toZerologLevel(lvl))

	return &Logger{
		section: section,
		logger:  logger,
	}
}

// With attaches the provided fields to the logger message
func (l Logger) With(fields ...any) *Logger {
	newLog := l.logger
	newLog = newLog.With().Fields(fields).Logger()

	return &Logger{logger: newLog}
}

// WithSection generates a new logger with the provided section attaches on the message
func (l Logger) WithSection(section string, fields ...any) *Logger {
	newLog := l.With(section_prefix, section).logger

	return &Logger{logger: newLog}
}

// Debug wrapper of zerolog Debug method with additional structured fields
func (l Logger) Debug(msg string, fields ...any) { l.write(l.logger.Debug(), msg, fields...) }

// Info wrapper of zerolog Info method with additional structured fields
func (l Logger) Info(msg string, fields ...any) { l.write(l.logger.Info(), msg, fields...) }

// Warn wrapper of zerolog Warn method with additional structured fields
func (l Logger) Warn(msg string, fields ...any) { l.write(l.logger.Warn(), msg, fields...) }

// Error wrapper of zerolog Error method with additional structured fields
func (l Logger) Error(msg string, fields ...any) { l.write(l.logger.Error(), msg, fields...) }

// Fatal wrapper of zerolog Fatal method with additional structured fields
func (l Logger) Fatal(msg string, fields ...any) { l.write(l.logger.Fatal(), msg, fields...) }

// Panic wrapper of zerolog Panic method with additional structured fields
func (l Logger) Panic(msg string, fields ...any) { l.write(l.logger.Panic(), msg, fields...) }

// write adds extra fields and send them to the log
func (l Logger) write(event *zerolog.Event, msg string, fields ...any) {
	event.Fields(fields).Msg(msg)
}

// ParseLevel converts a level name like debug or info to a level, unknown names give INFO
func ParseLevel(name string) level {
	// Case and spaces do not matter
	switch strings.ToLower(strings.TrimSpace(name)) {
	// Levels below the default
	case "debug":
		return DEBUG

	// Levels above the default
	case "warn", "warning":
		return WARN
	case "error":
		return ERROR
	case "fatal":
		return FATAL
	case "panic":
		return PANIC

	// info and any unknown name
	default:
		return INFO
	}
}

// toZerologLevel converts from custom level to zerolog level
func toZerologLevel(l level) zerolog.Level {
	// One to one mapping, unknown values fall back to info
	switch l {
	case DEBUG:
		return zerolog.DebugLevel
	case INFO:
		return zerolog.InfoLevel

	// Levels above info
	case WARN:
		return zerolog.WarnLevel
	case ERROR:
		return zerolog.ErrorLevel
	case FATAL:
		return zerolog.FatalLevel
	case PANIC:
		return zerolog.PanicLevel

	// Anything else
	default:
		return zerolog.InfoLevel
	}
}
