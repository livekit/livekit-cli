// Copyright 2021-2024 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package loadtester

import (
	"time"

	"go.uber.org/atomic"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

type testerStats struct {
	expectedTracks int
	trackStats     map[string]*trackStats
	err            error
}

type trackStats struct {
	trackID   string
	kind      lksdk.TrackKind
	startedAt atomic.Time
	packets   atomic.Int64
	bytes     atomic.Int64
	dropped   atomic.Int64
}

type summary struct {
	tracks    int
	expected  int
	packets   int64
	bytes     int64
	dropped   int64
	elapsed   time.Duration
	errString string
	errCount  int64
}

func getTestSummary(summaries map[string]*summary) *summary {
	s := &summary{}
	for _, testerSummary := range summaries {
		s.tracks += testerSummary.tracks
		s.expected += testerSummary.expected
		s.packets += testerSummary.packets
		s.bytes += testerSummary.bytes
		s.dropped += testerSummary.dropped
		if testerSummary.elapsed > s.elapsed {
			s.elapsed = testerSummary.elapsed
		}
		s.errCount += testerSummary.errCount
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
		s.dropped += trackStats.dropped.Load()
		elapsed := time.Since(trackStats.startedAt.Load())
		if elapsed > s.elapsed {
			s.elapsed = elapsed
		}
	}
	if testerStats.err == nil {
		s.errString = "-"
	} else {
		s.errString = testerStats.err.Error()
		s.errCount = 1
	}
	return s
}
