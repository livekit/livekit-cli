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

package assistant

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestDescribeFolderEmpty(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{".DS_Store": ""}))
	require.Equal(t, FolderEmpty, f.Kind)
}

func TestDescribeFolderOther(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{"notes.txt": "hi"}))
	require.Equal(t, FolderOther, f.Kind)
	require.Empty(t, f.Summary)
}

func TestDescribeFolderGo(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{"go.mod": `module example.com/app

go 1.24

require (
	github.com/livekit/protocol v1.39.0 // indirect
	github.com/livekit/server-sdk-go/v2 v2.9.1
)
`}))
	require.Equal(t, FolderLiveKit, f.Kind)
	require.Equal(t, []Package{{Name: "github.com/livekit/server-sdk-go/v2", Version: "v2.9.1"}}, f.Projects[0].LiveKit)
	require.Equal(t, "a Go project that uses the LiveKit Go server SDK", f.Summary)
}

func TestDescribeFolderPythonAgent(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{"pyproject.toml": `[project]
name = "livekit-agent-starter"
dependencies = [
    "livekit-agents[silero,turn-detector]~=1.8",
    "livekit-plugins-noise-cancellation~=0.2",
]
`}))
	require.Equal(t, FolderLiveKit, f.Kind)
	require.True(t, f.HasAgents)
	require.Equal(t, []Package{
		{Name: "livekit-agents", Version: "~=1.8"},
		{Name: "livekit-plugins-noise-cancellation", Version: "~=0.2"},
	}, f.Projects[0].LiveKit)
	require.Equal(t, "a Python project that uses LiveKit Agents", f.Summary)
}

func TestDescribeFolderAppWithoutLiveKit(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{
		"package.json":  `{"dependencies": {"next": "15.0.0", "react": "19.0.0"}}`,
		"tsconfig.json": "{}",
	}))
	require.Equal(t, FolderApp, f.Kind)
	require.Equal(t, "TypeScript", f.Projects[0].Language)
	require.Equal(t, "a Next.js app", f.Summary)
}

func TestDescribeFolderSubfolders(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{
		"README.md":                   "",
		"frontend/package.json":       `{"dependencies": {"next": "15.0.0", "livekit-client": "^2.15.0", "@livekit/components-react": "^2.9.0"}}`,
		"agent/requirements.txt":      "livekit-agents==1.8.5\n",
		"node_modules/x/package.json": `{}`,
	}))
	require.Equal(t, FolderLiveKit, f.Kind)
	require.Len(t, f.Projects, 2)
	require.Equal(t, "a Python project in agent that uses LiveKit Agents and a Next.js app in frontend that uses LiveKit React components and the LiveKit client SDK", f.Summary)
	require.True(t, f.HasAgents)
}

func TestDescribeFolderManyProjects(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{
		"a/go.mod":           "module a\n",
		"b/requirements.txt": "livekit-agents~=1.8\n",
		"c/package.json":     `{"dependencies": {"livekit-client": "^2.15.0"}}`,
	}))
	require.Equal(t, FolderLiveKit, f.Kind)
	require.True(t, f.HasAgents)
	require.Equal(t, "a Python project in b that uses LiveKit Agents and a JavaScript project in c that uses the LiveKit client SDK, and one other project", f.Summary)
}

func TestDescribeFolderManyLiveKitProjects(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{
		"a/requirements.txt": "livekit-agents~=1.8\n",
		"b/requirements.txt": "livekit-agents~=1.8\n",
		"c/requirements.txt": "livekit-agents~=1.8\n",
		"d/go.mod":           "module d\n",
	}))
	require.False(t, f.HasAgents)
	require.Equal(t, "4 projects, and 3 of them use LiveKit", f.Summary)
}

func TestDescribeFolderMonorepo(t *testing.T) {
	f := DescribeFolder(writeFiles(t, map[string]string{
		"package.json":                 `{"name": "pets", "private": true, "scripts": {"dev": "pnpm --filter web dev"}}`,
		"pnpm-workspace.yaml":          "packages:\n  - 'apps/web'\n  - 'packages/*'\n",
		"apps/web/package.json":        `{"dependencies": {"next": "16.3.6", "livekit-client": "^2.22.2", "livekit-server-sdk": "^2.18.0"}}`,
		"apps/web/src/package.json":    `{}`,
		"apps/agent/pyproject.toml":    "[project]\ndependencies = [\"livekit-agents~=1.8\"]\n",
		"packages/shared/package.json": `{"name": "shared"}`,
		"docs/notes.md":                "",
	}))
	require.Equal(t, FolderLiveKit, f.Kind)
	require.True(t, f.HasAgents)
	require.Len(t, f.Projects, 3)
	require.Equal(t, "a Python project in apps/agent that uses LiveKit Agents and a Next.js app in apps/web that uses the LiveKit client SDK and the LiveKit server SDK, and one other project", f.Summary)
}
