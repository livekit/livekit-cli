package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

func TestConsoleInjectsOptionalCredentialsBeforePythonImports(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python is not installed")
	}
	for _, scenario := range []string{"no project", "default project", "explicit credentials", "invalid named project"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".livekit"), 0o755))
			conf := config.CLIConfig{SkillsPrompted: true}
			want := []string{"", "", ""}
			if scenario == "default project" || scenario == "explicit credentials" {
				conf.DefaultProject = "configured"
				conf.Projects = []config.ProjectConfig{{Name: "configured", URL: "wss://configured.invalid", APIKey: "configured-key", APISecret: "configured-secret"}}
				want = []string{"wss://configured.invalid", "configured-key", "configured-secret"}
			}
			raw, err := yaml.Marshal(conf)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, ".livekit", "cli-config.yaml"), raw, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("livekit-agents\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.py"), nil, 0o644))
			module := filepath.Join(dir, "livekit", "agents")
			require.NoError(t, os.MkdirAll(module, 0o755))
			// Observe credentials during the SDK import, before an app entrypoint
			// or its CLI parser can do anything to populate them.
			require.NoError(t, os.WriteFile(filepath.Join(module, "__init__.py"), []byte(
				"import json, os\nfrom pathlib import Path\n"+
					"Path('import-env.json').write_text(json.dumps([os.environ.get(k, '') for k in ('LIVEKIT_URL', 'LIVEKIT_API_KEY', 'LIVEKIT_API_SECRET')]))\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(module, "__main__.py"), []byte("raise SystemExit(17)\n"), 0o644))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			args := []string{"agent", "console", "--text", "agent.py"}
			switch scenario {
			case "explicit credentials":
				args = append([]string{"--url", "wss://explicit.invalid", "--api-key", "explicit-key", "--api-secret", "explicit-secret"}, args...)
				want = []string{"wss://explicit.invalid", "explicit-key", "explicit-secret"}
			case "invalid named project":
				args = append([]string{"--project", "missing"}, args...)
			}
			cmd := exec.CommandContext(ctx, "lk", args...)
			cmd.Dir = dir
			for _, kv := range os.Environ() {
				key := strings.SplitN(kv, "=", 2)[0]
				if strings.HasPrefix(key, "LIVEKIT_") || key == "HOME" || key == "USERPROFILE" || key == "VIRTUAL_ENV" || key == "PYTHONPATH" {
					continue
				}
				cmd.Env = append(cmd.Env, kv)
			}
			cmd.Env = append(cmd.Env, "HOME="+home, "USERPROFILE="+home, "PYTHONPATH="+dir, "LK_NO_SKILLS_PROMPT=1")
			output, err := cmd.CombinedOutput()
			require.Error(t, err, "stub must exit before console connects")
			if scenario == "invalid named project" {
				require.Contains(t, string(output), "project not found")
				_, err := os.Stat(filepath.Join(dir, "import-env.json"))
				require.True(t, os.IsNotExist(err))
				return
			}
			raw, err = os.ReadFile(filepath.Join(dir, "import-env.json"))
			require.NoError(t, err, string(output))
			var got []string
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, want, got)
			require.NotContains(t, string(output), "configured-secret")
			require.NotContains(t, string(output), "explicit-secret")
		})
	}
}
