package printer

// SessionEventKind identifies what a SessionEvent reports
type SessionEventKind int

// Kinds of notifications a printer session delivers
const (
	SessionStatus SessionEventKind = iota + 1
	SessionStage
	SessionErrors
	SessionSending
	SessionSent
	SessionDone
	SessionLost
)

// SessionEvent is one notification of an open printer session, only the fields of its kind are set
type SessionEvent struct {
	Kind     SessionEventKind
	Status   Status
	Stage    Stage
	Errors   []PrinterError
	Progress int
	Err      error
}
