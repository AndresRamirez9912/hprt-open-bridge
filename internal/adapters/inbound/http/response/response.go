// Package response writes the JSON answers and errors shared by the handlers and the middlewares
package response

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// module is the name reported in the errors raised by the HTTP adapter itself
const module = "http"

// Errors raised by the HTTP adapter before reaching a use case
var (
	ErrNotFound    = shared.NewError(module, 4001, "Not found", http.StatusNotFound)
	ErrInvalidBody = shared.NewError(module, 4002, "Invalid JSON body", http.StatusBadRequest)
	ErrInternal    = shared.NewError(module, 4003, "Internal Server Error", http.StatusInternalServerError)
)

// ErrorBody is the body of every error, Error keeps the Node contract and the rest is shared.Error
type ErrorBody struct {
	Error string `json:"error"`
	*ErrorFields
}

// ErrorFields are the JSON fields of shared.Error, flattened next to the error field
type ErrorFields struct {
	Module       string `json:"module"`
	InternalCode int    `json:"internal_code"`
	Message      string `json:"message"`
	HttpCode     int    `json:"http_code"`
}

// NewErrorFields copies the fields of a shared.Error
func NewErrorFields(e *shared.Error) *ErrorFields {
	return &ErrorFields{Module: e.Module, InternalCode: e.InternalCode, Message: e.Message, HttpCode: e.HttpCode}
}

// JSON sends body as JSON with the given status code
func JSON(w http.ResponseWriter, status int, body any) {
	// The headers must be set before the status line is written
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// A failed write means the client is gone, there is nobody left to tell
	_ = json.NewEncoder(w).Encode(body)
}

// Error sends err with its own status code, unknown errors become a 500 with their message
func Error(w http.ResponseWriter, err error) {
	appErr := AsAppError(err)
	JSON(w, appErr.HttpCode, ErrorBody{Error: appErr.Message, ErrorFields: NewErrorFields(appErr)})
}

// AsAppError returns err as a shared.Error, wrapping unknown errors as internal errors
func AsAppError(err error) *shared.Error {
	// Errors of the domain already carry their module, codes and HTTP status
	var appErr *shared.Error
	if errors.As(err, &appErr) {
		return appErr
	}

	// Anything else is unexpected: keep its message so the client sees the cause
	return ErrInternal.Wrap(err.Error())
}
