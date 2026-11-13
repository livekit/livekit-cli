// Copyright 2025 LiveKit, Inc.
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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, LiveKitTOMLFile), []byte(body), 0o644))
	return dir
}

func TestLoadTOMLFile_LegacyAgentIDMovesToCloud(t *testing.T) {
	dir := writeTOML(t, `
[project]
subdomain = "proj"

[agent]
id = "CA_legacy"
name = "my-agent"
`)
	cfg, exists, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.True(t, exists)
	require.NoError(t, err)
	require.Equal(t, "my-agent", cfg.Agent.Name)
	require.Empty(t, cfg.Agent.ID)
	require.Equal(t, "CA_legacy", cfg.AgentID())
}

func TestLoadTOMLFile_CloudID(t *testing.T) {
	dir := writeTOML(t, `
[project]
subdomain = "proj"

[agent]
name = "my-agent"

[cloud]
id = "CA_a"
`)
	cfg, _, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.NoError(t, err)
	require.Equal(t, "CA_a", cfg.AgentID())
}

func TestSaveTOMLFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := NewLiveKitTOML("proj").WithDefaultAgent()
	cfg.Agent.Name = "my-agent"
	cfg.Cloud = &LiveKitTOMLCloudConfig{ID: "CA_a"}
	require.NoError(t, cfg.SaveTOMLFile(dir, LiveKitTOMLFile))
	raw, err := os.ReadFile(filepath.Join(dir, LiveKitTOMLFile))
	require.NoError(t, err)
	require.Equal(t, `[project]
  subdomain = "proj"

[agent]
  name = "my-agent"

[cloud]
  id = "CA_a"
`, string(raw))

	loaded, _, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.NoError(t, err)
	require.Equal(t, cfg.Agent.Name, loaded.Agent.Name)
	require.Equal(t, cfg.AgentID(), loaded.AgentID())
}
