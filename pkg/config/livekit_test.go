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
	require.Equal(t, map[string]string{"": "CA_legacy"}, cfg.AgentIDs())
}

func TestLoadTOMLFile_CloudRegions(t *testing.T) {
	dir := writeTOML(t, `
[project]
subdomain = "proj"

[agent]
name = "my-agent"

[cloud.us-east]
id = "CA_a"

[cloud.eu-central]
id = "CA_b"
`)
	cfg, _, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"us-east": "CA_a", "eu-central": "CA_b"}, cfg.AgentIDs())

	id, err := cfg.AgentID("eu-central")
	require.NoError(t, err)
	require.Equal(t, "CA_b", id)

	_, err = cfg.AgentID("")
	require.ErrorIs(t, err, ErrInvalidConfig)
	_, err = cfg.AgentID("ap-south")
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestLoadTOMLFile_CloudIDAndRegionsAreExclusive(t *testing.T) {
	dir := writeTOML(t, `
[project]
subdomain = "proj"

[cloud]
id = "CA_a"

[cloud.us-east]
id = "CA_b"
`)
	_, _, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestSaveTOMLFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := NewLiveKitTOML("proj").WithDefaultAgent()
	cfg.Agent.Name = "my-agent"
	cfg.SetAgentID("us-east", "CA_a", "")
	require.Equal(t, "CA_a", cfg.Cloud.ID, "a single region stays flat")

	cfg.SetAgentID("eu-central", "CA_b", "us-east")
	require.Empty(t, cfg.Cloud.ID)
	require.Equal(t, map[string]string{"us-east": "CA_a", "eu-central": "CA_b"}, cfg.Cloud.Regions)

	require.NoError(t, cfg.SaveTOMLFile(dir, LiveKitTOMLFile))
	raw, err := os.ReadFile(filepath.Join(dir, LiveKitTOMLFile))
	require.NoError(t, err)
	require.Contains(t, string(raw), "[cloud.us-east]")
	require.NotContains(t, string(raw), "[agent]\n  id")

	loaded, _, err := LoadTOMLFile(dir, LiveKitTOMLFile)
	require.NoError(t, err)
	require.Equal(t, cfg.Agent.Name, loaded.Agent.Name)
	require.Equal(t, cfg.AgentIDs(), loaded.AgentIDs())
}

func TestSaveTOMLFile_FlatCloudLayout(t *testing.T) {
	dir := t.TempDir()
	cfg := NewLiveKitTOML("proj").WithDefaultAgent()
	cfg.Agent.Name = "my-agent"
	cfg.SetAgentID("", "CA_a", "")
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
}
