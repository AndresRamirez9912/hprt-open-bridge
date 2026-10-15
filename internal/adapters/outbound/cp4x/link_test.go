package cp4x

import (
	"testing"
	"time"
)

// TestLinkMeter checks the heartbeat statistics for answered, lost and pending queries
func TestLinkMeter(t *testing.T) {
	ms := time.Millisecond

	// A negative round trip is a query that never got an answer
	testCases := []struct {
		name      string
		rtts      []time.Duration
		pending   int
		wantNil   bool
		samples   int
		lossPct   int
		avg, peak time.Duration
		lastLost  bool
	}{
		{name: "no heartbeat yet", wantNil: true},
		{name: "all answered", rtts: []time.Duration{10 * ms, 20 * ms, 30 * ms}, samples: 3, avg: 20 * ms, peak: 30 * ms},
		{name: "one lost in the middle", rtts: []time.Duration{10 * ms, -1, 30 * ms}, samples: 3, lossPct: 33, avg: 20 * ms, peak: 30 * ms},
		{name: "last ones pending too long", rtts: []time.Duration{10 * ms}, pending: 1, samples: 2, lossPct: 50, avg: 10 * ms, peak: 10 * ms, lastLost: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Replay the heartbeats on a fake clock
			m := linkMeter{size: 40, lostAfter: time.Second}
			now := time.Unix(1000, 0)
			for _, rtt := range tc.rtts {
				m.ping(now)
				if rtt < 0 {
					// The answer of the next query arrives once this one is too old
					now = now.Add(2 * time.Second)
					continue
				}
				now = now.Add(rtt)
				m.pong(now)
			}

			// Queries still waiting, read once they are older than lostAfter
			for i := 0; i < tc.pending; i++ {
				m.ping(now)
			}
			stats := m.stats(now.Add(2 * time.Second))

			// Without heartbeats there are no statistics
			if tc.wantNil {
				if stats != nil {
					t.Fatalf("stats = %+v, want nil", stats)
				}
				return
			}

			// Counters, averages and the last sample
			if stats == nil || stats.Samples != tc.samples || stats.LossPct != tc.lossPct {
				t.Fatalf("stats = %+v, want samples %d loss %d", stats, tc.samples, tc.lossPct)
			}
			if *stats.RTTAvg != tc.avg || *stats.RTTMax != tc.peak {
				t.Errorf("avg %v peak %v, want %v %v", *stats.RTTAvg, *stats.RTTMax, tc.avg, tc.peak)
			}
			if (stats.LastRTT == nil) != tc.lastLost {
				t.Errorf("LastRTT = %v, want lost %v", stats.LastRTT, tc.lastLost)
			}
		})
	}
}
