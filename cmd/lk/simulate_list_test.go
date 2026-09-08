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
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSimulationRunRow(t *testing.T) {
	created := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	row := simulationRunRow(&livekit.SimulationRun{
		Id:          "run_123",
		CreatedAt:   timestamppb.New(created),
		AgentName:   "my-agent",
		Mode:        livekit.SimulationMode_SIMULATION_MODE_AUDIO,
		Status:      livekit.SimulationRun_STATUS_COMPLETED,
		JobCount:    5,
		PassedCount: 4,
		FailedCount: 1,
	})
	require.Equal(t, []string{"run_123", "2026-09-08T17:00:00Z", "my-agent", "AUDIO", "COMPLETED", "4", "1", "5"}, row)
}

func TestSimulationRunRowUnspecifiedModeIsText(t *testing.T) {
	row := simulationRunRow(&livekit.SimulationRun{Id: "run_1"})
	require.Equal(t, "TEXT", row[3])
	require.Equal(t, "--", row[1])
}
