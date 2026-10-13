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
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFiles writes each name -> content under one temp dir and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return dir
}

const includingScenarios = `
id: SCNG_dental
include: [nodes/menus.yaml, nodes/people.yaml]
nodes:
  greeting: { say: "Hi, you've reached Sam.", then: livekit/voicemail }
scenarios:
  - id: SCN_a
    label: menu-to-sam
    start: menu
    agent_expectations: Presses 1, then confirms with Sam.
`

func TestIncludedNodesJoinTheGroup(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"dental.yaml": includingScenarios,
		// nested, with a path relative to the including file
		"nodes/menus.yaml": `
include: [common.yaml]
nodes:
  menu:
    say: "For Sam, press 1."
    routes: [{ match: "1", to: sam }]
    timeout: { after: 8s, to: goodbye }
`,
		// reached twice: from dental.yaml through people.yaml and menus.yaml
		"nodes/people.yaml": `
include: [common.yaml]
nodes:
  sam: { instructions: You are Sam. }
`,
		"nodes/common.yaml": `
nodes:
  goodbye: { say: Goodbye., then: livekit/hangup }
`,
	})

	group, err := loadScenarioGroup(filepath.Join(dir, "dental.yaml"))
	require.NoError(t, err)

	names := make([]string, 0, len(group.Nodes))
	for name := range group.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)
	require.Equal(t, []string{"goodbye", "greeting", "menu", "sam"}, names)
	require.Equal(t, "sam", group.Nodes["menu"].Routes[0].To)
	require.Equal(t, "menu", group.Scenarios[0].Start)
}

func TestIncludeErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "a cycle prints the include chain",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "include: [common.yaml]\nnodes: { menu: { say: hi, then: livekit/hangup } }\n",
				"nodes/common.yaml": "include: [menus.yaml]\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: "include cycle: dental.yaml -> nodes/menus.yaml -> nodes/common.yaml -> nodes/menus.yaml",
		},
		{
			name: "a node defined in two files names both",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "nodes: { menu: { say: hi, then: livekit/hangup } }\n",
				"nodes/people.yaml": "nodes: { menu: { say: hello, then: livekit/hangup } }\n",
			},
			want: `node "menu" is defined in both nodes/menus.yaml and nodes/people.yaml`,
		},
		{
			name: "an included file cannot set scenarios",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "nodes: { menu: { say: hi, then: livekit/hangup } }\nscenarios: []\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: "nodes/menus.yaml: an included file holds only `nodes` and `include`, not \"scenarios\"",
		},
		{
			name: "a missing include path",
			files: map[string]string{
				"dental.yaml":      includingScenarios,
				"nodes/menus.yaml": "nodes: { menu: { say: hi, then: livekit/hangup } }\n",
			},
			want: `dental.yaml: include "nodes/people.yaml"`,
		},
		{
			name: "a bad included node names its file",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "nodes: { menu: { sya: hi } }\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: `node "menu" (menus.yaml)`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeFiles(t, c.files)
			_, err := loadScenarioGroup(filepath.Join(dir, "dental.yaml"))
			require.ErrorContains(t, err, c.want)
		})
	}
}
