package shared

import (
	"fmt"
	"strings"
)

// Type assertion Error must implement error
var _ error = &Error{}

// Error is the custom error returned to the user
type Error struct {
	Module       string `json:"module"`
	InternalCode int    `json:"internal_code"`
	Message      string `json:"message"`
	HttpCode     int    `json:"http_code"`
}

// NewError returns an instance of Error with the provided data
func NewError(module string, internalCode int, message string, httpCode int) *Error {
	return &Error{
		Module:       module,
		InternalCode: internalCode,
		Message:      message,
		HttpCode:     httpCode,
	}
}

// Error returns the error message (used to Implemented error)
func (e *Error) Error() string {
	return e.Message
}

// Wrap attaches the provided description to the current description
// of the error
func (e *Error) Wrap(description string) *Error {
	// Nothing to add
	if strings.TrimSpace(description) == "" {
		return e
	}

	// A copy keeps the original error unchanged for the other callers
	return &Error{
		Module:       e.Module,
		InternalCode: e.InternalCode,
		Message:      e.Message + ": " + description,
		HttpCode:     e.HttpCode,
	}
}

// Wrapf attaches the provided description and arguments to the
// current description of the error
func (e *Error) Wrapf(format string, args ...any) *Error {
	// Nothing to add
	if strings.TrimSpace(format) == "" {
		return e
	}

	// A copy keeps the original error unchanged for the other callers
	return &Error{
		Module:       e.Module,
		InternalCode: e.InternalCode,
		Message:      e.Message + ": " + fmt.Sprintf(format, args...),
		HttpCode:     e.HttpCode,
	}
}

// Is compares the provided error with the error and returns true
// if both have the same internal code and Http code
func (e *Error) Is(other error) bool {
	if other == nil {
		return false
	}

	// Other must be also Error type
	otherError, ok := other.(*Error)
	if !ok {
		return false
	}

	// Must have the same internal code and HttpCode to be equal
	if otherError.InternalCode != e.InternalCode ||
		otherError.HttpCode != e.HttpCode {
		return false
	}

	return true
}
