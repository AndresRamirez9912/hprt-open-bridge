package printer

import "fmt"

// Stage is the progress of a print job as reported by the printer
type Stage int32

// Print stages reported by the printer in the status block
const (
	StageIdle       Stage = 0
	StageReceiving  Stage = 1
	StageYellow     Stage = 2
	StageMagenta    Stage = 3
	StageCyan       Stage = 4
	StageLaminating Stage = 5
	StageFinishing  Stage = 6
	StageComplete   Stage = 7
	StageFailed     Stage = 8
	StageRibbonErr  Stage = 9
	StagePaperErr   Stage = 10
	StagePaperJam   Stage = 11
	StageCancelled  Stage = 15
)

// stageText holds the key and the English label of each known stage
var stageText = map[Stage][2]string{
	StageIdle:       {"idle", "Idle"},
	StageReceiving:  {"receiving", "Receiving data"},
	StageYellow:     {"printing", "Printing yellow"},
	StageMagenta:    {"printing", "Printing magenta"},
	StageCyan:       {"printing", "Printing cyan"},
	StageLaminating: {"printing", "Laminating"},
	StageFinishing:  {"printing", "Finishing"},
	StageComplete:   {"complete", "Print complete"},
	StageFailed:     {"failed", "Print failed"},
	StageRibbonErr:  {"error", "Ribbon cartridge error"},
	StagePaperErr:   {"error", "Paper error"},
	StagePaperJam:   {"error", "Paper jam"},
	StageCancelled:  {"cancelled", "Print cancelled"},
}

// Key returns the short machine name of the stage, unknown when the code is not documented
func (s Stage) Key() string {
	// Documented stages have a key, any other code is unknown
	if t, ok := stageText[s]; ok {
		return t[0]
	}
	return "unknown"
}

// Label returns the English description of the stage
func (s Stage) Label() string {
	// Documented stages have a label, any other code shows its number
	if t, ok := stageText[s]; ok {
		return t[1]
	}
	return fmt.Sprintf("Unknown status %d", int32(s))
}

// IsPrinting reports the stages 2 to 6, the ones that prove a job is really printing
func (s Stage) IsPrinting() bool { return s >= StageYellow && s <= StageFinishing }

// IsFailure reports the stages that end a job with an error
func (s Stage) IsFailure() bool { return s >= StageFailed && s <= StagePaperJam }

// ErrorCode identifies one error flag of the printer and is stable for translations
type ErrorCode int

// Error flags of the printer, with the numbers used by the vendor app
const (
	CodeNoPaperTray       ErrorCode = 0
	CodeOutOfPaper        ErrorCode = 1
	CodeRibbonMissing     ErrorCode = 2
	CodeDataError         ErrorCode = 3
	CodePaperJammed       ErrorCode = 4
	CodePrintFailed       ErrorCode = 5
	CodeRibbonError       ErrorCode = 6
	CodeLowPower          ErrorCode = 7
	CodeMemoryError       ErrorCode = 8
	CodeHeadOverheating   ErrorCode = 9
	CodeBatteryOverheat   ErrorCode = 10
	CodeRibbonMismatch    ErrorCode = 11
	CodeRibbonAuthUsedUp  ErrorCode = 12
	CodeRFIDError         ErrorCode = 13
	CodeRFIDMileageZero   ErrorCode = 14
	CodePaperError        ErrorCode = 15
	CodePaperCoversSensor ErrorCode = 16
	CodeAbnormalPaperJam  ErrorCode = 17
)

// PrinterError is one error flag reported by the printer
type PrinterError struct {
	Code  ErrorCode
	Label string
}

// Blocking reports whether the error prevents printing, low power is only a warning
func (e PrinterError) Blocking() bool { return e.Code != CodeLowPower }

// Status is the decoded status block the printer sends after every status query
type Status struct {
	Battery      int
	Firmware     string
	Errors       []PrinterError
	Notices      []string
	Stage        Stage
	PrintCounter uint16
	Token        [2]byte
	Raw          []byte
}

// Reserved reports whether some device currently holds the print reservation
func (s Status) Reserved() bool { return s.Token != [2]byte{} }

// BlockingErrors returns the errors that prevent printing
func (s Status) BlockingErrors() []PrinterError {
	// Keep only the errors that stop a job, warnings like low power are skipped
	var out []PrinterError
	for _, e := range s.Errors {
		if e.Blocking() {
			out = append(out, e)
		}
	}
	return out
}

// ErrorLabels returns the labels of the given errors in the same order
func ErrorLabels(errs []PrinterError) []string {
	// One label per error, in the order the printer reported them
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Label)
	}
	return out
}
