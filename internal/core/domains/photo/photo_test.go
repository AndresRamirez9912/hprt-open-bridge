package photo

import (
	"errors"
	"testing"
)

// TestNew checks the validation of the print files
func TestNew(t *testing.T) {
	// The smallest file that starts like a JPEG
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}

	testCases := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{name: "valid jpeg", data: jpeg},
		{name: "empty", data: nil, wantErr: ErrNotJPEG},
		{name: "too short", data: []byte{0xFF, 0xD8}, wantErr: ErrNotJPEG},
		{name: "png signature", data: []byte{0x89, 'P', 'N', 'G', 0x0D}, wantErr: ErrNotJPEG},
		{name: "too large", data: append([]byte{0xFF, 0xD8}, make([]byte, MaxSize)...), wantErr: ErrTooLarge},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Validate the file
			p, err := New(tc.data)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			// A valid photo keeps the file untouched
			if err == nil && p.Size() != len(tc.data) {
				t.Errorf("Size() = %d, want %d", p.Size(), len(tc.data))
			}
		})
	}
}
