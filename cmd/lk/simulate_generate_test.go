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
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

func TestSaveScenarioGroup(t *testing.T) {
	out = util.NewPrinter(io.Discard, io.Discard, true)
	path := filepath.Join(t.TempDir(), "scenarios.yaml")

	first := &livekit.ScenarioGroup{Name: "frontdesk", Scenarios: []*livekit.Scenario{
		{Label: "book a table", Instructions: "Ask for a table", AgentExpectations: "Confirms the party size"},
	}}
	require.NoError(t, saveScenarioGroup(path, first))

	got, err := loadScenarioGroup(path)
	require.NoError(t, err)
	require.Equal(t, "frontdesk", got.Name)
	require.Len(t, got.Scenarios, 1)

	// a second save appends and keeps the file's own name
	second := &livekit.ScenarioGroup{Name: "other", Scenarios: []*livekit.Scenario{
		{Label: "cancel", Instructions: "Cancel the booking", AgentExpectations: "Confirms cancellation"},
	}}
	require.NoError(t, saveScenarioGroup(path, second))

	got, err = loadScenarioGroup(path)
	require.NoError(t, err)
	require.Equal(t, "frontdesk", got.Name)
	require.Len(t, got.Scenarios, 2)
	require.Equal(t, "cancel", got.Scenarios[1].Label)
}

func TestSaveScenarioGroupRejectsUnparseableFile(t *testing.T) {
	out = util.NewPrinter(io.Discard, io.Discard, true)
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	require.NoError(t, os.WriteFile(path, []byte("scenarios: [\n"), 0o644))

	err := saveScenarioGroup(path, &livekit.ScenarioGroup{Scenarios: []*livekit.Scenario{{Label: "x"}}})
	require.Error(t, err)

	// the broken file is left untouched
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "scenarios: [\n", string(data))
}
