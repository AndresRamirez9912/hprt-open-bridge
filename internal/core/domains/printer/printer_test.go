package printer

import (
	"errors"
	"testing"
)

// TestStage checks the key, label and classification of every kind of stage
func TestStage(t *testing.T) {
	// One case per family of stages, plus an undocumented code
	testCases := []struct {
		name       string
		stage      Stage
		key        string
		label      string
		isPrinting bool
		isFailure  bool
	}{
		{name: "idle", stage: StageIdle, key: "idle", label: "Idle"},
		{name: "receiving", stage: StageReceiving, key: "receiving", label: "Receiving data"},
		{name: "first color", stage: StageYellow, key: "printing", label: "Printing yellow", isPrinting: true},
		{name: "last printing stage", stage: StageFinishing, key: "printing", label: "Finishing", isPrinting: true},
		{name: "complete", stage: StageComplete, key: "complete", label: "Print complete"},
		{name: "failed", stage: StageFailed, key: "failed", label: "Print failed", isFailure: true},
		{name: "paper jam", stage: StagePaperJam, key: "error", label: "Paper jam", isFailure: true},
		{name: "cancelled", stage: StageCancelled, key: "cancelled", label: "Print cancelled"},
		{name: "unknown", stage: Stage(42), key: "unknown", label: "Unknown status 42"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Names shown to the clients
			if got := tc.stage.Key(); got != tc.key {
				t.Errorf("Key() = %q, want %q", got, tc.key)
			}
			if got := tc.stage.Label(); got != tc.label {
				t.Errorf("Label() = %q, want %q", got, tc.label)
			}

			// Classification used by the completion rules
			if got := tc.stage.IsPrinting(); got != tc.isPrinting {
				t.Errorf("IsPrinting() = %v, want %v", got, tc.isPrinting)
			}
			if got := tc.stage.IsFailure(); got != tc.isFailure {
				t.Errorf("IsFailure() = %v, want %v", got, tc.isFailure)
			}
		})
	}
}

// TestStatus checks the reservation flag and the blocking errors of a status block
func TestStatus(t *testing.T) {
	// A warning and a real error of the printer
	lowPower := PrinterError{Code: CodeLowPower, Label: "Low power"}
	noPaper := PrinterError{Code: CodeOutOfPaper, Label: "Out of paper"}

	testCases := []struct {
		name     string
		status   Status
		reserved bool
		blocking int
	}{
		{name: "free and healthy", status: Status{}},
		{name: "reserved", status: Status{Token: [2]byte{2, 152}}, reserved: true},
		{name: "low power only warns", status: Status{Errors: []PrinterError{lowPower}}},
		{name: "out of paper blocks", status: Status{Errors: []PrinterError{lowPower, noPaper}}, blocking: 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.status.Reserved(); got != tc.reserved {
				t.Errorf("Reserved() = %v, want %v", got, tc.reserved)
			}
			if got := len(tc.status.BlockingErrors()); got != tc.blocking {
				t.Errorf("len(BlockingErrors()) = %d, want %d", got, tc.blocking)
			}
		})
	}
}

// TestErrorBuilders checks the messages the web app translates and that errors.Is keeps working
func TestErrorBuilders(t *testing.T) {
	// Errors reported by the printer for the job failures
	jam := PrinterError{Code: CodePaperJammed, Label: "Paper jammed"}
	noPaper := PrinterError{Code: CodeOutOfPaper, Label: "Out of paper"}

	testCases := []struct {
		name    string
		err     error
		message string
		is      func(error) bool
	}{
		{name: "connect failed", err: ConnectFailed("10.0.0.5", "Timeout connecting to 10.0.0.5:9100"), message: "Could not connect to 10.0.0.5: Timeout connecting to 10.0.0.5:9100", is: IsConnectFailed},
		{name: "send stalled", err: SendStalled(120), message: "Sending stalled for 120 s: the Wi-Fi link to the printer is losing too many packets", is: IsSendStalled},
		{name: "failed stage with errors", err: JobFailed(StagePaperErr, []PrinterError{jam}), message: "Paper error — Paper jammed", is: IsJobFailed},
		{name: "refused before sending", err: JobFailed(StageIdle, []PrinterError{noPaper, jam}), message: "Out of paper, Paper jammed", is: IsJobFailed},
		{name: "failed without details", err: JobFailed(StageIdle, nil), message: "Print failed", is: IsJobFailed},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The exact text matters: the web app translates by it
			if got := tc.err.Error(); got != tc.message {
				t.Errorf("message = %q, want %q", got, tc.message)
			}

			// The codes identify the error whatever its message
			if !tc.is(tc.err) {
				t.Errorf("the error is not recognized by its Is function")
			}
			if errors.Is(tc.err, ErrBusy) {
				t.Errorf("the error must not match an unrelated error")
			}
		})
	}
}
