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
	"errors"
	"testing"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/livekit"
)

// The matrix-rain view is deliberately absent from these fixtures: it seeds
// itself from math/rand and has no frame to pin.

func simulateFixture() *simulateModel {
	m := newSimulateModel(&simulateConfig{
		pc:             &config.ProjectConfig{Name: "demo", URL: "wss://demo.livekit.cloud"},
		numSimulations: 3,
		concurrency:    2,
		mode:           modeGenerateFromSource,
	})
	m.runID = "SR_fixture0001"
	m.width = 100
	m.height = 30
	return m
}

func fixtureSteps() []step {
	return []step{
		{label: "Starting agent", status: "done", elapsed: 1200 * time.Millisecond},
		{label: "Creating simulation", status: "running"},
		{label: "Uploading source", status: "pending"},
	}
}

func fixtureJob(id, label string, status livekit.SimulationRun_Job_Status) *livekit.SimulationRun_Job {
	return &livekit.SimulationRun_Job{
		Id:                id,
		Label:             label,
		Status:            status,
		Instructions:      "Ask the agent about " + label,
		AgentExpectations: "The agent answers about " + label,
	}
}

func fixtureRun(status livekit.SimulationRun_Status) *livekit.SimulationRun {
	return &livekit.SimulationRun{
		Id:     "SR_fixture0001",
		Status: status,
		Jobs: []*livekit.SimulationRun_Job{
			fixtureJob("SRJ_aaaaaaaa", "booking a table", livekit.SimulationRun_Job_STATUS_COMPLETED),
			fixtureJob("SRJ_bbbbbbbb", "changing a reservation", livekit.SimulationRun_Job_STATUS_RUNNING),
			fixtureJob("SRJ_cccccccc", "cancelling outright", livekit.SimulationRun_Job_STATUS_FAILED),
			fixtureJob("SRJ_dddddddd", "asking for the hours", livekit.SimulationRun_Job_STATUS_PENDING),
		},
		NumSimulations: 4,
		Concurrency:    2,
	}
}

// runningFixture is a model mid-run: setup finished, jobs in every status.
func runningFixture() *simulateModel {
	m := simulateFixture()
	m.steps = fixtureSteps()
	for i := range m.steps {
		m.steps[i].status = "done"
		m.steps[i].elapsed = time.Duration(i+1) * 500 * time.Millisecond
	}
	m.setupDone = true
	m.currentStep = len(m.steps) - 1
	m.run = fixtureRun(livekit.SimulationRun_STATUS_RUNNING)
	m.startTime = time.Now().Add(-90 * time.Second)
	m.cursor = 1
	return m
}

func TestVRTSimulateFrames(t *testing.T) {
	tests := []struct {
		name  string
		model func() *simulateModel
	}{
		{"simulate_setup_running", func() *simulateModel {
			m := simulateFixture()
			m.steps = fixtureSteps()
			m.currentStep = 1
			m.stepStart = time.Now().Add(-3 * time.Second)
			return m
		}},
		{"simulate_setup_warnings", func() *simulateModel {
			m := simulateFixture()
			m.config.warnings = []string{"--concurrency is ignored when running against a live agent"}
			m.steps = fixtureSteps()
			m.currentStep = 1
			m.stepStart = time.Now()
			return m
		}},
		{"simulate_setup_error", func() *simulateModel {
			m := simulateFixture()
			m.steps = fixtureSteps()
			m.steps[1].status = "failed"
			m.currentStep = 1
			m.err = errors.New("agent worker exited before registering")
			return m
		}},
		{"simulate_running_list", runningFixture},
		{"simulate_running_description", func() *simulateModel {
			m := runningFixture()
			m.run.AgentDescription = "A restaurant booking agent. It takes reservations, moves them, and cancels them, and it always confirms the party size before writing anything down."
			m.showDescription = true
			return m
		}},
		{"simulate_running_description_collapsed", func() *simulateModel {
			m := runningFixture()
			m.run.AgentDescription = "A restaurant booking agent. It takes reservations, moves them, and cancels them."
			return m
		}},
		{"simulate_confirm_quit", func() *simulateModel {
			m := runningFixture()
			m.confirmQuit = true
			m.confirmQuitSel = 1
			return m
		}},
		{"simulate_quota_modal", func() *simulateModel {
			m := runningFixture()
			m.quotaWarning = &quotaInfo{category: "MaxConcurrentGatewayLLMTpm"}
			m.quotaSuggested = 1
			m.peakRunning = 4
			return m
		}},
		{"simulate_saving_prompt", func() *simulateModel {
			m := runningFixture()
			m.saving = true
			m.saveInput.SetValue("scenarios")
			return m
		}},
		{"simulate_saving_error", func() *simulateModel {
			m := runningFixture()
			m.saving = true
			m.saveInput.SetValue("scenarios")
			m.saveErr = "file already exists"
			return m
		}},
		{"simulate_saving_long_path", func() *simulateModel {
			m := runningFixture()
			m.config.projectDir = "/Users/agent/src/restaurant-booking-agent/simulations"
			m.saving = true
			m.saveInput.SetValue("scenarios.yaml")
			return m
		}},
		{"simulate_toast", func() *simulateModel {
			m := runningFixture()
			m.toast = "Copied job ID"
			m.toastOK = true
			return m
		}},
		{"simulate_completed", func() *simulateModel {
			m := runningFixture()
			m.run = fixtureRun(livekit.SimulationRun_STATUS_COMPLETED)
			for _, j := range m.run.Jobs {
				j.Status = livekit.SimulationRun_Job_STATUS_COMPLETED
			}
			m.runFinished = true
			m.endTime = m.startTime.Add(2 * time.Minute)
			m.summary = &livekit.SimulationRunSummary{
				Passed:    4,
				Failed:    0,
				GoingWell: "The agent confirmed the party size on every booking.",
				ToImprove: "It repeated the address more often than callers needed.",
			}
			return m
		}},
		{"simulate_failed_no_jobs", func() *simulateModel {
			m := runningFixture()
			m.run = &livekit.SimulationRun{
				Id:     "SR_fixture0001",
				Status: livekit.SimulationRun_STATUS_FAILED,
				Error:  "scenario generation failed: model returned no scenarios",
			}
			return m
		}},
		{"simulate_detail_live", func() *simulateModel {
			m := runningFixture()
			m.detailJobID = "SRJ_aaaaaaaa"
			m.detailWidth = m.width
			return m
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireFrame(t, tc.name, renderSimulateFrame(tc.model()))
		})
	}
}
