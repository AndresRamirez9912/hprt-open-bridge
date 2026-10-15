package cp4x

// Wire format of the printer protocol, frame encoding and decoding only with no I/O
//
// Command port frame, both directions
//
//	0x7E | length u16 LE | cmd | payload
//	length = 1 + len(payload)
//
// The data port receives the JPEG file as it is, with no framing at all

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// DiscoveryProbe is the ASCII text sent on UDP to find printers
const DiscoveryProbe = "check_hprt_printer"

// Command ids of the command port
const (
	CmdQuerySN       byte = 0x01
	CmdStatus        byte = 0x05
	CmdErrorPush     byte = 0x13
	CmdStagePush     byte = 0x14
	CmdQueryFirmware byte = 0x1B
)

// Sizes of the fixed parts of the stream
const (
	frameStart       byte = 0x7E
	statusBlockSize       = 24
	magicHeaderSize       = 22
	magicTrailerSize      = 5
)

// magic starts the CRC framed packets used for firmware updates
var magic = [4]byte{0x12, 0x34, 0x56, 0x78}

// EncodeCommand builds one command port frame
func EncodeCommand(cmd byte, payload []byte) []byte {
	// Header: start byte, length of command plus payload, command id
	frame := make([]byte, 4+len(payload))
	frame[0] = frameStart
	binary.LittleEndian.PutUint16(frame[1:3], uint16(len(payload)+1))
	frame[3] = cmd

	// Payload right after the header
	copy(frame[4:], payload)
	return frame
}

// ClaimToken returns the reservation token, the last two octets of the printer own IPv4 address
func ClaimToken(printerIP [4]byte) [2]byte { return [2]byte{printerIP[2], printerIP[3]} }

// errorFlag maps one bit of the 3 error bytes to a printer error
type errorFlag struct {
	byteIndex int
	mask      byte
	err       printer.PrinterError
}

// errorFlags lists every known error bit with the labels the web app translates
var errorFlags = []errorFlag{
	{0, 0x01, printer.PrinterError{Code: printer.CodeNoPaperTray, Label: "No paper tray"}},
	{0, 0x02, printer.PrinterError{Code: printer.CodeOutOfPaper, Label: "Out of paper"}},
	{0, 0x04, printer.PrinterError{Code: printer.CodeRibbonMissing, Label: "Ribbon missing"}},
	{0, 0x08, printer.PrinterError{Code: printer.CodeDataError, Label: "Data transmit error"}},
	{0, 0x10, printer.PrinterError{Code: printer.CodePaperJammed, Label: "Paper jammed"}},
	{0, 0x20, printer.PrinterError{Code: printer.CodePrintFailed, Label: "Print failed"}},
	{0, 0x40, printer.PrinterError{Code: printer.CodeRibbonError, Label: "Ribbon error"}},
	{0, 0x80, printer.PrinterError{Code: printer.CodeLowPower, Label: "Low power, please charge it"}},
	{1, 0x01, printer.PrinterError{Code: printer.CodeMemoryError, Label: "Memory error"}},
	{1, 0x02, printer.PrinterError{Code: printer.CodeHeadOverheating, Label: "Print head overheating"}},
	{1, 0x04, printer.PrinterError{Code: printer.CodeBatteryOverheat, Label: "Battery overheating"}},
	{1, 0x08, printer.PrinterError{Code: printer.CodeRibbonMismatch, Label: "Ribbon not match"}},
	{2, 0x02, printer.PrinterError{Code: printer.CodeRibbonAuthUsedUp, Label: "Authorized ribbon used up"}},
	{2, 0x04, printer.PrinterError{Code: printer.CodePaperError, Label: "Paper error"}},
	{2, 0x08, printer.PrinterError{Code: printer.CodePaperCoversSensor, Label: "Paper covers the sensor"}},
	{2, 0x10, printer.PrinterError{Code: printer.CodeAbnormalPaperJam, Label: "Abnormal paper jam"}},
	{2, 0x20, printer.PrinterError{Code: printer.CodeRFIDError, Label: "RFID error"}},
	{2, 0x40, printer.PrinterError{Code: printer.CodeRFIDMileageZero, Label: "RFID mileage is 0"}},
}

// ParseErrors decodes the 3 error flag bytes of a status block or of an error push
func ParseErrors(flags []byte) []printer.PrinterError {
	// Every set bit is one error, in the order of the table
	var out []printer.PrinterError
	for _, f := range errorFlags {
		if f.byteIndex < len(flags) && flags[f.byteIndex]&f.mask != 0 {
			out = append(out, f.err)
		}
	}
	return out
}

