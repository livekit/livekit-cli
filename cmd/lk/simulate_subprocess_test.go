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

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func writeEntrypoint(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, nil, 0o644))
}

func TestFindEntrypointFromSubdirectory(t *testing.T) {
	// Run from a folder inside the project, its agent there is found.
	root := t.TempDir()
	writeEntrypoint(t, filepath.Join(root, "examples", "drive-thru", "agent.py"))
	t.Chdir(filepath.Join(root, "examples", "drive-thru"))

	entry, err := findEntrypoint(root, "", agentfs.ProjectTypePythonUV)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("examples", "drive-thru", "agent.py"), entry)
}

func TestFindEntrypointIgnoresParentProject(t *testing.T) {
	// cwd is one agent project, and another project is in a folder below
	// it. The child's own agent is found, not the parent's.
	parent := t.TempDir()
	writeEntrypoint(t, filepath.Join(parent, "src", "agent.py"))
	child := filepath.Join(parent, "my-agent")
	writeEntrypoint(t, filepath.Join(child, "src", "agent.py"))
	t.Chdir(parent)

	entry, err := findEntrypoint(child, "", agentfs.ProjectTypePythonUV)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("src", "agent.py"), entry)
}
