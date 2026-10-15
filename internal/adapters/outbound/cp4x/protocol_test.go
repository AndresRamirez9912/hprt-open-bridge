package cp4x

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// statusBlock builds a 24 byte status block for the tests
func statusBlock(battery byte, stage printer.Stage, errFlags [3]byte, token [2]byte) []byte {
	// Battery and the short firmware 1.2.3 stored in reverse order
	b := make([]byte, statusBlockSize)
	b[0] = battery
	b[1], b[2], b[3] = 3, 2, 1

	// Error flags, stage, print counter and reservation token
	copy(b[4:7], errFlags[:])
	binary.LittleEndian.PutUint32(b[8:12], uint32(stage))
	binary.LittleEndian.PutUint16(b[13:15], 30)
	b[18], b[19] = token[0], token[1]
	return b
}

// TestEncodeCommand checks the frame layout of the command port
func TestEncodeCommand(t *testing.T) {
	// Frames captured from the vendor app
	testCases := []struct {
		name    string
		cmd     byte
		payload []byte
		want    []byte
	}{
		{name: "heartbeat", cmd: CmdStatus, payload: []byte{0, 0}, want: []byte{0x7E, 0x03, 0x00, 0x05, 0x00, 0x00}},
		{name: "no payload", cmd: CmdQuerySN, want: []byte{0x7E, 0x01, 0x00, 0x01}},
		{name: "claim token", cmd: CmdStatus, payload: []byte{2, 152}, want: []byte{0x7E, 0x03, 0x00, 0x05, 0x02, 0x98}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EncodeCommand(tc.cmd, tc.payload); !bytes.Equal(got, tc.want) {
				t.Errorf("EncodeCommand = % x, want % x", got, tc.want)
			}
		})
	}
}

// TestParseStatus checks the decoding of the status block fields
func TestParseStatus(t *testing.T) {
	// Status blocks covering each field and the error bytes
	testCases := []struct {
		name      string
		block     []byte
		wantErr   bool
		battery   int
		stage     printer.Stage
		errCodes  []printer.ErrorCode
		token     [2]byte
		firmware  string
		reserved  bool
		noticeLen int
	}{
		{name: "idle and healthy", block: statusBlock(87, printer.StageIdle, [3]byte{}, [2]byte{}), battery: 87, firmware: "1.2.3"},
		{name: "printing cyan reserved", block: statusBlock(50, printer.StageCyan, [3]byte{}, [2]byte{2, 152}), battery: 50, stage: printer.StageCyan, token: [2]byte{2, 152}, firmware: "1.2.3", reserved: true},
		{name: "out of paper and low power", block: statusBlock(9, printer.StageIdle, [3]byte{0x82}, [2]byte{}), battery: 9, errCodes: []printer.ErrorCode{printer.CodeOutOfPaper, printer.CodeLowPower}, firmware: "1.2.3"},
		{name: "error in the third byte", block: statusBlock(100, printer.StageIdle, [3]byte{0, 0, 0x04}, [2]byte{}), battery: 100, errCodes: []printer.ErrorCode{printer.CodePaperError}, firmware: "1.2.3"},
		{name: "airprint notices", block: statusBlock(100, printer.StageIdle, [3]byte{0, 0x30}, [2]byte{}), battery: 100, firmware: "1.2.3", noticeLen: 2},
		{name: "too short", block: make([]byte, 10), wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decode the block
			st, err := ParseStatus(tc.block)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			// Plain fields
			if st.Battery != tc.battery || st.Stage != tc.stage || st.Token != tc.token || st.Firmware != tc.firmware {
				t.Errorf("status = %+v", st)
			}
			if st.Reserved() != tc.reserved {
				t.Errorf("Reserved() = %v, want %v", st.Reserved(), tc.reserved)
			}

			// Error flags in the order of the table
			if len(st.Errors) != len(tc.errCodes) {
				t.Fatalf("errors = %v, want codes %v", st.Errors, tc.errCodes)
			}
			for i, code := range tc.errCodes {
				if st.Errors[i].Code != code {
					t.Errorf("error %d = %v, want %v", i, st.Errors[i].Code, code)
				}
			}

			// Display notices
			if len(st.Notices) != tc.noticeLen {
				t.Errorf("notices = %v, want %d", st.Notices, tc.noticeLen)
			}
		})
	}
}

// TestParser checks how the command port stream is cut into messages
func TestParser(t *testing.T) {
	// Frames of every kind the printer sends
	status := EncodeCommand(CmdStatus, statusBlock(87, printer.StageIdle, [3]byte{}, [2]byte{}))
	stagePush := EncodeCommand(CmdStagePush, []byte{0x03, 0, 0, 0})
	errorPush := EncodeCommand(CmdErrorPush, []byte{0x10, 0, 0})
	snReply := EncodeCommand(CmdQuerySN, []byte("SN01\x00\x00"))

	testCases := []struct {
		name   string
		chunks [][]byte
		kinds  []MessageKind
	}{
		{name: "one framed status", chunks: [][]byte{status}, kinds: []MessageKind{KindStatus}},
		{name: "frame split in two reads", chunks: [][]byte{status[:7], status[7:]}, kinds: []MessageKind{KindStatus}},
		{name: "three frames in one read", chunks: [][]byte{append(append(append([]byte{}, stagePush...), errorPush...), snReply...)}, kinds: []MessageKind{KindStage, KindErrors, KindReply}},
		{name: "bare status block", chunks: [][]byte{statusBlock(10, printer.StageYellow, [3]byte{}, [2]byte{})}, kinds: []MessageKind{KindStatus}},
		{name: "unknown command is a plain reply", chunks: [][]byte{EncodeCommand(0x42, []byte{1})}, kinds: []MessageKind{KindReply}},
		{name: "short stage push is a plain reply", chunks: [][]byte{EncodeCommand(CmdStagePush, []byte{1})}, kinds: []MessageKind{KindReply}},
		{name: "incomplete frame waits", chunks: [][]byte{status[:10]}, kinds: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Feed the chunks as separate socket reads
			var p Parser
			var got []MessageKind
			for _, c := range tc.chunks {
				for _, m := range p.Push(c) {
					got = append(got, m.Kind)
				}
			}

			// Same messages in the same order
			if len(got) != len(tc.kinds) {
				t.Fatalf("kinds = %v, want %v", got, tc.kinds)
			}
			for i := range got {
				if got[i] != tc.kinds[i] {
					t.Errorf("message %d kind = %v, want %v", i, got[i], tc.kinds[i])
				}
			}
		})
	}
}

// TestHelpers checks the claim token and the cleanup of serial number replies
func TestHelpers(t *testing.T) {
	// The token of the printer at 192.168.2.152
	token := ClaimToken([4]byte{192, 168, 2, 152})

	testCases := []struct {
		name string
		got  string
		want string
	}{
		{name: "claim token is the last two octets", got: string(token[:]), want: string([]byte{2, 152})},
		{name: "serial keeps letters and digits", got: cleanASCII([]byte("CP410GL2548\x00\x01\xff")), want: "CP410GL2548"},
		{name: "serial keeps the allowed symbols", got: cleanASCII([]byte("A.B&C^D-E F")), want: "A.B&C^D-EF"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
