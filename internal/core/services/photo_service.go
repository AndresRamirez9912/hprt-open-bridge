package services

import (
	"context"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Compile time check that the service implements its inbound port
var _ ports.PhotoUseCase = &PhotoService{}

// jobRunner runs print jobs on the connected printer, implemented by PrinterService
type jobRunner interface {
	// RunJob prints one photo and waits until the job ends
	RunJob(ctx context.Context, p *photo.Photo) (printer.PrintResult, error)
	// CancelJob aborts the running job and reports whether there was one
	CancelJob() bool
}

// PhotoService validates the print files and hands them to the printer
type PhotoService struct {
	jobs jobRunner
}

// NewPhotoService returns a service that prints through jobs
func NewPhotoService(jobs jobRunner) *PhotoService {
	return &PhotoService{jobs: jobs}
}

// PrintPhoto validates the file and prints it, waiting until the job ends
func (s *PhotoService) PrintPhoto(ctx context.Context, data []byte) (printer.PrintResult, error) {
	// Refuse anything that is not a valid print file before touching the printer
	p, err := photo.New(data)
	if err != nil {
		return printer.PrintResult{}, err
	}

	// Print and wait for the end of the job
	return s.jobs.RunJob(ctx, p)
}

// CancelPrint aborts the running job and reports whether there was one
func (s *PhotoService) CancelPrint(context.Context) bool {
	return s.jobs.CancelJob()
}
