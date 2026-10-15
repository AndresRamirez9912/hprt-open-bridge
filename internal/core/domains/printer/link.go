package printer

import "time"

// LinkStats summarizes the recent heartbeats as a measure of the Wi-Fi link quality
type LinkStats struct {
	Samples int
	LossPct int
	// RTTAvg and RTTMax are nil when every recent heartbeat was lost
	RTTAvg *time.Duration
	RTTMax *time.Duration
	// LastRTT is the latest heartbeat round trip, nil when it was lost
	LastRTT *time.Duration
}
