package printer

// JobPhase names one step of a print job as shown to clients
type JobPhase string

// Phases of a print job in the order they normally happen
const (
	PhaseClaim   JobPhase = "claim"
	PhaseSending JobPhase = "sending"
	PhaseSent    JobPhase = "sent"
	PhaseStage   JobPhase = "stage"
	PhaseDone    JobPhase = "done"
	PhaseFailed  JobPhase = "failed"
)

// JobEvent is one progress notification of the running print job
type JobEvent struct {
	Phase    JobPhase
	Label    string
	Progress int
	// RateKBps is the transfer speed while sending, zero when unknown
	RateKBps int
	// Stage is set on stage events only
	Stage *Stage
	// OK, Error and Unconfirmed are set on the final done or failed event
	OK          bool
	Error       string
	Unconfirmed bool
}

// PrintResult describes a print job that ended without error
type PrintResult struct {
	// Unconfirmed is true when the printer never reported progress and the job was assumed done
	Unconfirmed bool
}
