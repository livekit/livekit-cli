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

func retryJob(label string, status livekit.SimulationRun_Job_Status) *livekit.SimulationRun_Job {
	return &livekit.SimulationRun_Job{Label: label, Status: status}
}

func retryRunOf(jobs ...*livekit.SimulationRun_Job) *livekit.SimulationRun {
	return &livekit.SimulationRun{
		Status: livekit.SimulationRun_STATUS_COMPLETED,
		Jobs:   jobs,
	}
}

const (
	jobPassed  = livekit.SimulationRun_Job_STATUS_COMPLETED
	jobFailed  = livekit.SimulationRun_Job_STATUS_FAILED
	jobRunning = livekit.SimulationRun_Job_STATUS_RUNNING
)

func TestMergedFinalRun(t *testing.T) {
	first := retryRunOf(
		retryJob("a", jobPassed),
		retryJob("b", jobFailed),
		retryJob("c", jobFailed),
	)

	t.Run("single attempt is returned as-is", func(t *testing.T) {
		require.Same(t, first, mergedFinalRun([]*livekit.SimulationRun{first}))
	})

	t.Run("last terminal outcome wins", func(t *testing.T) {
		retry1 := retryRunOf(retryJob("b", jobPassed), retryJob("c", jobFailed))
		retry2 := retryRunOf(retryJob("c", jobPassed))
		merged := mergedFinalRun([]*livekit.SimulationRun{first, retry1, retry2})
		require.Equal(t, []string(nil), failedScenarioKeys(merged))
		total, _, passed, failed := simulationJobCounts(merged)
		require.Equal(t, 3, total)
		require.Equal(t, 3, passed)
		require.Equal(t, 0, failed)
	})

	t.Run("a cancelled retry cannot launder a failure", func(t *testing.T) {
		// the retry was cancelled with the job still running: non-terminal
		// outcomes are ignored, the first run's failure stands
		retry := retryRunOf(retryJob("b", jobRunning), retryJob("c", jobPassed))
		merged := mergedFinalRun([]*livekit.SimulationRun{first, retry})
		require.Equal(t, []string{"b"}, failedScenarioKeys(merged))
	})

	t.Run("generated jobs without labels merge by instructions", func(t *testing.T) {
		f := retryRunOf(&livekit.SimulationRun_Job{Instructions: "ask for hours", Status: jobFailed})
		r := retryRunOf(&livekit.SimulationRun_Job{Instructions: "ask for hours", Status: jobPassed})
		merged := mergedFinalRun([]*livekit.SimulationRun{f, r})
		require.Empty(t, failedScenarioKeys(merged))
	})
}

func TestPassedOnRetry(t *testing.T) {
	first := retryRunOf(
		retryJob("a", jobPassed),
		retryJob("b", jobFailed),
		retryJob("c", jobFailed),
	)
	retry := retryRunOf(retryJob("b", jobPassed), retryJob("c", jobFailed))

	require.Empty(t, passedOnRetry([]*livekit.SimulationRun{first}))
	require.Equal(t, []string{"b"}, passedOnRetry([]*livekit.SimulationRun{first, retry}))
}

func TestRetryScenarioGroup(t *testing.T) {
	group := &livekit.ScenarioGroup{
		Name: "g",
		Scenarios: []*livekit.Scenario{
			{Label: "a"},
			{Label: "b"},
			{Instructions: "unlabeled"},
		},
	}

	t.Run("prefers the run's own group", func(t *testing.T) {
		run := &livekit.SimulationRun{ScenarioGroup: group}
		got := retryScenarioGroup(run, nil, []string{"b", "unlabeled"})
		require.Len(t, got.Scenarios, 2)
		require.Equal(t, "g", got.Name)
	})

	t.Run("falls back when the run carries no scenarios", func(t *testing.T) {
		got := retryScenarioGroup(&livekit.SimulationRun{}, group, []string{"a"})
		require.Len(t, got.Scenarios, 1)
		require.Equal(t, "a", got.Scenarios[0].Label)
	})

	t.Run("nil when nothing matches", func(t *testing.T) {
		run := &livekit.SimulationRun{ScenarioGroup: group}
		require.Nil(t, retryScenarioGroup(run, nil, []string{"missing"}))
	})
}
