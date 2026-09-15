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
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

func attemptJob(id, scenario string, attempt int32, status livekit.SimulationRun_Job_Status) *livekit.SimulationRun_Job {
	return &livekit.SimulationRun_Job{Id: id, ScenarioId: scenario, Attempt: attempt, Status: status}
}

func TestScenarioPassCounts(t *testing.T) {
	const (
		done    = livekit.SimulationRun_Job_STATUS_COMPLETED
		failed  = livekit.SimulationRun_Job_STATUS_FAILED
		running = livekit.SimulationRun_Job_STATUS_RUNNING
	)
	run := &livekit.SimulationRun{Samples: 2, Jobs: []*livekit.SimulationRun_Job{
		attemptJob("SRJ_1", "SCN_a", 1, done), attemptJob("SRJ_2", "SCN_a", 2, done), // pass^k
		attemptJob("SRJ_3", "SCN_b", 1, done), attemptJob("SRJ_4", "SCN_b", 2, failed), // pass@k only
		attemptJob("SRJ_5", "SCN_c", 1, failed), attemptJob("SRJ_6", "SCN_c", 2, failed), // neither
		attemptJob("SRJ_7", "SCN_d", 1, done), attemptJob("SRJ_8", "SCN_d", 2, running), // not finished
	}}

	scenarios, passAny, passAll := scenarioPassCounts(run)
	require.Equal(t, 3, scenarios)
	require.Equal(t, 2, passAny)
	require.Equal(t, 1, passAll)
	require.Equal(t, "pass@2 2/3 (0.67), pass^2 1/3 (0.33)", passRateLine(run))

	run.Samples = 1
	require.Equal(t, "", passRateLine(run))
}

func TestSortedJobs_GroupsAttemptsInScenarioOrder(t *testing.T) {
	run := &livekit.SimulationRun{
		Samples:       2,
		ScenarioGroup: &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Id: "SCN_b"}, {Id: "SCN_a"}}},
		Jobs: []*livekit.SimulationRun_Job{
			attemptJob("SRJ_1", "SCN_a", 2, 0),
			attemptJob("SRJ_2", "SCN_b", 2, 0),
			attemptJob("SRJ_3", "SCN_a", 1, 0),
			attemptJob("SRJ_4", "SCN_b", 1, 0),
		},
	}
	var ids []string
	for _, j := range sortedJobs(run) {
		ids = append(ids, j.Id)
	}
	require.Equal(t, []string{"SRJ_4", "SRJ_2", "SRJ_3", "SRJ_1"}, ids)
	require.Equal(t, " (attempt 1/2)", attemptSuffix(run, run.Jobs[3]))
}

func repeatedFixture() *simulateModel {
	m := runningFixture()
	m.run = &livekit.SimulationRun{
		Id: "SR_fixture0001", Status: livekit.SimulationRun_STATUS_RUNNING, Samples: 2,
		ScenarioGroup: &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Id: "SCN_a"}, {Id: "SCN_b"}}},
		Jobs: []*livekit.SimulationRun_Job{
			attemptJob("SRJ_a1", "SCN_a", 1, livekit.SimulationRun_Job_STATUS_COMPLETED),
			attemptJob("SRJ_a2", "SCN_a", 2, livekit.SimulationRun_Job_STATUS_FAILED),
			attemptJob("SRJ_b1", "SCN_b", 1, livekit.SimulationRun_Job_STATUS_COMPLETED),
			attemptJob("SRJ_b2", "SCN_b", 2, livekit.SimulationRun_Job_STATUS_RUNNING),
		},
	}
	for _, j := range m.run.Jobs {
		j.Label = "scenario " + j.ScenarioId
	}
	return m
}

func TestFilteredJobs_RepeatedRunNestsAttempts(t *testing.T) {
	rows := repeatedFixture().filteredJobs()
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.id())
	}
	require.Equal(t, []string{"SCN_a", "SRJ_a1", "SRJ_a2", "SCN_b", "SRJ_b1", "SRJ_b2"}, ids)
	require.Equal(t, 1, rows[0].origIdx)
	require.Equal(t, 0, rows[1].origIdx, "attempt rows carry no number")
	require.True(t, rows[2].last)
	require.Equal(t, 2, rows[3].origIdx)

	icon, _ := scenarioStatusIcon(rows[0].scenario.attempts)
	require.Equal(t, '✗', icon, "a scenario with a failed attempt is not passed")
	icon, _ = scenarioStatusIcon(rows[3].scenario.attempts)
	require.Equal(t, '⏺', icon, "a scenario still runs while any attempt does")
}

func TestEnterOnScenarioRowOpensTheScenario(t *testing.T) {
	m := repeatedFixture()
	m.cursor = 0
	m.Update(keyPress("enter"))
	require.Equal(t, "SCN_a", m.detailID)
	require.NotNil(t, m.findScenario(m.detailID))
	require.Contains(t, m.renderDetail(), "1/2 attempts passed")

	m.closeDetailCmd()
	m.cursor = 2
	m.Update(keyPress("enter"))
	require.Equal(t, "SRJ_a2", m.detailID)
}
