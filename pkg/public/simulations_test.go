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

package public

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestCreateSimulationRunForwardsAllOptions(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"simulationRunId":"SR_1"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	mode := oapi.SIMULATIONMODEAUDIO
	resp, err := c.CreateSimulationRun(context.Background(), "p_1", oapi.SimulationServiceCreateSimulationRunJSONRequestBody{
		AgentName:            ptr("support-agent"),
		Deployment:           ptr("canary"),
		NumSimulations:       ptr(int32(5)),
		Concurrency:          ptr(int32(4)),
		Mode:                 &mode,
		ScenarioGroup:        &oapi.LivekitScenarioGroup{Name: ptr("smoke"), Scenarios: &[]oapi.LivekitScenario{{Label: ptr("greet")}}},
		BackgroundNoise:      ptr(true),
		LowQualityMicrophone: ptr(true),
		PacketLoss:           ptr(true),
		Ci:                   &oapi.LivekitSimulationRunCI{Provider: ptr("github_actions")},
		Sampling:             &oapi.LivekitSimulationRunSamplingOptions{Samples: ptr(int32(3)), PassRate: ptr(0.75)},
		SipCallTo:            ptr("+14155550100"),
		SipTrunkId:           ptr("ST_out1"),
		SipNumber:            ptr("+14155550199"),
	})
	require.NoError(t, err)
	require.Equal(t, "SR_1", *resp.SimulationRunId)
	assert.Equal(t, "/v0/projects/p_1/simulation-runs", gotPath)
	assert.Equal(t, "support-agent", gotBody["agentName"])
	assert.Equal(t, "canary", gotBody["deployment"])
	assert.Equal(t, float64(5), gotBody["numSimulations"])
	assert.Equal(t, float64(4), gotBody["concurrency"])
	assert.Equal(t, "SIMULATION_MODE_AUDIO", gotBody["mode"])
	assert.Equal(t, true, gotBody["backgroundNoise"])
	assert.Equal(t, true, gotBody["lowQualityMicrophone"])
	assert.Equal(t, true, gotBody["packetLoss"])
	assert.Equal(t, "+14155550100", gotBody["sipCallTo"])
	assert.Equal(t, "ST_out1", gotBody["sipTrunkId"])
	assert.Equal(t, "+14155550199", gotBody["sipNumber"])
	assert.Equal(t, map[string]any{"samples": float64(3), "passRate": 0.75}, gotBody["sampling"])
	assert.Equal(t, map[string]any{"provider": "github_actions"}, gotBody["ci"])
}

func TestListSimulationRunsForwardsParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"SR_1"}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	completed := oapi.LivekitSimulationRunStatusSTATUSCOMPLETED
	audio := oapi.SIMULATIONMODEAUDIO

	runs, next, err := c.ListSimulationRuns(context.Background(), "p_1", &oapi.SimulationServiceListSimulationRunsParams{
		Status:     &completed,
		AgentName:  ptr("support-agent"),
		Mode:       &audio,
		StartTime:  &start,
		EndTime:    &end,
		PageCursor: ptr("c1"),
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "SR_1", *runs[0].Id)
	assert.Equal(t, "c2", next)
	assert.Equal(t, "/v0/projects/p_1/simulation-runs", gotPath)
	assert.Equal(t, "STATUS_COMPLETED", gotQuery.Get("status"))
	assert.Equal(t, "support-agent", gotQuery.Get("agentName"))
	assert.Equal(t, "SIMULATION_MODE_AUDIO", gotQuery.Get("mode"))
	assert.Equal(t, "c1", gotQuery.Get("page.cursor"))
	assert.Equal(t, start.Format(time.RFC3339), gotQuery.Get("startTime"))
	assert.Equal(t, end.Format(time.RFC3339), gotQuery.Get("endTime"))
}
