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

func sampleJob(id, scenario string, sample int32, status livekit.SimulationRun_Job_Status) *livekit.SimulationRun_Job {
	return &livekit.SimulationRun_Job{Id: id, ScenarioId: scenario, Sample: sample, Status: status}
}

func sampling(k int32, rate float64) *livekit.SimulationRun_Sampling {
	return &livekit.SimulationRun_Sampling{Samples: k, PassRate: rate}
}

func TestScenarioPassCounts(t *testing.T) {
	const (
		done    = livekit.SimulationRun_Job_STATUS_COMPLETED
		failed  = livekit.SimulationRun_Job_STATUS_FAILED
		running = livekit.SimulationRun_Job_STATUS_RUNNING
	)
	run := &livekit.SimulationRun{Sampling: sampling(2, 1), Jobs: []*livekit.SimulationRun_Job{
		sampleJob("SRJ_1", "SCN_a", 1, done), sampleJob("SRJ_2", "SCN_a", 2, done), // pass^k
		sampleJob("SRJ_3", "SCN_b", 1, done), sampleJob("SRJ_4", "SCN_b", 2, failed), // pass@k only
		sampleJob("SRJ_5", "SCN_c", 1, failed), sampleJob("SRJ_6", "SCN_c", 2, failed), // neither
		sampleJob("SRJ_7", "SCN_d", 1, done), sampleJob("SRJ_8", "SCN_d", 2, running), // not finished
	}}

	scenarios, passAny, passAll := scenarioPassCounts(run)
	require.Equal(t, 3, scenarios)
	require.Equal(t, 2, passAny)
	require.Equal(t, 1, passAll)
	require.Equal(t, "pass@2 2/3 (0.67), pass^2 1/3 (0.33)", passRateLine(run))

	run.Sampling = nil
	require.Equal(t, "", passRateLine(run))
}

func TestSortedJobs_GroupsSamplesInScenarioOrder(t *testing.T) {
	run := &livekit.SimulationRun{
		Sampling:      sampling(2, defaultPassRate),
		ScenarioGroup: &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Id: "SCN_b"}, {Id: "SCN_a"}}},
		Jobs: []*livekit.SimulationRun_Job{
			sampleJob("SRJ_1", "SCN_a", 2, 0),
			sampleJob("SRJ_2", "SCN_b", 2, 0),
			sampleJob("SRJ_3", "SCN_a", 1, 0),
			sampleJob("SRJ_4", "SCN_b", 1, 0),
		},
	}
	var ids []string
	for _, j := range sortedJobs(run) {
		ids = append(ids, j.Id)
	}
	require.Equal(t, []string{"SRJ_4", "SRJ_2", "SRJ_3", "SRJ_1"}, ids)
	require.Equal(t, " (sample 1/2)", sampleSuffix(run, run.Jobs[3]))
}

func repeatedFixture() *simulateModel {
	m := runningFixture()
	m.run = &livekit.SimulationRun{
		Id: "SR_fixture0001", Status: livekit.SimulationRun_STATUS_RUNNING, Sampling: sampling(2, defaultPassRate),
		ScenarioGroup: &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Id: "SCN_a"}, {Id: "SCN_b"}}},
		Jobs: []*livekit.SimulationRun_Job{
			sampleJob("SRJ_a1", "SCN_a", 1, livekit.SimulationRun_Job_STATUS_COMPLETED),
			sampleJob("SRJ_a2", "SCN_a", 2, livekit.SimulationRun_Job_STATUS_FAILED),
			sampleJob("SRJ_b1", "SCN_b", 1, livekit.SimulationRun_Job_STATUS_COMPLETED),
			sampleJob("SRJ_b2", "SCN_b", 2, livekit.SimulationRun_Job_STATUS_RUNNING),
		},
	}
	for _, j := range m.run.Jobs {
		j.Label = "scenario " + j.ScenarioId
	}
	return m
}

