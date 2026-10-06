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
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"
)

func loadScenariosYAML(t *testing.T, doc string) *livekit.ScenarioGroup {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	group, err := loadScenarioGroup(path)
	require.NoError(t, err)
	return group
}

func TestLoadScenarioGroup_Language(t *testing.T) {
	group := loadScenariosYAML(t, `
name: languages
language:
  speak: en
  listen: hi
scenarios:
  - label: inherits the file default
    instructions: a
    agent_expectations: b
  - label: bare tag sets both
    instructions: a
    agent_expectations: b
    language: hi
  - label: a mapping overrides key by key
    instructions: a
    agent_expectations: b
    language:
      listen: en
`)
	want := []*livekit.Scenario_Language{
		{Speak: "en", Listen: "hi"},
		{Speak: "hi", Listen: "hi"},
		{Speak: "en", Listen: "en"},
	}
	for i, s := range group.GetScenarios() {
		require.Empty(t, cmp.Diff(want[i], s.GetLanguage(), protocmp.Transform()), s.GetLabel())
	}
}

func TestLoadScenarioGroup_NoLanguageLeavesItUnset(t *testing.T) {
	group := loadScenariosYAML(t, `
name: none
scenarios:
  - label: s
    instructions: a
    agent_expectations: b
`)
	require.Nil(t, group.GetScenarios()[0].GetLanguage())
}

func TestScenarioGroupToYAML_LanguageRoundTrips(t *testing.T) {
	group := &livekit.ScenarioGroup{Name: "rt", Scenarios: []*livekit.Scenario{
		{Label: "both", Instructions: "a", AgentExpectations: "b", Language: &livekit.Scenario_Language{Speak: "hi", Listen: "hi"}},
		{Label: "split", Instructions: "a", AgentExpectations: "b", Language: &livekit.Scenario_Language{Speak: "en", Listen: "hi"}},
		{Label: "none", Instructions: "a", AgentExpectations: "b"},
	}}
	out, err := scenarioGroupToYAML(group)
	require.NoError(t, err)
	require.Contains(t, string(out), "language: hi\n")
	require.NotContains(t, string(out), "listen: hi\n    speak")

	back := loadScenariosYAML(t, string(out))
	require.Empty(t, cmp.Diff(group, back, protocmp.Transform()))
}
