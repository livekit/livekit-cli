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
	"github.com/urfave/cli/v3"
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
		// includes common.yaml too, so dental.yaml reaches it twice
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
			want: "nodes/menus.yaml: an included file holds only `nodes` and `include`, and keys that start with \"x-\" for anchors, not \"scenarios\"",
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
			name: "a cycle back to the root file",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "include: [../dental.yaml]\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: "include cycle: dental.yaml -> nodes/menus.yaml -> dental.yaml",
		},
		{
			name: "a root node defined again in an included file",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "nodes: { greeting: { say: hi, then: livekit/hangup } }\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: `node "greeting" is defined in both dental.yaml and nodes/menus.yaml`,
		},
		{
			name: "a misspelled include key in the root file",
			files: map[string]string{
				"dental.yaml": "id: g\nincludes: [nodes.yaml]\nscenarios: [{ id: s, label: l, start: menu }]\n",
			},
			want: "a scenarios file holds only `id`, `name`, `include`, `nodes`, and `scenarios`, and keys that start with \"x-\" for anchors, not \"includes\"",
		},
		{
			name: "an empty include entry",
			files: map[string]string{
				"dental.yaml": "id: g\ninclude: [\x27\x27]\nscenarios: [{ id: s, label: l, start: livekit/hangup }]\n",
			},
			want: "dental.yaml: include has an empty entry",
		},
		{
			name: "a directory include",
			files: map[string]string{
				"dental.yaml":  "id: g\ninclude: [nodes]\nscenarios: [{ id: s, label: l, start: livekit/hangup }]\n",
				"nodes/x.yaml": "nodes: {}\n",
			},
			want: `dental.yaml: include "nodes" is a directory, not a file`,
		},
		{
			name: "two included files with the same name",
			files: map[string]string{
				"dental.yaml":  "id: g\ninclude: [a/nodes.yaml, b/nodes.yaml]\nscenarios: [{ id: s, label: l, start: x }]\n",
				"a/nodes.yaml": "nodes: { x: { say: hi, then: livekit/hangup } }\n",
				"b/nodes.yaml": "nodes: { y: { sya: hi } }\n",
			},
			want: `b/nodes.yaml: node "y": line 1: unknown field "sya"`,
		},
		{
			name: "a bad included node names its file",
			files: map[string]string{
				"dental.yaml":       includingScenarios,
				"nodes/menus.yaml":  "nodes: { menu: { sya: hi } }\n",
				"nodes/people.yaml": "nodes: { sam: { instructions: x } }\n",
			},
			want: `nodes/menus.yaml: node "menu": line 1: unknown field "sya"`,
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

const sharedNodes = `
nodes:
  menu: { say: hi, routes: [{ match: "1", to: sam }] }
  sam: { instructions: You are Sam. }
`

func TestIncludeMixedPathFormsLoadOnce(t *testing.T) {
	dir := writeFiles(t, map[string]string{"nodes/common.yaml": sharedNodes})
	abs := filepath.Join(dir, "nodes", "common.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dental.yaml"), []byte(`
id: g
include: [nodes/common.yaml, nodes/other.yaml]
scenarios: [{ id: s, label: l, start: menu }]
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nodes", "other.yaml"), []byte("include: ["+abs+"]\n"), 0o644))

	t.Chdir(dir)
	group, err := loadScenarioGroup("dental.yaml")
	require.NoError(t, err)
	require.Len(t, group.Nodes, 2)
}

func TestIncludeResolvesBesideSymlinks(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"shared/dental.yaml": "id: g\ninclude: [nodes.yaml]\nscenarios: [{ id: s, label: l, start: menu }]\n",
		"shared/menus.yaml":  "include: [people.yaml]\nnodes: { menu: { say: hi, routes: [{ match: \"1\", to: sam }] } }\n",
		// the files the symlinks' neighbors name; none sit beside the targets
		"app/nodes.yaml":        "include: [links/menus.yaml]\n",
		"app/links/people.yaml": "nodes: { sam: { instructions: You are Sam. } }\n",
	})
	require.NoError(t, os.Symlink(filepath.Join(dir, "shared", "dental.yaml"), filepath.Join(dir, "app", "dental.yaml")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "shared", "menus.yaml"), filepath.Join(dir, "app", "links", "menus.yaml")))

	group, err := loadScenarioGroup(filepath.Join(dir, "app", "dental.yaml"))
	require.NoError(t, err)
	require.Len(t, group.Nodes, 2)
}

func TestScenariosFileKeysAreFreeWithoutFlows(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"s.yaml": "id: g\ndescription: kept for people\nscenarios: [{ id: s, label: l, instructions: hi }]\n",
	})
	_, err := loadScenarioGroup(filepath.Join(dir, "s.yaml"))
	require.NoError(t, err)
}

func TestIncludeResolvesDotDotPhysically(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"real/proj/dental.yaml": "id: g\ninclude: [nodes.yaml, deeper/../shared.yaml]\nscenarios: [{ id: s, label: l, start: menu }]\n",
		"real/proj/nodes.yaml":  "nodes: { menu: { say: real, then: livekit/hangup } }\n",
		"real/proj/sub/x":       "",
		"real/shared.yaml":      "nodes: { bye: { say: shared, then: livekit/hangup } }\n",
		"real/other/x":          "",
		// decoys where a lexical clean of the paths would look
		"cwd/dental.yaml": "id: g\ninclude: [nodes.yaml]\nscenarios: [{ id: s, label: l, start: menu }]\n",
		"cwd/nodes.yaml":  "nodes: { menu: { say: decoy, then: livekit/hangup } }\n",
	})
	require.NoError(t, os.Symlink(filepath.Join(dir, "real", "proj", "sub"), filepath.Join(dir, "cwd", "link")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "real", "other"), filepath.Join(dir, "real", "proj", "deeper")))

	t.Chdir(filepath.Join(dir, "cwd"))
	group, err := loadScenarioGroup("link/../dental.yaml") // not filepath.Join, which cleans the ..
	require.NoError(t, err)
	require.Equal(t, "real", group.Nodes["menu"].Say)
	require.Equal(t, "shared", group.Nodes["bye"].Say)
}

func TestIncludeOneFileUnderTwoCases(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"dental.yaml": "id: g\ninclude: [nodes.yaml, NODES.yaml]\nscenarios: [{ id: s, label: l, start: menu }]\n",
		"nodes.yaml":  "nodes: { menu: { say: hi, then: livekit/hangup } }\n",
	})
	if _, err := os.Stat(filepath.Join(dir, "NODES.yaml")); err != nil {
		t.Skip("the file system is case-sensitive")
	}
	group, err := loadScenarioGroup(filepath.Join(dir, "dental.yaml"))
	require.NoError(t, err)
	require.Len(t, group.Nodes, 1)
}

func TestAnchorKeysAreAllowed(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"dental.yaml": "id: g\nx-hangup: &bye { then: livekit/hangup }\ninclude: [nodes.yaml]\nnodes: { menu: { <<: *bye, say: hi } }\nscenarios: [{ id: s, label: l, start: menu }]\n",
		"nodes.yaml":  "x-hangup: &bye { then: livekit/hangup }\nnodes: { sam: { <<: *bye, say: hello } }\n",
	})
	group, err := loadScenarioGroup(filepath.Join(dir, "dental.yaml"))
	require.NoError(t, err)
	require.Equal(t, "livekit/hangup", group.Nodes["sam"].Then)
}

func TestScenarioIDsAreNotWrittenIntoAnIncludedFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"nodes.yaml": "nodes: { sam: { say: hi, then: livekit/hangup } }\n"})
	path := filepath.Join(dir, "nodes.yaml")

	err := ensureScenarioIDs(&cli.Command{}, path)
	require.ErrorContains(t, err, "has no `scenarios`")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "id:")
}