// ParseStatus decodes a status block, the layout is documented in the bridge README
func ParseStatus(block []byte) (printer.Status, error) {
	// The fields used go up to byte 19
	if len(block) < 20 {
		return printer.Status{}, printer.ErrShortReply.Wrapf("status block of %d bytes", len(block))
	}

	// Two bits of byte 5 are display notices, not errors
	var notices []string
	if block[5]&0x10 != 0 {
		notices = append(notices, "AirPrint display")
	}
	if block[5]&0x20 != 0 {
		notices = append(notices, "AirPrint flash")
	}

	// The firmware is stored in reverse order and the counters in little endian
	return printer.Status{
		Battery:      int(block[0]),
		Firmware:     fmt.Sprintf("%d.%d.%d", block[3], block[2], block[1]),
		Errors:       ParseErrors(block[4:7]),
		Notices:      notices,
		Stage:        printer.Stage(int32(binary.LittleEndian.Uint32(block[8:12]))),
		PrintCounter: binary.LittleEndian.Uint16(block[13:15]),
		Token:        [2]byte{block[18], block[19]},
		Raw:          append([]byte(nil), block...),
	}, nil
}

// MessageKind classifies a message read from the command port
type MessageKind int

// Kinds of messages found on the command port stream
const (
	KindReply MessageKind = iota
	KindStatus
	KindStage
	KindErrors
	KindMagic
)

// Message is one decoded item of the command port stream
type Message struct {
	Kind    MessageKind
	Cmd     byte
	Payload []byte
	Status  printer.Status
	Stage   printer.Stage
	Errors  []printer.PrinterError
}

// Parser turns the command port byte stream into messages and is not safe for concurrent use
type Parser struct{ buf []byte }

// Push adds bytes read from the socket and returns every message now complete
func (p *Parser) Push(chunk []byte) []Message {
	// Keep the bytes after any incomplete message of the previous read
	p.buf = append(p.buf, chunk...)

	// Take every complete message out of the buffer
	var out []Message
	for {
		msg, ok := p.next()
		if !ok {
			return out
		}
		out = append(out, msg)
	}
}

// next extracts one message from the buffer or reports that more bytes are needed
func (p *Parser) next() (Message, bool) {
	for len(p.buf) >= 4 {
		b := p.buf
		switch {
		case b[0] == frameStart:
			// A 0x7E frame: start byte, u16 length, command and payload
			length := int(binary.LittleEndian.Uint16(b[1:3]))
			if length == 0 {
				// An impossible length: skip one byte to find the next frame
				p.buf = b[1:]
				continue
			}
			if len(b) < 3+length {
				return Message{}, false
			}
			cmd := b[3]
			payload := append([]byte(nil), b[4:3+length]...)
			p.buf = b[3+length:]
			return decodeFrame(cmd, payload), true

		case [4]byte(b[:4]) == magic:
			// A CRC framed packet: fixed header, data and trailer
			if len(b) < magicHeaderSize+magicTrailerSize {
				return Message{}, false
			}
			length := int(binary.LittleEndian.Uint32(b[14:18]))
			total := magicHeaderSize + length + magicTrailerSize
			if len(b) < total {
				return Message{}, false
			}
			msg := Message{Kind: KindMagic, Cmd: b[4], Payload: append([]byte(nil), b[magicHeaderSize:magicHeaderSize+length]...)}
			p.buf = b[total:]
			return msg, true

		default:
			// Some printers push a bare 24 byte status block with no framing
			if len(b) < statusBlockSize {
				return Message{}, false
			}
			status, _ := ParseStatus(b[:statusBlockSize])
			p.buf = b[statusBlockSize:]
			return Message{Kind: KindStatus, Cmd: CmdStatus, Status: status}, true
		}
	}
	return Message{}, false
}

// decodeFrame gives a 0x7E frame its kind according to its command id
func decodeFrame(cmd byte, payload []byte) Message {
	// By default a frame is the reply to a command
	msg := Message{Kind: KindReply, Cmd: cmd, Payload: payload}

	// Status blocks and the pushes of the printer get their own kind when they are long enough
	switch {
	case cmd == CmdStatus && len(payload) >= 20:
		msg.Kind = KindStatus
		msg.Status, _ = ParseStatus(payload)
	case cmd == CmdStagePush && len(payload) >= 4:
		msg.Kind = KindStage
		msg.Stage = printer.Stage(int32(binary.LittleEndian.Uint32(payload)))
	case cmd == CmdErrorPush && len(payload) >= 3:
		msg.Kind = KindErrors
		msg.Errors = ParseErrors(payload[:3])
	}
	return msg
}

// cleanASCII keeps letters, digits and the characters . & ^ - of a serial number reply
func cleanASCII(b []byte) string {
	// strings.Map drops every rune for which the function returns -1
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune(".&^-", r):
			return r
		}
		return -1
	}, string(b))
}
