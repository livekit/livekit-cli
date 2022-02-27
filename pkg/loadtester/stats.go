package loadtester

import (
	"sync/atomic"
	"time"

	lksdk "github.com/livekit/server-sdk-go"
)

type testerStats struct {
	expectedTracks int
	trackStats     map[string]*trackStats
}

type trackStats struct {
	trackID      string
	kind         lksdk.TrackKind
	startedAt    time.Time
	packets      int64
	bytes        int64
	latency      int64
	latencyCount int64
	dropped      int64
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
		s.elapsed += testerSummary.elapsed
	}
	return s
}

func getTesterSummary(testerStats *testerStats) *summary {
	s := &summary{
		expected: testerStats.expectedTracks,
	}
	for _, trackStats := range testerStats.trackStats {
		s.tracks++
		s.packets += atomic.LoadInt64(&trackStats.packets)
		s.bytes += atomic.LoadInt64(&trackStats.bytes)
		s.latency += atomic.LoadInt64(&trackStats.latency)
		s.latencyCount += atomic.LoadInt64(&trackStats.latencyCount)
		s.dropped += atomic.LoadInt64(&trackStats.dropped)
		s.elapsed += time.Since(trackStats.startedAt)
	}
	return s
}
