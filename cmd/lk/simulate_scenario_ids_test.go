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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
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

func TestInsertScenarioIDsLeavesEverythingElseInPlace(t *testing.T) {
	in := `# Scenarios for the drive-thru agent.
name: drive-thru  # suite name
scenarios:
  # The happy path.
  - label: order a burger # inline
    instructions: >
      order a burger
      and fries


  - # comment on the dash line
    label: "refund"
    instructions: ask for a refund
  - {label: flow, instructions: y}
  - id:
    label: empty
  - label: quoted
    id: ""  # keep this comment
  - label: tilde
    id: ~
`
	want := `# Scenarios for the drive-thru agent.
id: SCNG_x
name: drive-thru  # suite name
scenarios:
  # The happy path.
  - id: SCN_x
    label: order a burger # inline
    instructions: >
      order a burger
      and fries


  - # comment on the dash line
    id: SCN_x
    label: "refund"
    instructions: ask for a refund
  - {id: SCN_x, label: flow, instructions: y}
  - id: SCN_x
    label: empty
  - label: quoted
    id: SCN_x  # keep this comment
  - label: tilde
    id: SCN_x
`
	out, added, err := insertScenarioIDs([]byte(in))
	require.NoError(t, err)
	require.Equal(t, 7, added)
	ids := regexp.MustCompile(`(SCNG?)_[0-9A-Za-z]+`)
	require.Equal(t, want, ids.ReplaceAllString(string(out), "${1}_x"))

	crlf := strings.ReplaceAll(in, "\n", "\r\n")
	out, _, err = insertScenarioIDs([]byte(crlf))
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(want, "\n", "\r\n"), ids.ReplaceAllString(string(out), "${1}_x"))
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

func TestLoadScenarioGroupSimulatorFlow(t *testing.T) {
	const steps = `
steps:
  menu:
    say: Press 1 for sales.
    speaker: MACHINE
    routes:
      - match: "1"
        to: sales
    timeout:
      after: 8s
      to: livekit/hangup
  sales:
    instructions: You are a sales rep.
    voicemail:
      beep: false
`
	cases := []struct {
		name    string
		yaml    string
		want    *livekit.ScenarioGroup
		wantErr []string
	}{
		{
			name: "steps and start",
			yaml: `
id: g1
name: n` + steps + `
scenarios:
  - id: s1
    label: call
    start: menu
    max_duration: 60s
`,
			want: &livekit.ScenarioGroup{
				Id:   "g1",
				Name: "n",
				Steps: map[string]*livekit.SimulatorStep{
					"menu": {
						Say:     "Press 1 for sales.",
						Speaker: livekit.SimulatorStep_MACHINE,
						Routes:  []*livekit.SimulatorStep_Route{{Match: "1", To: "sales"}},
						Timeout: &livekit.SimulatorStep_Timeout{After: durationpb.New(8 * time.Second), To: "livekit/hangup"},
					},
					"sales": {
						Instructions: "You are a sales rep.",
						Voicemail:    &livekit.SimulatorStep_Voicemail{Beep: proto.Bool(false)},
					},
				},
				Scenarios: []*livekit.Scenario{{
					Id:          "s1",
					Label:       "call",
					Start:       "menu",
					MaxDuration: durationpb.New(60 * time.Second),
				}},
			},
		},
		{
			name: "built-in start",
			yaml: `
id: g1
name: n
scenarios:
  - id: s1
    label: call
    start: livekit/unavailable
`,
			want: &livekit.ScenarioGroup{
				Id:        "g1",
				Name:      "n",
				Scenarios: []*livekit.Scenario{{Id: "s1", Label: "call", Start: "livekit/unavailable"}},
			},
		},
		{
			name: "instructions only",
			yaml: `
id: g1
name: n
scenarios:
  - id: s1
    label: order
    instructions: order a burger
    agent_expectations: confirms the order
`,
			want: &livekit.ScenarioGroup{
				Id:   "g1",
				Name: "n",
				Scenarios: []*livekit.Scenario{{
					Id:                "s1",
					Label:             "order",
					Instructions:      "order a burger",
					AgentExpectations: "confirms the order",
				}},
			},
		},
		{
			name: "unknown step key",
			yaml: `
id: g1
name: n
steps:
  menu:
    sya: typo
scenarios:
  - id: s1
    label: call
    start: menu
`,
			wantErr: []string{`step "menu"`, "sya"},
		},
		{
			name: "start and instructions",
			yaml: `
id: g1
name: n` + steps + `
scenarios:
  - id: s1
    label: call
    start: menu
    instructions: hi
`,
			wantErr: []string{`scenario "call"`, "exactly one of `start` and `instructions`"},
		},
		{
			name: "unknown start",
			yaml: `
id: g1
name: n` + steps + `
scenarios:
  - id: s1
    label: call
    start: nowhere
`,
			wantErr: []string{`scenario "call"`, `start "nowhere"`},
		},
		{
			name: "max_duration without unit",
			yaml: `
id: g1
name: n` + steps + `
scenarios:
  - id: s1
    label: call
    start: menu
    max_duration: 60
`,
			wantErr: []string{`scenario "call"`, "max_duration"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			group, err := loadScenarioGroup(writeScenariosFile(t, c.yaml))
			if c.wantErr != nil {
				for _, e := range c.wantErr {
					require.ErrorContains(t, err, e)
				}
				return
			}
			require.NoError(t, err)
			require.True(t, proto.Equal(c.want, group), "got %v", group)
		})
	}
}

func TestScenarioGroupToYAMLRoundTripsFlow(t *testing.T) {
	path := writeScenariosFile(t, `
id: g1
name: n
steps:
  menu:
    say: Press 1.
    speaker: MACHINE
    max_visits:
      count: 2
      to: livekit/hangup
    voicemail:
      beep: false
      silence_timeout: 1.5s
scenarios:
  - id: s1
    label: call
    start: menu
    max_duration: 60s
`)
	group, err := loadScenarioGroup(path)
	require.NoError(t, err)
	out, err := scenarioGroupToYAML(group)
	require.NoError(t, err)
	again, err := loadScenarioGroup(writeScenariosFile(t, string(out)))
	require.NoError(t, err)
	require.True(t, proto.Equal(group, again), "got %v\nyaml:\n%s", again, out)
}

func TestInsertScenarioIDsKeepsSteps(t *testing.T) {
	in := `name: n
steps:
  menu:
    say: hi # greet
scenarios:
  - label: call
    start: menu
`
	out, added, err := insertScenarioIDs([]byte(in))
	require.NoError(t, err)
	require.Equal(t, 2, added)
	ids := regexp.MustCompile(`(SCNG?)_\w+`)
	want := `id: SCNG_x
name: n
steps:
  menu:
    say: hi # greet
scenarios:
  - id: SCN_x
    label: call
    start: menu
`
	require.Equal(t, want, ids.ReplaceAllString(string(out), "${1}_x"))
}
