package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/response"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Request limits and defaults of the API
const (
	maxConnectBody   = 4 * 1024
	defaultDiscovery = 6 * time.Second
)

// Handler holds the use cases the HTTP endpoints drive
type Handler struct {
	printers    ports.PrinterUseCase
	photos      ports.PhotoUseCase
	diagnostics ports.DiagnosticsUseCase
	activity    ports.ActivityUseCase
}

// NewHandler returns the HTTP controllers of the bridge
func NewHandler(printers ports.PrinterUseCase, photos ports.PhotoUseCase, diagnostics ports.DiagnosticsUseCase, activity ports.ActivityUseCase) *Handler {
	return &Handler{printers: printers, photos: photos, diagnostics: diagnostics, activity: activity}
}

// GetState answers the current connection snapshot
func (h *Handler) GetState(w http.ResponseWriter, _ *http.Request) {
	response.JSON(w, http.StatusOK, toStateDTO(h.printers.State()))
}

// GetLogs answers the recent protocol log
func (h *Handler) GetLogs(w http.ResponseWriter, _ *http.Request) {
	// Map every log line to its JSON shape
	entries := h.activity.Logs()
	out := make([]logDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, toLogDTO(e))
	}

	response.JSON(w, http.StatusOK, out)
}

// Discover looks for printers on the LAN for timeout milliseconds, optionally at one target
func (h *Handler) Discover(w http.ResponseWriter, r *http.Request) {
	// Search time in milliseconds, the default when missing or unreadable
	timeout := defaultDiscovery
	if v := r.URL.Query().Get("timeout"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			timeout = time.Duration(n) * time.Millisecond
		}
	}

	// Optional single address to probe instead of broadcasting
	var target netip.Addr
	if v := r.URL.Query().Get("target"); v != "" {
		addr, err := netip.ParseAddr(v)
		if err != nil || !addr.Is4() {
			response.Error(w, printer.ErrInvalidTarget)
			return
		}
		target = addr
	}

	// Run the search, it blocks for the whole timeout
	found, err := h.printers.Discover(r.Context(), timeout, target)
	if err != nil {
		response.Error(w, err)
		return
	}

	// Map the printers to their JSON shape
	out := make([]discoveredDTO, 0, len(found))
	for _, f := range found {
		out = append(out, discoveredDTO{IP: f.IP.String(), Name: f.Name, SN: f.SN})
	}
	response.JSON(w, http.StatusOK, out)
}

// Connect opens a session with the printer of the request body
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	// Read a small JSON body, an empty body is accepted and fails on the IP check
	var body connectRequestDTO
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConnectBody))
	if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &body) != nil) {
		response.Error(w, response.ErrInvalidBody)
		return
	}

	// The printers only speak IPv4
	ip, err := netip.ParseAddr(body.IP)
	if err != nil || !ip.Is4() {
		response.Error(w, printer.ErrInvalidIP)
		return
	}

	// Build the domain request, wakeMs is optional
	req := printer.ConnectRequest{IP: ip, Info: printer.Info{Name: body.Name, SN: body.SN}}
	if body.WakeMs != nil {
		req.WakeWindow = time.Duration(*body.WakeMs) * time.Millisecond
	}

	// Connect, it may take up to the wake window while the printer wakes up
	state, err := h.printers.Connect(r.Context(), req)
	if err != nil {
		response.Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, toStateDTO(state))
}

// Disconnect closes the session with the printer
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, toStateDTO(h.printers.Disconnect(r.Context())))
}

// Print sends the JPEG of the request body and answers when the printer finishes
func (h *Handler) Print(w http.ResponseWriter, r *http.Request) {
	// Read the file with one byte more than allowed to detect files that are too large
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, photo.MaxSize+1))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			err = photo.ErrTooLarge
		}
		response.Error(w, err)
		return
	}

	// Print and wait, a failed job keeps the ok flag of the Node contract next to the error fields
	result, err := h.photos.PrintPhoto(r.Context(), data)
	if err != nil {
		appErr := response.AsAppError(err)
		response.JSON(w, appErr.HttpCode, printResultDTO{OK: false, Error: appErr.Message, ErrorFields: response.NewErrorFields(appErr)})
		return
	}
	response.JSON(w, http.StatusOK, printResultDTO{OK: true, Unconfirmed: result.Unconfirmed})
}

// Cancel aborts the running print job
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, cancelDTO{Cancelled: h.photos.CancelPrint(r.Context())})
}

// LinkTest pings the printer of the ip parameter, or the connected one
func (h *Handler) LinkTest(w http.ResponseWriter, r *http.Request) {
	// An explicit address must be IPv4, without one the use case takes the connected printer
	var ip netip.Addr
	if v := r.URL.Query().Get("ip"); v != "" {
		addr, err := netip.ParseAddr(v)
		if err != nil || !addr.Is4() {
			response.Error(w, printer.ErrNoPrinterIP)
			return
		}
		ip = addr
	}

	// Run the test, the ping series takes about five seconds
	report, err := h.diagnostics.LinkTest(r.Context(), ip)
	if err != nil {
		response.Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, toLinkReportDTO(report))
}

// Gateway answers the router of the bridge machine
func (h *Handler) Gateway(w http.ResponseWriter, r *http.Request) {
	g, err := h.diagnostics.Gateway(r.Context())
	if err != nil {
		response.Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, gatewayDTO{Gateway: g.Address.String(), Dev: g.Device, Source: g.Source, Wifi: toHostWifiDTO(g.Wifi)})
}

// ScanWifi answers the Wi-Fi networks the bridge machine can see
func (h *Handler) ScanWifi(w http.ResponseWriter, r *http.Request) {
	// Scan, it takes several seconds because nmcli rescans the radio
	networks, err := h.diagnostics.ScanWifi(r.Context())
	if err != nil {
		response.Error(w, err)
		return
	}

	// Map the networks to their JSON shape
	out := make([]wifiNetworkDTO, 0, len(networks))
	for _, n := range networks {
		out = append(out, wifiNetworkDTO{SSID: n.SSID, Signal: n.Signal, FreqMHz: n.FreqMHz, Security: n.Security, Band24: n.Band24})
	}
	response.JSON(w, http.StatusOK, out)
}

// NotFound answers every path that is not in the route table
func (h *Handler) NotFound(w http.ResponseWriter, _ *http.Request) {
	response.Error(w, response.ErrNotFound)
}
