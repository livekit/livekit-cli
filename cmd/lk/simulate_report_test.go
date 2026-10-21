// Copyright 2026 LiveKit, Inc.
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

package main

import (
	"bytes"
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

func TestSimLogQuotaExceeded_NoteAndClosingLine(t *testing.T) {
	var buf bytes.Buffer
	l := newSimLog(&buf, &buf)

	l.QuotaExceeded("LLM tokens-per-minute rate limit", 5)
	require.Contains(t, buf.String(), "LLM tokens-per-minute rate limit")
	require.Contains(t, buf.String(), "--concurrency 5")

	// Results repeats the note as a closing line after the per-job output.
	buf.Reset()
	l.Results(&livekit.SimulationRun{}, nil)
	require.Contains(t, buf.String(), "--concurrency 5")
}

func TestSimLogResults_NoQuotaNoteByDefault(t *testing.T) {
	var buf bytes.Buffer
	l := newSimLog(&buf, &buf)
	l.Results(&livekit.SimulationRun{}, nil)
	require.NotContains(t, buf.String(), "--concurrency")
}

func TestWriteRunResults_HistoricallyRed(t *testing.T) {
	const (
		done   = livekit.SimulationRun_Job_STATUS_COMPLETED
		failed = livekit.SimulationRun_Job_STATUS_FAILED
	)
	red := []bool{false, false, true, false, false, false, false, false, false, true}
	job := func(id, scenario, label string, sample int32, status livekit.SimulationRun_Job_Status, prior []bool, historicallyRed bool) *livekit.SimulationRun_Job {
		return &livekit.SimulationRun_Job{
			Id: id, ScenarioId: scenario, Label: label, Sample: sample, Status: status,
			Error: "agent hung up", PriorPassed: prior, HistoricallyRed: historicallyRed,
		}
	}
	// 0.75 of 2 needs both samples: SCN_a and SCN_c fail, SCN_b passes.
	run := &livekit.SimulationRun{
		Sampling:      sampling(2, defaultPassRate),
		ScenarioGroup: &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Id: "SCN_a"}, {Id: "SCN_b"}, {Id: "SCN_c"}}},
		Jobs: []*livekit.SimulationRun_Job{
			job("SRJ_a1", "SCN_a", "Refund", 1, failed, red, true),
			job("SRJ_a2", "SCN_a", "Refund", 2, failed, red, true),
			job("SRJ_b1", "SCN_b", "Greeting", 1, done, red, true),
			job("SRJ_b2", "SCN_b", "Greeting", 2, done, red, true),
			job("SRJ_c1", "SCN_c", "Booking", 1, failed, []bool{true, true, false}, false),
			job("SRJ_c2", "SCN_c", "Booking", 2, done, []bool{true, true, false}, false),
		},
	}

	var buf bytes.Buffer
	writeRunResults(&buf, run, nil)
	out := buf.String()
	require.Contains(t, out, "::group::✗ Refund (sample 1/2) [historically red: failed 8 of last 10 runs] (SRJ_a1)\n")
	require.Contains(t, out, "::error::Job 1 failed: agent hung up [historically red: failed 8 of last 10 runs]\n")
	require.Contains(t, out, "::group::✓ Greeting (sample 1/2) (SRJ_b1)\n")
	require.Contains(t, out, "::group::✗ Booking (sample 1/2) (SRJ_c1)\n")

	require.Equal(t,
		"1 of 2 failed scenarios were already failing before this run (historically red): Refund",
		historicallyRedLine(run))

	run.Jobs[0].HistoricallyRed, run.Jobs[1].HistoricallyRed = false, false
	require.Equal(t, "", historicallyRedLine(run))
}
