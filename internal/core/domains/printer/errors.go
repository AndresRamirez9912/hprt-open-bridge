package printer

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// module is the name reported in every error of this domain
const module = "printer"

// Errors of the printer domain, their messages are the ones the web app translates
var (
	ErrInvalidIP      = shared.NewError(module, 1001, "Invalid IP address", http.StatusBadRequest)
	ErrInvalidTarget  = shared.NewError(module, 1002, "Invalid target", http.StatusBadRequest)
	ErrNoPrinter      = shared.NewError(module, 1003, "No printer connected", http.StatusConflict)
	ErrNoPrinterIP    = shared.NewError(module, 1004, "No printer IP", http.StatusBadRequest)
	ErrBusy           = shared.NewError(module, 1005, "Printer is busy with another device", http.StatusInternalServerError)
	ErrJobRunning     = shared.NewError(module, 1006, "A print job is already running", http.StatusConflict)
	ErrNoClaimAnswer  = shared.NewError(module, 1007, "Printer did not answer the print request", http.StatusInternalServerError)
	ErrConnectionLost = shared.NewError(module, 1008, "Connection to the printer was lost", http.StatusInternalServerError)
	ErrPrintTimeout   = shared.NewError(module, 1009, "Timed out waiting for the printer", http.StatusInternalServerError)
	ErrCancelled      = shared.NewError(module, 1010, "Print cancelled", http.StatusInternalServerError)
	ErrShortReply     = shared.NewError(module, 1011, "Reply too short", http.StatusBadGateway)
	ErrDiscovery      = shared.NewError(module, 1012, "Discovery failed", http.StatusInternalServerError)

	// Codes of the errors built by the functions below, so errors.Is works on them
	errConnectFailed = shared.NewError(module, 1100, "Could not connect", http.StatusBadGateway)
	errSendStalled   = shared.NewError(module, 1101, "Sending stalled", http.StatusInternalServerError)
	errJobFailed     = shared.NewError(module, 1102, "Print failed", http.StatusInternalServerError)
)

// ConnectFailed builds the error of a connection that could not be opened to ip
func ConnectFailed(ip, reason string) *shared.Error {
	return withMessage(errConnectFailed, fmt.Sprintf("Could not connect to %s: %s", ip, reason))
}

// IsConnectFailed reports whether err was built by ConnectFailed
func IsConnectFailed(err error) bool { return errConnectFailed.Is(err) }

// SendStalled builds the error of an image that could not be sent for the given seconds
func SendStalled(seconds int) *shared.Error {
	return withMessage(errSendStalled, fmt.Sprintf("Sending stalled for %d s: the Wi-Fi link to the printer is losing too many packets", seconds))
}

// IsSendStalled reports whether err was built by SendStalled
func IsSendStalled(err error) bool { return errSendStalled.Is(err) }

// JobFailed builds the error of a job the printer refused or failed, stage idle means refused before sending
func JobFailed(stage Stage, errs []PrinterError) *shared.Error {
	// The message starts with the failed stage, when there is one, followed by the error labels
	var parts []string
	if stage != StageIdle {
		parts = append(parts, stage.Label())
	}
	parts = append(parts, ErrorLabels(errs)...)

	// Nothing more specific is known
	if len(parts) == 0 {
		return errJobFailed
	}

	// A refusal lists the errors with commas, a failed stage joins them with a dash as the web app expects
	sep := ", "
	if stage != StageIdle {
		sep = " — "
	}
	return withMessage(errJobFailed, strings.Join(parts, sep))
}

// IsJobFailed reports whether err was built by JobFailed
func IsJobFailed(err error) bool { return errJobFailed.Is(err) }

// withMessage copies base with a different message and keeps its codes
func withMessage(base *shared.Error, message string) *shared.Error {
	return shared.NewError(base.Module, base.InternalCode, message, base.HttpCode)
}
