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

package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func timeNow() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) }

var cursorEntry = map[string]any{"url": MCPServerURL}

func TestAddJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "new file",
			in:   "",
			want: "{\n  \"mcpServers\": {\n    \"livekit-docs\": {\n      \"url\": \"https://docs.livekit.io/mcp\"\n    }\n  }\n}\n",
		},
		{
			name: "existing servers keep their formatting and comments",
			in:   "{\n    // mine\n    \"mcpServers\": {\n        \"other\": { \"url\": \"https://x\" }\n    }\n}\n",
			want: "{\n    // mine\n    \"mcpServers\": {\n        \"other\": { \"url\": \"https://x\" },\n        \"livekit-docs\": {\n            \"url\": \"https://docs.livekit.io/mcp\"\n        }\n    }\n}\n",
		},
		{
			name: "no servers key",
			in:   "{\n  \"theme\": \"dark\"\n}\n",
			want: "{\n  \"theme\": \"dark\",\n  \"mcpServers\": {\n    \"livekit-docs\": {\n      \"url\": \"https://docs.livekit.io/mcp\"\n    }\n  }\n}\n",
		},
		{
			name: "empty servers object",
			in:   "{\n  \"mcpServers\": {}\n}\n",
			want: "{\n  \"mcpServers\": {\n    \"livekit-docs\": {\n      \"url\": \"https://docs.livekit.io/mcp\"\n    }\n  }\n}\n",
		},
		{
			name: "one-line file",
			in:   `{"numStartups": 3}`,
			want: `{"numStartups": 3, "mcpServers": {"livekit-docs":{"url":"https://docs.livekit.io/mcp"}}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := addJSON([]byte(c.in), "mcpServers", cursorEntry)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(got))
		})
	}

	_, err := addJSON([]byte(`{"mcpServers": []}`), "mcpServers", cursorEntry)
	assert.Error(t, err)
	_, err = addJSON([]byte(`[]`), "mcpServers", cursorEntry)
	assert.Error(t, err)
}

func TestConfigureMCP(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	cursor := FindAgent("cursor")

	st := CheckMCP(env, ScopeProject, cursor)
	assert.Equal(t, MCPMissing, st.State)

	st, err := ConfigureMCP(ctx, env, ScopeProject, cursor)
	require.NoError(t, err)
	assert.Equal(t, MCPConfigured, st.State)
	assert.True(t, st.Added)
	assert.Equal(t, filepath.Join(env.Root, ".cursor", "mcp.json"), st.Path)

	// Idempotent.
	st, err = ConfigureMCP(ctx, env, ScopeProject, cursor)
	require.NoError(t, err)
	assert.Equal(t, MCPConfigured, st.State)
	assert.False(t, st.Added)

	// Trailing slash counts as the same server.
	vscode := filepath.Join(env.Root, ".vscode", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(vscode), 0o755))
	require.NoError(t, os.WriteFile(vscode, []byte(`{"servers": {"livekit-docs": {"type": "http", "url": "https://docs.livekit.io/mcp/"}}}`), 0o644))
	assert.Equal(t, MCPConfigured, CheckMCP(env, ScopeProject, FindAgent("github-copilot")).State)

	// A same-named entry pointing elsewhere is left alone.
	gemini := filepath.Join(env.Root, ".gemini", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(gemini), 0o755))
	custom := `{"mcpServers": {"livekit-docs": {"httpUrl": "http://localhost:3000/mcp"}}}`
	require.NoError(t, os.WriteFile(gemini, []byte(custom), 0o644))
	st, err = ConfigureMCP(ctx, env, ScopeProject, FindAgent("gemini-cli"))
	require.NoError(t, err)
	assert.Equal(t, MCPCustom, st.State)
	data, _ := os.ReadFile(gemini)
	assert.Equal(t, custom, string(data))

	// Unreadable config is an error, not something to overwrite.
	bad := filepath.Join(env.Root, ".mcp.json")
	require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o644))
	_, err = ConfigureMCP(ctx, env, ScopeProject, FindAgent("claude-code"))
	require.Error(t, err)

	// Some agents can't be configured automatically.
	assert.Equal(t, MCPUnsupported, CheckMCP(env, ScopeProject, FindAgent("windsurf")).State)
	assert.Equal(t, MCPUnsupported, CheckMCP(env, ScopeGlobal, FindAgent("goose")).State)
}

func TestConfigureMCPCodex(t *testing.T) {
	env := testEnv(t)
	codex := FindAgent("codex")
	path := filepath.Join(env.Home, ".codex", "config.toml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"), 0o644))

	st, err := ConfigureMCP(context.Background(), env, ScopeGlobal, codex)
	require.NoError(t, err)
	assert.Equal(t, MCPConfigured, st.State)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.livekit-docs]\nurl = \"https://docs.livekit.io/mcp\"\n", string(data))
	assert.Equal(t, MCPConfigured, CheckMCP(env, ScopeGlobal, codex).State)

	// Servers defined as an inline table can't take an appended section.
	require.NoError(t, os.WriteFile(path, []byte("mcp_servers = { other = { command = \"x\" } }\n"), 0o644))
	_, err = ConfigureMCP(context.Background(), env, ScopeGlobal, codex)
	require.ErrorContains(t, err, "can't add livekit-docs automatically")
}

func TestConfigureMCPUsesAgentCLI(t *testing.T) {
	env := testEnv(t)
	env.LookPath = func(bin string) (string, error) {
		if bin == "claude" {
			return "/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	// With `claude` on PATH, lk runs `claude mcp add` rather than editing
	// ~/.claude.json. A fake PATH entry makes the command fail, proving it ran.
	t.Setenv("PATH", t.TempDir())
	_, err := ConfigureMCP(context.Background(), env, ScopeGlobal, FindAgent("claude-code"))
	require.ErrorContains(t, err, "claude mcp add --scope user --transport http livekit-docs https://docs.livekit.io/mcp")
	assert.NoFileExists(t, filepath.Join(env.Home, ".claude.json"))
}
