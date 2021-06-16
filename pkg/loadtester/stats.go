package loadtester

import "time"

type testerStats struct {
	expectedTracks int
	trackStats     map[string]*trackStats
}

type trackStats struct {
	trackID string

	startedAt time.Time
	resets    int64
	max       int64

	packets      int64
	bytes        int64
	latency      int64
	latencyCount int64
	ooo          int64
	missing      map[int64]bool
}

type summary struct {
	tracks       int
	expected     int
	packets      int64
	bytes        int64
	latency      int64
	latencyCount int64
	ooo          int64
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
		s.ooo += testerSummary.ooo
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
		s.packets += trackStats.packets
		s.bytes += trackStats.bytes
		s.latency += trackStats.latency
		s.latencyCount += trackStats.latencyCount
		s.ooo += trackStats.ooo
		s.dropped += int64(len(trackStats.missing))
		s.elapsed += time.Since(trackStats.startedAt)
	}
	return s
}
