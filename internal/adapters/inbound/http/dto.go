package http

import (
	"encoding/hex"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/response"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// The DTOs below are the JSON contract of the API, the same one the Node bridge exposes

// stateDTO is the connection snapshot sent by GET /api/state and the state events
type stateDTO struct {
	IP           string     `json:"ip,omitempty"`
	Connected    bool       `json:"connected"`
	Reconnecting bool       `json:"reconnecting"`
	Info         *infoDTO   `json:"info,omitempty"`
	Status       *statusDTO `json:"status"`
	Link         *linkDTO   `json:"link"`
	Reserved     bool       `json:"reserved"`
	Printing     bool       `json:"printing"`
}

// disconnectedDTO is the short snapshot sent when no printer is connected
type disconnectedDTO struct {
	Connected bool `json:"connected"`
}

// infoDTO is the identity of the printer, unknown values are null
type infoDTO struct {
	Name     *string `json:"name"`
	SN       *string `json:"sn"`
	Model    *string `json:"model"`
	Firmware string  `json:"firmware,omitempty"`
}

// statusDTO is the decoded status block of the printer
type statusDTO struct {
	Battery      int        `json:"battery"`
	PrintCounter uint16     `json:"printCounter"`
	Firmware     string     `json:"firmware"`
	Errors       []errorDTO `json:"errors"`
	Notices      []string   `json:"notices"`
	StageCode    int32      `json:"stageCode"`
	Stage        stageDTO   `json:"stage"`
	Token        [2]byte    `json:"token"`
	Raw          string     `json:"raw"`
}

// errorDTO is one error flag of the printer
type errorDTO struct {
	Code  int    `json:"code"`
	Label string `json:"label"`
}

// stageDTO names a print stage
type stageDTO struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// linkDTO is the heartbeat statistics in milliseconds
type linkDTO struct {
	Samples   int    `json:"samples"`
	LossPct   int    `json:"lossPct"`
	RTTAvgMs  *int64 `json:"rttAvgMs"`
	RTTMaxMs  *int64 `json:"rttMaxMs"`
	LastRTTMs *int64 `json:"lastRttMs"`
}

// jobDTO is one print job event
type jobDTO struct {
	Phase       string `json:"phase"`
	Label       string `json:"label,omitempty"`
	Progress    *int   `json:"progress,omitempty"`
	Rate        int    `json:"rate,omitempty"`
	StageCode   *int32 `json:"stageCode,omitempty"`
	Key         string `json:"key,omitempty"`
	OK          *bool  `json:"ok,omitempty"`
	Error       string `json:"error,omitempty"`
	Unconfirmed bool   `json:"unconfirmed,omitempty"`
}

// logDTO is one protocol log line with its time in epoch milliseconds
type logDTO struct {
	T   int64  `json:"t"`
	Msg string `json:"msg"`
}

// discoveredDTO is a printer that answered the discovery probe
type discoveredDTO struct {
	IP   string `json:"ip"`
	Name string `json:"name"`
	SN   string `json:"sn"`
}

// connectRequestDTO is the body of POST /api/connect
type connectRequestDTO struct {
	IP     string `json:"ip"`
	Name   string `json:"name,omitempty"`
	SN     string `json:"sn,omitempty"`
	WakeMs *int64 `json:"wakeMs,omitempty"`
}

// printResultDTO is the answer of POST /api/print, a failed job also carries the error fields
type printResultDTO struct {
	OK          bool   `json:"ok"`
	Unconfirmed bool   `json:"unconfirmed,omitempty"`
	Error       string `json:"error,omitempty"`
	*response.ErrorFields
}

// cancelDTO is the answer of POST /api/cancel
type cancelDTO struct {
	Cancelled bool `json:"cancelled"`
}

// linkReportDTO is the answer of GET /api/link
type linkReportDTO struct {
	IP        string       `json:"ip"`
	Ping      *pingDTO     `json:"ping"`
	PCWifi    *hostWifiDTO `json:"pcWifi"`
	Heartbeat *linkDTO     `json:"heartbeat"`
}

// pingDTO is an ICMP ping series in milliseconds
type pingDTO struct {
	Sent     int     `json:"sent"`
	LossPct  float64 `json:"lossPct"`
	RTTMinMs *int64  `json:"rttMinMs"`
	RTTAvgMs *int64  `json:"rttAvgMs"`
	RTTMaxMs *int64  `json:"rttMaxMs"`
}

// hostWifiDTO is the Wi-Fi network of the bridge machine
type hostWifiDTO struct {
	SSID    string `json:"ssid"`
	Signal  int    `json:"signal"`
	Freq    string `json:"freq"`
	Channel int    `json:"channel"`
}

// gatewayDTO is the answer of GET /api/gateway
type gatewayDTO struct {
	Gateway string       `json:"gateway"`
	Dev     string       `json:"dev"`
	Source  string       `json:"source"`
	Wifi    *hostWifiDTO `json:"wifi"`
}

// wifiNetworkDTO is one network of GET /api/wifi/scan
type wifiNetworkDTO struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	FreqMHz  int    `json:"freqMHz"`
	Security string `json:"security"`
	Band24   bool   `json:"band24"`
}

