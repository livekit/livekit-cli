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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

func quotaTestModel(t *testing.T) *simulateModel {
	t.Helper()
	m := newSimulateModel(&simulateConfig{concurrency: 0})
	m.setupDone = true
	m.agent = &AgentProcess{
		roomLogs:       map[string][]string{},
		latestRoomByPx: map[string]string{},
	}
	return m
}

func runningRun(nRunning int) *livekit.SimulationRun {
	run := &livekit.SimulationRun{Status: livekit.SimulationRun_STATUS_RUNNING}
	for i := 0; i < nRunning; i++ {
		run.Jobs = append(run.Jobs, &livekit.SimulationRun_Job{
			Status: livekit.SimulationRun_Job_STATUS_RUNNING,
		})
	}
	return run
}

func TestQuotaDialog_ShowsOnceAndDismisses(t *testing.T) {
	m := quotaTestModel(t)

	// Poll 1: 10 jobs running, no quota errors yet — no dialog.
	m.Update(simulationRunMsg{run: runningRun(10)})
	require.Nil(t, m.quotaWarning)
	require.Equal(t, 10, m.peakRunning)

	// The agent starts logging 429s; the next poll raises the dialog.
	m.agent.appendLog(quotaLineTpm)
	m.Update(simulationRunMsg{run: runningRun(10)})
	require.NotNil(t, m.quotaWarning)
	require.True(t, m.quotaModalActive())

	// The dialog replaces the hint bar, names the quota, suggests half the
	// observed peak (10/2=5), and carries the dismiss button.
	hint := m.renderHint()
	require.Contains(t, hint, "Inference quota exceeded")
	require.Contains(t, hint, "tokens-per-minute")
	require.Contains(t, hint, "--concurrency 5")
	require.Contains(t, hint, "Dismiss")

	// Enter dismisses; the dialog never comes back this run.
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.False(t, m.quotaModalActive())
	require.True(t, m.quotaDismissed)
	m.Update(simulationRunMsg{run: runningRun(10)})
	require.False(t, m.quotaModalActive())
	require.NotContains(t, m.renderHint(), "Inference quota exceeded")
}

func TestQuotaDialog_ExplicitConcurrencyHalved(t *testing.T) {
	m := quotaTestModel(t)
	m.config.concurrency = 7
	m.agent.appendLog(quotaLineRpm)
	m.Update(simulationRunMsg{run: runningRun(7)})
	require.True(t, m.quotaModalActive())
	require.Contains(t, m.renderHint(), "--concurrency 3") // 7/2 floors to 3
	require.Contains(t, m.renderHint(), "requests-per-minute")
}

func TestQuotaDialog_EscDismissesAndKeysAreCaptured(t *testing.T) {
	m := quotaTestModel(t)
	m.agent.appendLog(quotaLineTpm)
	m.Update(simulationRunMsg{run: runningRun(2)})
	require.True(t, m.quotaModalActive())

	// Keys other than dismiss/quit are swallowed while the dialog is up.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	require.Equal(t, 0, m.cursor)
	require.True(t, m.quotaModalActive())

	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	require.False(t, m.quotaModalActive())
}
