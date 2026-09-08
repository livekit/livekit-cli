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
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// keyPress builds the key event Bubble Tea would deliver for a key named the way
// the model's switch statements name it. Key names belong to the terminal
// library, so this mapping is the contract those switches are written against.
func keyPress(name string) tea.KeyMsg {
	named := map[string]rune{
		"enter":     tea.KeyEnter,
		"esc":       tea.KeyEscape,
		"space":     tea.KeySpace,
		"tab":       tea.KeyTab,
		"backspace": tea.KeyBackspace,
		"up":        tea.KeyUp,
		"down":      tea.KeyDown,
		"left":      tea.KeyLeft,
		"right":     tea.KeyRight,
		"pgup":      tea.KeyPgUp,
		"pgdown":    tea.KeyPgDown,
	}
	if code, ok := named[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	if mod, rest, ok := strings.Cut(name, "+"); ok && mod == "ctrl" {
		if r := []rune(rest); len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Mod: tea.ModCtrl}
		}
	}
	if r := []rune(name); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: name}
	}
	panic("keyPress: unmapped key name " + name)
}

// TestSimulateKeyDispatch pins the key bindings to the model state they change.
// Keys are matched by name, and those names are the terminal library's to
// choose — Bubble Tea v2 renamed space from " " to "space", which silently
// unbound the quota dialog until this was noticed.
func TestSimulateKeyDispatch(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		setup func() *simulateModel
		check func(t *testing.T, m *simulateModel)
	}{
		{
			name:  "space dismisses the quota dialog",
			key:   "space",
			setup: func() *simulateModel { m := runningFixture(); m.quotaWarning = &quotaInfo{}; return m },
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.quotaDismissed) },
		},
		{
			name:  "enter dismisses the quota dialog",
			key:   "enter",
			setup: func() *simulateModel { m := runningFixture(); m.quotaWarning = &quotaInfo{}; return m },
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.quotaDismissed) },
		},
		{
			name:  "esc leaves the quit confirmation",
			key:   "esc",
			setup: func() *simulateModel { m := runningFixture(); m.confirmQuit = true; return m },
			check: func(t *testing.T, m *simulateModel) { require.False(t, m.confirmQuit) },
		},
		{
			name:  "tab moves the quit confirmation selection",
			key:   "tab",
			setup: func() *simulateModel { m := runningFixture(); m.confirmQuit = true; return m },
			check: func(t *testing.T, m *simulateModel) { require.Equal(t, 1, m.confirmQuitSel) },
		},
		{
			name:  "down moves the cursor",
			key:   "down",
			setup: func() *simulateModel { m := runningFixture(); m.cursor = 0; return m },
			check: func(t *testing.T, m *simulateModel) { require.Equal(t, 1, m.cursor) },
		},
		{
			name:  "up moves the cursor back",
			key:   "up",
			setup: func() *simulateModel { m := runningFixture(); m.cursor = 2; return m },
			check: func(t *testing.T, m *simulateModel) { require.Equal(t, 1, m.cursor) },
		},
		{
			name:  "d expands the agent description",
			key:   "d",
			setup: func() *simulateModel { m := runningFixture(); m.run.AgentDescription = "an agent"; return m },
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.showDescription) },
		},
		{
			name: "t toggles tool detail inside a job",
			key:  "t",
			setup: func() *simulateModel {
				m := runningFixture()
				m.detailJobID = "SRJ_aaaaaaaa"
				return m
			},
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.showToolDetail) },
		},
		{
			name:  "t is inert in the list view",
			key:   "t",
			setup: runningFixture,
			check: func(t *testing.T, m *simulateModel) { require.False(t, m.showToolDetail) },
		},
		{
			name:  "ctrl+l toggles the log pane",
			key:   "ctrl+l",
			setup: runningFixture,
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.showLogs) },
		},
		{
			name:  "enter opens the job detail",
			key:   "enter",
			setup: runningFixture,
			check: func(t *testing.T, m *simulateModel) { require.Equal(t, "SRJ_bbbbbbbb", m.detailJobID) },
		},
		{
			name: "esc closes the job detail",
			key:  "esc",
			setup: func() *simulateModel {
				m := runningFixture()
				m.detailJobID = "SRJ_aaaaaaaa"
				return m
			},
			check: func(t *testing.T, m *simulateModel) { require.Empty(t, m.detailJobID) },
		},
		{
			name:  "q asks before quitting a live run",
			key:   "q",
			setup: runningFixture,
			check: func(t *testing.T, m *simulateModel) { require.True(t, m.confirmQuit) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup()
			m.handleKey(keyPress(tc.key))
			tc.check(t, m)
		})
	}
}

// TestSimulateDetailLeavesAltScreen covers the one piece of screen handling that
// moved into model state in Bubble Tea v2: the job detail prints into the
// terminal's own scrollback, which it can only reach with the alt screen off.
func TestSimulateDetailLeavesAltScreen(t *testing.T) {
	m := runningFixture()
	require.True(t, m.View().AltScreen, "the list view runs on the alt screen")

	m.detailJobID = "SRJ_aaaaaaaa"
	m.openDetailCmd()
	require.False(t, m.View().AltScreen, "opening a job must leave the alt screen before it prints")

	m.closeDetailCmd()
	require.True(t, m.View().AltScreen, "closing a job returns to the alt screen")
}
