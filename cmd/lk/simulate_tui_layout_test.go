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

// pageScrollModel builds a finished 10-job run with a long summary — the shape
// that used to push the header and top of the job list off-screen on short
// terminals (a 31x156 terminal showed only jobs 7-10).
func pageScrollModel(width, height int) *simulateModel {
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

func viewLines(m *simulateModel) []string {
	return strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
}

// The page must fit the terminal height, anchored to the top by default: the
// header and the first jobs stay visible and the overflow is advertised.
func TestSimulatePageFitsShortTerminal(t *testing.T) {
	m := pageScrollModel(156, 31)
	view := m.View()
	require.LessOrEqual(t, len(viewLines(m)), 31, "view must fit a 31-row terminal")
	require.Contains(t, view, "Agent Simulation", "header must stay visible")
	require.Contains(t, view, "Scenario 1", "top of the job list must stay visible")
	require.Contains(t, view, "more lines", "overflow must be advertised")
	require.Contains(t, view, "PgUp/PgDn scroll", "hint must advertise page scrolling")
}

// Scrolling down reveals the bottom of the summary; the offset clamps at the
// end and the hint bar stays pinned.
func TestSimulatePageScrollsToBottom(t *testing.T) {
	m := pageScrollModel(156, 31)
	m.View() // establish pageOverflow
	m.viewScrollOff = 1 << 20
	view := m.View()
	require.LessOrEqual(t, len(viewLines(m)), 31)
	require.Contains(t, view, "↑", "scrolled view must show the above-marker")
	require.Contains(t, view, "Suggestion:", "bottom of the summary must be reachable")
	require.Contains(t, view, "q quit", "hint bar stays pinned below the page")
	require.NotContains(t, view, "Agent Simulation", "header scrolls off at the bottom")
}

// Moving the cursor to the last job scrolls the page to keep it visible.
func TestSimulatePageFollowsCursor(t *testing.T) {
	m := pageScrollModel(156, 12) // short enough that the list itself overflows
	m.View()
	for range 9 {
		m.moveCursor(1)
	}
	view := m.View()
	require.LessOrEqual(t, len(viewLines(m)), 12)
	require.Contains(t, view, "Scenario 10", "cursor row must be scrolled into view")
}

// Content that fits renders in full with no markers and no scroll hint.
func TestSimulatePageNoScrollWhenFits(t *testing.T) {
	m := pageScrollModel(156, 60)
	view := m.View()
	require.NotContains(t, view, "more lines")
	require.NotContains(t, view, "PgUp/PgDn scroll ·")
	require.Contains(t, view, "Scenario 1")
	require.Contains(t, view, "Scenario 10")
	require.Contains(t, view, "Suggestion:")
}
