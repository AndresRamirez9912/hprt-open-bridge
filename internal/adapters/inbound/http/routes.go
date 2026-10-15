package http

import (
	"net/http"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/middleware"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/response"
)

// Route describes one endpoint of the API
//
// The table returned by Routes is the single source of truth of the HTTP contract: the
// router is built from it and its metadata is enough to generate an OpenAPI document later
type Route struct {
	// OperationID is the unique name of the operation
	OperationID string
	Method      string
	Path        string
	Summary     string
	// Tag groups the routes by use case
	Tag string
	// Query lists the accepted query parameters
	Query []Param
	// Request describes the body, nil when the route takes none
	Request *Body
	// Responses lists the possible answers
	Responses []Response
	// Handler serves the route
	Handler http.HandlerFunc
}

// Param describes one query parameter of a route
type Param struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

// Body describes a request or response body, Schema is a zero value of its DTO
type Body struct {
	ContentType string
	Schema      any
}

// Response describes one possible answer of a route
type Response struct {
	Status      int
	Description string
	Body        Body
}

// Tags of the route table, one per use case
const (
	tagPrinter     = "printer"
	tagPhoto       = "photo"
	tagDiagnostics = "diagnostics"
	tagActivity    = "activity"
)

// Content types used by the API
const (
	contentJSON = "application/json"
	contentJPEG = "image/jpeg"
	contentSSE  = "text/event-stream"
)

// jsonBody describes a JSON body whose shape is the given DTO
func jsonBody(schema any) Body { return Body{ContentType: contentJSON, Schema: schema} }

// errorResponse is the answer shared by every failing route
func errorResponse(status int, description string) Response {
	return Response{Status: status, Description: description, Body: jsonBody(response.ErrorBody{})}
}

