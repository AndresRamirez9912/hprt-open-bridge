package cp4x

import (
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// linkMeter measures the link with the heartbeats, an unanswered query counts as lost
//
// Concurrency: it has no lock of its own, the Session calls it with Session.mu held
type linkMeter struct {
	size      int
	lostAfter time.Duration
	pending   []time.Time
	// samples holds the recent round trips, nil marks a lost heartbeat
	samples []*time.Duration
}

// ping records that a status query was just sent
func (m *linkMeter) ping(now time.Time) {
	m.pending = append(m.pending, now)
}

// pong records that a status block arrived and pairs it with the oldest query still alive
func (m *linkMeter) pong(now time.Time) {
	// Queries older than lostAfter will never be answered: count them as lost
	for len(m.pending) > 0 && now.Sub(m.pending[0]) > m.lostAfter {
		m.pending = m.pending[1:]
		m.add(nil)
	}

	// A status block the printer pushed on its own answers no query
	if len(m.pending) == 0 {
		return
	}

	// The answer belongs to the oldest query still alive
	rtt := now.Sub(m.pending[0])
	m.pending = m.pending[1:]
	m.add(&rtt)
}

// add keeps one sample and forgets the oldest ones beyond size
func (m *linkMeter) add(rtt *time.Duration) {
	// Keep only the newest size samples
	m.samples = append(m.samples, rtt)
	if len(m.samples) > m.size {
		m.samples = m.samples[len(m.samples)-m.size:]
	}
}

// stats summarizes the samples plus the queries already too old to be answered
func (m *linkMeter) stats(now time.Time) *printer.LinkStats {
	// The answered samples plus the queries that are already too old to be answered
	samples := append([]*time.Duration(nil), m.samples...)
	for _, sent := range m.pending {
		if now.Sub(sent) > m.lostAfter {
			samples = append(samples, nil)
		}
	}
	if len(samples) > m.size {
		samples = samples[len(samples)-m.size:]
	}

	// No heartbeat yet
	if len(samples) == 0 {
		return nil
	}

	// Sum and peak of the answered heartbeats
	var sum, peak time.Duration
	answered := 0
	for _, s := range samples {
		if s == nil {
			continue
		}
		answered++
		sum += *s
		peak = max(peak, *s)
	}

	// Loss is rounded like the Node bridge, the last sample may be a lost one
	out := &printer.LinkStats{
		Samples: len(samples),
		LossPct: ((len(samples)-answered)*100 + len(samples)/2) / len(samples),
		LastRTT: samples[len(samples)-1],
	}

	// The average only exists when at least one heartbeat was answered
	if answered > 0 {
		avg := sum / time.Duration(answered)
		out.RTTAvg, out.RTTMax = &avg, &peak
	}
	return out
}
