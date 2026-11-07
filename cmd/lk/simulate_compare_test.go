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

	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/livekit"
)

func TestCompareRunIDs(t *testing.T) {
	ids, err := compareRunIDs([]string{"SR_b", "SR_a"})
	require.NoError(t, err)
	require.Equal(t, []string{"SR_b", "SR_a"}, ids, "the first run stays the baseline")

	for _, args := range [][]string{
		nil,
		{"SR_a"},
		{"SR_a", "SR_a"},
		{"SR_a", "SR_b", "SR_c", "SR_d", "SR_e"},
	} {
		_, err := compareRunIDs(args)
		require.Error(t, err, "%v", args)
	}
}

func TestSimulationCompareDashboardURLNeedsAProject(t *testing.T) {
	require.Empty(t, simulationCompareDashboardURL("", []string{"SR_a", "SR_b"}))
	require.Contains(t, simulationCompareDashboardURL("p_1", []string{"SR_a", "SR_b"}), "/projects/p_1/simulations/compare?runs=SR_a,SR_b")
}

func TestCompareRunsJSONNeedsFinishedRuns(t *testing.T) {
	runs := map[string]*livekit.SimulationRun{
		"SR_a": {Id: "SR_a", Status: livekit.SimulationRun_STATUS_COMPLETED},
		"SR_b": {Id: "SR_b", Status: livekit.SimulationRun_STATUS_RUNNING},
		"SR_c": {Id: "SR_c", Status: livekit.SimulationRun_STATUS_FAILED},
	}
	fetch := func(id string) (*livekit.SimulationRun, error) { return runs[id], nil }

	_, err := compareRunsJSON([]string{"SR_a", "SR_b"}, fetch)
	require.ErrorContains(t, err, "SR_b is still in progress")

	export, err := compareRunsJSON([]string{"SR_c", "SR_a"}, fetch)
	require.NoError(t, err)
	require.Len(t, export.Runs, 2)
	require.Contains(t, string(export.Runs[0].Run), `"SR_c"`, "baseline stays first")
}