// Routes returns the route table of the bridge API
func Routes(h *Handler) []Route {
	return []Route{
		// Printer connection
		{
			OperationID: "getState", Method: http.MethodGet, Path: "/api/state", Tag: tagPrinter,
			Summary:   "Current connection snapshot of the printer",
			Responses: []Response{{Status: http.StatusOK, Description: "Snapshot", Body: jsonBody(stateDTO{})}},
			Handler:   h.GetState,
		},

		// Live activity
		{
			OperationID: "streamEvents", Method: http.MethodGet, Path: "/api/events", Tag: tagActivity,
			Summary:   "Live events: state, job and log, the first one is the current state",
			Responses: []Response{{Status: http.StatusOK, Description: "Server sent events", Body: Body{ContentType: contentSSE, Schema: jobDTO{}}}},
			Handler:   h.Events,
		},
		{
			OperationID: "getLogs", Method: http.MethodGet, Path: "/api/logs", Tag: tagActivity,
			Summary:   "Recent protocol log, oldest first",
			Responses: []Response{{Status: http.StatusOK, Description: "Log lines", Body: jsonBody([]logDTO{})}},
			Handler:   h.GetLogs,
		},

		// Finding and connecting printers
		{
			OperationID: "discoverPrinters", Method: http.MethodGet, Path: "/api/discover", Tag: tagPrinter,
			Summary: "Find printers on the local network with the UDP probe",
			Query: []Param{
				{Name: "timeout", Type: "integer", Description: "Search time in milliseconds, 6000 by default and 20000 at most"},
				{Name: "target", Type: "string", Description: "IPv4 address to probe instead of broadcasting"},
			},
			Responses: []Response{
				{Status: http.StatusOK, Description: "Printers found", Body: jsonBody([]discoveredDTO{})},
				errorResponse(http.StatusBadRequest, "Invalid target"),
			},
			Handler: h.Discover,
		},
		{
			OperationID: "connectPrinter", Method: http.MethodPost, Path: "/api/connect", Tag: tagPrinter,
			Summary: "Open a session with a printer, it is kept alive and reconnected until disconnect",
			Request: &Body{ContentType: contentJSON, Schema: connectRequestDTO{}},
			Responses: []Response{
				{Status: http.StatusOK, Description: "Connected", Body: jsonBody(stateDTO{})},
				errorResponse(http.StatusBadRequest, "Invalid IP address"),
				errorResponse(http.StatusBadGateway, "Could not connect"),
			},
			Handler: h.Connect,
		},
		{
			OperationID: "disconnectPrinter", Method: http.MethodPost, Path: "/api/disconnect", Tag: tagPrinter,
			Summary:   "Close the session and stop reconnecting",
			Responses: []Response{{Status: http.StatusOK, Description: "Disconnected", Body: jsonBody(disconnectedDTO{})}},
			Handler:   h.Disconnect,
		},

		// Printing
		{
			OperationID: "printPhoto", Method: http.MethodPost, Path: "/api/print", Tag: tagPhoto,
			Summary: "Print a ready to print JPEG, the answer arrives when the printer finishes",
			Request: &Body{ContentType: contentJPEG},
			Responses: []Response{
				{Status: http.StatusOK, Description: "Printed", Body: jsonBody(printResultDTO{})},
				errorResponse(http.StatusBadRequest, "Body must be a JPEG image"),
				errorResponse(http.StatusConflict, "No printer connected or a job is already running"),
				errorResponse(http.StatusRequestEntityTooLarge, "Payload too large"),
				{Status: http.StatusInternalServerError, Description: "The job failed", Body: jsonBody(printResultDTO{})},
			},
			Handler: h.Print,
		},
		{
			OperationID: "cancelPrint", Method: http.MethodPost, Path: "/api/cancel", Tag: tagPhoto,
			Summary:   "Cancel the running print job",
			Responses: []Response{{Status: http.StatusOK, Description: "Whether a job was cancelled", Body: jsonBody(cancelDTO{})}},
			Handler:   h.Cancel,
		},

		// Network diagnostics of the bridge machine
		{
			OperationID: "testLink", Method: http.MethodGet, Path: "/api/link", Tag: tagDiagnostics,
			Summary:   "Ping the printer and report the Wi-Fi of the bridge machine and the heartbeats",
			Query:     []Param{{Name: "ip", Type: "string", Description: "Printer IPv4, the connected printer by default"}},
			Responses: []Response{{Status: http.StatusOK, Description: "Link report", Body: jsonBody(linkReportDTO{})}, errorResponse(http.StatusBadRequest, "No printer IP")},
			Handler:   h.LinkTest,
		},
		{
			OperationID: "getGateway", Method: http.MethodGet, Path: "/api/gateway", Tag: tagDiagnostics,
			Summary:   "Router of the bridge machine, the printer itself in hotspot mode",
			Responses: []Response{{Status: http.StatusOK, Description: "Gateway", Body: jsonBody(gatewayDTO{})}, errorResponse(http.StatusNotFound, "No IPv4 network on Wi-Fi")},
			Handler:   h.Gateway,
		},
		{
			OperationID: "scanWifi", Method: http.MethodGet, Path: "/api/wifi/scan", Tag: tagDiagnostics,
			Summary:   "Wi-Fi networks seen by the bridge machine, 2.4 GHz first",
			Responses: []Response{{Status: http.StatusOK, Description: "Networks", Body: jsonBody([]wifiNetworkDTO{})}, errorResponse(http.StatusServiceUnavailable, "Scan not available")},
			Handler:   h.ScanWifi,
		},
	}
}

// NewRouter registers every route of the table and wraps the result with the middleware chain
func NewRouter(h *Handler, chain middleware.Chain) http.Handler {
	// Register every route of the table as METHOD /path
	mux := http.NewServeMux()
	for _, route := range Routes(h) {
		mux.HandleFunc(route.Method+" "+route.Path, route.Handler)
	}

	// Any other path or method answers the JSON not found error of the API
	mux.HandleFunc("/", h.NotFound)

	// Every request goes through the middleware chain before reaching the mux
	return chain.Then(mux)
}