func TestFilteredJobs_RepeatedRunNestsSamples(t *testing.T) {
	rows := repeatedFixture().filteredJobs()
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.id())
	}
	require.Equal(t, []string{"SCN_a", "SRJ_a1", "SRJ_a2", "SCN_b", "SRJ_b1", "SRJ_b2"}, ids)
	require.Equal(t, 1, rows[0].origIdx)
	require.Equal(t, 0, rows[1].origIdx, "sample rows carry no number")
	require.True(t, rows[2].last)
	require.Equal(t, 2, rows[3].origIdx)

	required := requiredSamples(repeatedFixture().run)
	icon, _ := scenarioStatusIcon(rows[0].scenario.samples, required)
	require.Equal(t, '✗', icon, "a scenario with a failed sample is not passed")
	icon, _ = scenarioStatusIcon(rows[3].scenario.samples, required)
	require.Equal(t, '⏺', icon, "a scenario still runs while any sample does")
}

func TestEnterOnScenarioRowOpensTheScenario(t *testing.T) {
	m := repeatedFixture()
	m.cursor = 0
	m.Update(keyPress("enter"))
	require.Equal(t, "SCN_a", m.detailID)
	require.NotNil(t, m.findScenario(m.detailID))
	require.Contains(t, m.renderDetail(), "1/2 samples passed")

	m.closeDetailCmd()
	m.cursor = 2
	m.Update(keyPress("enter"))
	require.Equal(t, "SRJ_a2", m.detailID)
}

func TestRequiredSamples_RoundsToWholeSamples(t *testing.T) {
	require.Equal(t, 1, requiredSamples(nil), "an unsampled run needs its one sample")
	require.Equal(t, 1, requiredSamples(&livekit.SimulationRun{Sampling: sampling(1, 0.5)}))

	// Every rate in [0.5, 0.833] is the same gate at three samples.
	for _, rate := range []float64{0.5, 0.67, 0.75, 0.8} {
		require.Equal(t, 2, requiredSamples(&livekit.SimulationRun{Sampling: sampling(3, rate)}),
			"rate %v of 3", rate)
	}
	require.Equal(t, 1, requiredSamples(&livekit.SimulationRun{Sampling: sampling(3, 0.34)}))
	require.Equal(t, 3, requiredSamples(&livekit.SimulationRun{Sampling: sampling(3, 1)}))

	// A rate that rounds to zero would pass a scenario that never succeeded.
	require.Equal(t, 1, requiredSamples(&livekit.SimulationRun{Sampling: sampling(3, 0.01)}))
}

func TestScenarioFailureCounts_GatesOnThePassRate(t *testing.T) {
	const (
		done    = livekit.SimulationRun_Job_STATUS_COMPLETED
		failed  = livekit.SimulationRun_Job_STATUS_FAILED
		running = livekit.SimulationRun_Job_STATUS_RUNNING
	)
	// 0.75 of 3 needs 2: SCN_a flakes once and passes, SCN_b does not.
	run := &livekit.SimulationRun{Sampling: sampling(3, defaultPassRate), Jobs: []*livekit.SimulationRun_Job{
		sampleJob("SRJ_1", "SCN_a", 1, done), sampleJob("SRJ_2", "SCN_a", 2, failed), sampleJob("SRJ_3", "SCN_a", 3, done),
		sampleJob("SRJ_4", "SCN_b", 1, done), sampleJob("SRJ_5", "SCN_b", 2, failed), sampleJob("SRJ_6", "SCN_b", 3, failed),
	}}
	scenarios, failedScenarios := scenarioFailureCounts(run)
	require.Equal(t, 2, scenarios)
	require.Equal(t, 1, failedScenarios)
	require.EqualError(t, runFailureError(run), "1 of 2 scenarios failed")

	// A scenario that can still reach the bar is not yet a failure.
	run.Jobs[5].Status = running
	_, failedScenarios = scenarioFailureCounts(run)
	require.Equal(t, 0, failedScenarios)

	// Without sampling every job is its own scenario and one failure fails it.
	plain := &livekit.SimulationRun{Jobs: []*livekit.SimulationRun_Job{
		{Id: "SRJ_1", Status: done}, {Id: "SRJ_2", Status: failed},
	}}
	scenarios, failedScenarios = scenarioFailureCounts(plain)
	require.Equal(t, 2, scenarios)
	require.Equal(t, 1, failedScenarios)
}
