// Copyright 2025 LiveKit, Inc.
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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/livekit"
)

// summaryTestModel builds a finished 10-job run with a long summary, the shape
// that used to push the header and top of the job list off-screen on short
// terminals (a 31x156 terminal showed only jobs 7-10).
func summaryTestModel(width, height int) *simulateModel {
	m := newSimulateModel(&simulateConfig{})
	m.width = width
	m.height = height
	m.setupDone = true

	jobs := make([]*livekit.SimulationRun_Job, 10)
	for i := range jobs {
		jobs[i] = &livekit.SimulationRun_Job{
			Id:     fmt.Sprintf("SRJ_%02d", i+1),
			Label:  fmt.Sprintf("Scenario %d", i+1),
			Status: livekit.SimulationRun_Job_STATUS_COMPLETED,
		}
	}
	longText := strings.Repeat("The agent handled the flows well and confirmed values before completing. ", 6)
	m.run = &livekit.SimulationRun{
		Id:     "SR_test",
		Status: livekit.SimulationRun_STATUS_COMPLETED,
		Jobs:   jobs,
		Summary: &livekit.SimulationRunSummary{
			Passed:    8,
			Failed:    2,
			GoingWell: longText,
			ToImprove: longText,
			Issues: []*livekit.SimulationRunSummary_Issue{
				{Description: longText, Suggestion: longText},
				{Description: longText, Suggestion: longText},
			},
		},
	}
	m.runFinished = true
	return m
}

// The list view must fit the terminal height so the job list stays visible; a
// long summary is clamped rather than pushing the top of the view off-screen.
func TestSimulateViewFitsShortTerminal(t *testing.T) {
	m := summaryTestModel(156, 31)
	view := m.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	require.LessOrEqual(t, len(lines), 31, "list view must fit a 31-row terminal")
	require.Contains(t, view, "Scenario 1", "first job must remain visible")
	require.Contains(t, view, "Scenario 10", "last job must remain visible")
	require.Contains(t, view, "more summary lines", "clamped summary must advertise expansion")
}

// Expanding the summary shows it windowed and scrollable in place of the list.
func TestSimulateSummaryExpandScroll(t *testing.T) {
	m := summaryTestModel(156, 31)
	m.summaryExpanded = true
	view := m.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	require.LessOrEqual(t, len(lines), 31, "expanded summary view must fit the terminal")
	require.Contains(t, view, "more lines below")

	m.summaryScrollOff = 1000 // clamped to max scroll on render
	view = m.View()
	require.Contains(t, view, "more lines above")
	require.NotContains(t, view, "Scenario 1", "job list is replaced while the summary is expanded")
}

// A short summary renders in full, unclamped.
func TestSimulateShortSummaryUnclamped(t *testing.T) {
	m := summaryTestModel(156, 40)
	m.run.Summary.GoingWell = "All good."
	m.run.Summary.ToImprove = ""
	m.run.Summary.Issues = nil
	view := m.View()
	require.NotContains(t, view, "more summary lines")
	require.Contains(t, view, "All good.")
	require.NotContains(t, view, "S summary", "hint must not advertise expansion when nothing is clamped")
}

// The hint bar advertises S only when the summary was actually clamped.
func TestSimulateSummaryHintOnlyWhenTruncated(t *testing.T) {
	m := summaryTestModel(156, 31)
	view := m.View()
	require.Contains(t, view, "S summary")
}
