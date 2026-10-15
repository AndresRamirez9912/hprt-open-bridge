package photo

import (
	"net/http"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// MaxSize is the largest print file accepted, a maximum quality postcard is about 3 MB
const MaxSize = 30 * 1024 * 1024

// module is the name reported in every error of this domain
const module = "photo"

// Errors of the photo domain
var (
	ErrNotJPEG  = shared.NewError(module, 2001, "Body must be a JPEG image", http.StatusBadRequest)
	ErrTooLarge = shared.NewError(module, 2002, "Payload too large", http.StatusRequestEntityTooLarge)
)

// Photo is a print file ready for the printer, already sized and rotated by the client
type Photo struct {
	jpeg []byte
}

// New validates that data is a JPEG file of an accepted size and wraps it as a Photo
func New(data []byte) (*Photo, error) {
	// The printer buffer is limited, larger files are refused before sending anything
	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}

	// Every JPEG file starts with the SOI marker FF D8
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, ErrNotJPEG
	}

	return &Photo{jpeg: data}, nil
}

// Bytes returns the JPEG file exactly as it is sent to the printer
func (p *Photo) Bytes() []byte { return p.jpeg }

// Size returns the size of the JPEG file in bytes
func (p *Photo) Size() int { return len(p.jpeg) }
