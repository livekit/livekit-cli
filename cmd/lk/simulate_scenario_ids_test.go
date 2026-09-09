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
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func writeScenariosFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestInsertScenarioIDsPreservesFileAndAddsMissing(t *testing.T) {
	in := `# suite for the drive-thru agent
name: drive-thru
scenarios:
  - label: order a burger # happy path
    instructions: order a burger
    agent_expectations: confirms the order
  - id: keep-me
    label: already has id
    instructions: say hi
`
	out, added, err := insertScenarioIDs([]byte(in))
	require.NoError(t, err)
	require.Equal(t, 2, added) // group id + first scenario

	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(out, &doc))
	root := doc.Content[0]
	require.Equal(t, "id", root.Content[0].Value, "group id is inserted as the first key")
	require.True(t, strings.HasPrefix(root.Content[1].Value, "SCNG_"))
	require.Equal(t, "name", root.Content[2].Value)

	scenarios := root.Content[5]
	require.Equal(t, "id", scenarios.Content[0].Content[0].Value, "scenario id is inserted as the first key")
	require.True(t, strings.HasPrefix(scenarios.Content[0].Content[1].Value, "SCN_"))
	require.Equal(t, "keep-me", scenarios.Content[1].Content[1].Value, "existing ids are untouched")

	s := string(out)
	require.Contains(t, s, "# suite for the drive-thru agent")
	require.Contains(t, s, "  - id: ", "two-space indentation is kept")
	require.Contains(t, s, "label: order a burger # happy path")

	// a fully identified file is left alone
	out2, added2, err := insertScenarioIDs(out)
	require.NoError(t, err)
	require.Equal(t, 0, added2)
	require.Nil(t, out2)
}

func TestLoadScenarioGroupRequiresUniqueIDs(t *testing.T) {
	_, err := loadScenarioGroup(writeScenariosFile(t, `
id: g1
name: n
scenarios:
  - label: first
    instructions: a
  - id: s2
    label: second
    instructions: b
`))
	require.ErrorContains(t, err, `"first"`)
	require.ErrorContains(t, err, "id")

	_, err = loadScenarioGroup(writeScenariosFile(t, `
id: g1
name: n
scenarios:
  - id: dup
    label: first
    instructions: a
  - id: dup
    label: second
    instructions: b
`))
	require.ErrorContains(t, err, `duplicate scenario id "dup"`)

	_, err = loadScenarioGroup(writeScenariosFile(t, `
name: n
scenarios:
  - id: s1
    label: first
    instructions: a
`))
	require.ErrorContains(t, err, "scenarios file has no id")

	group, err := loadScenarioGroup(writeScenariosFile(t, `
id: g1
name: n
scenarios:
  - id: s1
    label: first
    instructions: a
`))
	require.NoError(t, err)
	require.Equal(t, "g1", group.GetId())
	require.Equal(t, "s1", group.GetScenarios()[0].GetId())
}

func TestScenarioGroupToYAMLFillsIDs(t *testing.T) {
	group := &livekit.ScenarioGroup{
		Name: "generated",
		Scenarios: []*livekit.Scenario{
			{Label: "a", Instructions: "x"},
			{Id: "fixed", Label: "b", Instructions: "y"},
		},
	}
	out, err := scenarioGroupToYAML(group)
	require.NoError(t, err)

	path := writeScenariosFile(t, string(out))
	loaded, err := loadScenarioGroup(path)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(loaded.GetId(), "SCNG_"))
	require.True(t, strings.HasPrefix(loaded.GetScenarios()[0].GetId(), "SCN_"))
	require.Equal(t, "fixed", loaded.GetScenarios()[1].GetId())
}