// toStateDTO maps the connection snapshot, the short form when no printer is connected
func toStateDTO(s printer.State) any {
	// Without a printer the Node bridge answers only the connected flag
	if !s.IP.IsValid() {
		return disconnectedDTO{}
	}

	// Unknown identity fields are sent as null, the web app checks for that
	return stateDTO{
		IP:           s.IP.String(),
		Connected:    s.Connected,
		Reconnecting: s.Reconnecting,
		Info: &infoDTO{
			Name:     nullable(s.Info.Name),
			SN:       nullable(s.Info.SN),
			Model:    nullable(s.Info.Model),
			Firmware: s.Info.Firmware,
		},
		Status:   toStatusDTO(s.Status),
		Link:     toLinkDTO(s.Link),
		Reserved: s.Reserved,
		Printing: s.Printing,
	}
}

// toStatusDTO maps a status block, nil stays nil
func toStatusDTO(s *printer.Status) *statusDTO {
	if s == nil {
		return nil
	}

	// Empty lists are sent as [] instead of null
	errs := make([]errorDTO, 0, len(s.Errors))
	for _, e := range s.Errors {
		errs = append(errs, errorDTO{Code: int(e.Code), Label: e.Label})
	}
	notices := append([]string{}, s.Notices...)

	// The raw block is sent in hex for the protocol log of the web app
	return &statusDTO{
		Battery:      s.Battery,
		PrintCounter: s.PrintCounter,
		Firmware:     s.Firmware,
		Errors:       errs,
		Notices:      notices,
		StageCode:    int32(s.Stage),
		Stage:        stageDTO{Key: s.Stage.Key(), Label: s.Stage.Label()},
		Token:        s.Token,
		Raw:          hex.EncodeToString(s.Raw),
	}
}

// toLinkDTO maps the heartbeat statistics, nil stays nil
func toLinkDTO(l *printer.LinkStats) *linkDTO {
	if l == nil {
		return nil
	}
	return &linkDTO{Samples: l.Samples, LossPct: l.LossPct, RTTAvgMs: ms(l.RTTAvg), RTTMaxMs: ms(l.RTTMax), LastRTTMs: ms(l.LastRTT)}
}

// toJobDTO maps a job event with the fields its phase carries
func toJobDTO(e printer.JobEvent) jobDTO {
	// Fields every phase may carry
	out := jobDTO{Phase: string(e.Phase), Label: e.Label, Rate: e.RateKBps}

	// Fields that only some phases carry, so they are absent from the other ones
	switch e.Phase {
	case printer.PhaseSending, printer.PhaseSent:
		progress := e.Progress
		out.Progress = &progress
	case printer.PhaseStage:
		if e.Stage != nil {
			code := int32(*e.Stage)
			out.StageCode, out.Key = &code, e.Stage.Key()
		}
	case printer.PhaseDone, printer.PhaseFailed:
		ok := e.OK
		out.OK, out.Error, out.Unconfirmed = &ok, e.Error, e.Unconfirmed
	}
	return out
}

// toLogDTO maps one protocol log line
func toLogDTO(e printer.LogEntry) logDTO {
	return logDTO{T: e.Time.UnixMilli(), Msg: e.Message}
}

// toHostWifiDTO maps the Wi-Fi network of the bridge machine, nil stays nil
func toHostWifiDTO(w *network.HostWifi) *hostWifiDTO {
	if w == nil {
		return nil
	}
	return &hostWifiDTO{SSID: w.SSID, Signal: w.Signal, Freq: w.Freq, Channel: w.Channel}
}

// toLinkReportDTO maps the result of a link test
func toLinkReportDTO(r *network.LinkReport) linkReportDTO {
	out := linkReportDTO{IP: r.IP.String(), PCWifi: toHostWifiDTO(r.HostWifi), Heartbeat: toLinkDTO(r.Heartbeat)}

	// Without ping on the machine the field stays null
	if p := r.Ping; p != nil {
		out.Ping = &pingDTO{Sent: p.Sent, LossPct: p.LossPct, RTTMinMs: ms(p.RTTMin), RTTAvgMs: ms(p.RTTAvg), RTTMaxMs: ms(p.RTTMax)}
	}
	return out
}

// nullable turns an empty string into a JSON null
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ms converts an optional duration to whole milliseconds
func ms(d *time.Duration) *int64 {
	if d == nil {
		return nil
	}
	v := d.Milliseconds()
	return &v
}
