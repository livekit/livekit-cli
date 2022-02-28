package loadtester

import (
	"time"

	lksdk "github.com/livekit/server-sdk-go"
	"go.uber.org/atomic"
)

type testerStats struct {
	expectedTracks int
	trackStats     map[string]*trackStats
}

type trackStats struct {
	trackID      string
	kind         lksdk.TrackKind
	startedAt    atomic.Time
	packets      atomic.Int64
	bytes        atomic.Int64
	latency      atomic.Int64
	latencyCount atomic.Int64
	dropped      atomic.Int64
}

type summary struct {
	tracks       int
	expected     int
	packets      int64
	bytes        int64
	latency      int64
	latencyCount int64
	dropped      int64
	elapsed      time.Duration
}

func getTestSummary(summaries map[string]*summary) *summary {
	s := &summary{}
	for _, testerSummary := range summaries {
		s.tracks += testerSummary.tracks
		s.expected += testerSummary.expected
		s.packets += testerSummary.packets
		s.bytes += testerSummary.bytes
		s.latency += testerSummary.latency
		s.latencyCount += testerSummary.latencyCount
		s.dropped += testerSummary.dropped
		if testerSummary.elapsed > s.elapsed {
			s.elapsed = testerSummary.elapsed
		}
	}
	return s
}

func getTesterSummary(testerStats *testerStats) *summary {
	s := &summary{
		expected: testerStats.expectedTracks,
	}
	for _, trackStats := range testerStats.trackStats {
		s.tracks++
		s.packets += trackStats.packets.Load()
		s.bytes += trackStats.bytes.Load()
		s.latency += trackStats.latency.Load()
		s.latencyCount += trackStats.latencyCount.Load()
		s.dropped += trackStats.dropped.Load()
		elapsed := time.Since(trackStats.startedAt.Load())
		if elapsed > s.elapsed {
			s.elapsed = elapsed
		}
	}
	return s
}
