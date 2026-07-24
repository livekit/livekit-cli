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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

const localhostURL = "http://localhost:7880"

func TestAgentProcessFailSignal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	// The first node.exe spawn on a Windows CI runner can take several seconds
	// (Defender scans the binary on first exec), which blows the SDK version
	// probe's 5s timeout inside startAgent and fails the test before the
	// crash-signal behavior under test ever runs. Spawn a throwaway Node
	// process first to absorb the cold start.
	require.NoError(t, exec.Command("node", "-e",
		`console.log('pre-warming node so the SDK version probe does not time out on a cold runner')`).Run())

	// An agent whose job crashes logs a marker but keeps the process alive;
	// Failed() must fire without waiting for exit.
	dir := t.TempDir()
	script := `console.log('shutting down job task {"reason": "job crashed"}'); setTimeout(() => {}, 30000);`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.js"), []byte(script), 0o644))
	// startAgent gates on the SDK version, which Node resolves from node_modules,
	// so install a satisfying @livekit/agents stub.
	agentsDir := filepath.Join(dir, "node_modules", "@livekit", "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "package.json"),
		[]byte(`{"name": "@livekit/agents", "version": "1.6.0"}`), 0o644))

	ap, err := startAgent(AgentStartConfig{
		Dir:         dir,
		Entrypoint:  "agent.js",
		ProjectType: agentfs.ProjectTypeNode,
		FailSignals: consoleCrashSignals,
	})
	require.NoError(t, err)
	defer ap.Kill()

	select {
	case <-ap.Failed():
	case <-time.After(10 * time.Second):
		t.Fatal("Failed() did not fire on crash marker")
	}
}

// clearLiveKitEnv removes the LIVEKIT_* connection env vars for the duration of
// the test so flag resolution is hermetic regardless of the developer's shell.
func clearLiveKitEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LIVEKIT_URL", "LIVEKIT_API_KEY", "LIVEKIT_API_SECRET"} {
		if prev, ok := os.LookupEnv(k); ok {
			key, val := k, prev
			require.NoError(t, os.Unsetenv(key))
			t.Cleanup(func() { _ = os.Setenv(key, val) })
		}
	}
}

// credentialFlags returns fresh instances of the connection flags, mirroring the
// production definitions in globalFlags (verified by
// TestGlobalFlagsMatchCredentialFlags). Fresh instances are required because
// urfave/cli caches parse state on the flag pointers; reusing the shared
// package-level globalFlags across multiple app.Run calls in one test binary
// would leak state between cases. Production builds the app once, so it is
// unaffected.
func credentialFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "url",
			Sources: cli.EnvVars("LIVEKIT_URL"),
			Value:   localhostURL,
		},
		&cli.StringFlag{
			Name:    "api-key",
			Sources: cli.EnvVars("LIVEKIT_API_KEY"),
		},
		&cli.StringFlag{
			Name:    "api-secret",
			Sources: cli.EnvVars("LIVEKIT_API_SECRET"),
		},
	}
}

// runWithCredentialFlags parses argv against fresh credential flags and returns
// the agentCredentials that explicitCredentials extracts inside the action. This
// exercises the actual urfave/cli wiring (localhost default, env sources, IsSet).
func runWithCredentialFlags(t *testing.T, argv []string) agentCredentials {
	t.Helper()
	var got agentCredentials
	app := &cli.Command{
		Name:  "lk",
		Flags: credentialFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			got = explicitCredentials(cmd)
			return nil
		},
	}
	require.NoError(t, app.Run(context.Background(), argv))
	return got
}

// TestGlobalFlagsMatchCredentialFlags guards against drift between the test
// fixtures and the real global flag definitions: the production --url flag must
// keep its localhost default and the three flags must read their LIVEKIT_* env
// vars, or the fix's assumptions break.
func TestGlobalFlagsMatchCredentialFlags(t *testing.T) {
	find := func(flags []cli.Flag, name string) *cli.StringFlag {
		for _, f := range flags {
			if sf, ok := f.(*cli.StringFlag); ok && sf.Name == name {
				return sf
			}
		}
		t.Fatalf("flag %q not found", name)
		return nil
	}

	urlFlag := find(globalFlags, "url")
	assert.Equal(t, localhostURL, urlFlag.Value, "production --url default changed; update localhostURL")
	assert.Contains(t, urlFlag.Sources.EnvKeys(), "LIVEKIT_URL")
	assert.Contains(t, find(globalFlags, "api-key").Sources.EnvKeys(), "LIVEKIT_API_KEY")
	assert.Contains(t, find(globalFlags, "api-secret").Sources.EnvKeys(), "LIVEKIT_API_SECRET")
}

// --- explicitCredentials: defaults vs. intentional overrides -----------------

func TestExplicitCredentials_LocalhostDefaultIsNotExplicit(t *testing.T) {
	// The crux of the bug: with nothing provided, the --url localhost default
	// must NOT be reported as an explicit value, otherwise it masks the
	// configured project's URL downstream.
	clearLiveKitEnv(t)

	got := runWithCredentialFlags(t, []string{"lk"})

	assert.Empty(t, got.url, "localhost default must not count as explicitly set")
	assert.Empty(t, got.apiKey)
	assert.Empty(t, got.apiSecret)
}

func TestExplicitCredentials_URLFlagIsExplicit(t *testing.T) {
	clearLiveKitEnv(t)

	got := runWithCredentialFlags(t, []string{"lk", "--url", "wss://example.livekit.cloud"})

	assert.Equal(t, "wss://example.livekit.cloud", got.url)
	assert.Empty(t, got.apiKey)
	assert.Empty(t, got.apiSecret)
}

func TestExplicitCredentials_ExplicitLocalhostFlagIsHonored(t *testing.T) {
	// A user intentionally passing the localhost URL must be treated as explicit,
	// even though it equals the default value.
	clearLiveKitEnv(t)

	got := runWithCredentialFlags(t, []string{"lk", "--url", localhostURL})

	assert.Equal(t, localhostURL, got.url)
}

func TestExplicitCredentials_EnvVarsAreExplicit(t *testing.T) {
	clearLiveKitEnv(t)
	t.Setenv("LIVEKIT_URL", "wss://from-env.livekit.cloud")
	t.Setenv("LIVEKIT_API_KEY", "envkey")
	t.Setenv("LIVEKIT_API_SECRET", "envsecret")

	got := runWithCredentialFlags(t, []string{"lk"})

	assert.Equal(t, "wss://from-env.livekit.cloud", got.url)
	assert.Equal(t, "envkey", got.apiKey)
	assert.Equal(t, "envsecret", got.apiSecret)
}

func TestExplicitCredentials_AllFlags(t *testing.T) {
	clearLiveKitEnv(t)

	got := runWithCredentialFlags(t, []string{
		"lk",
		"--url", "wss://example.livekit.cloud",
		"--api-key", "flagkey",
		"--api-secret", "flagsecret",
	})

	assert.Equal(t, agentCredentials{
		url:       "wss://example.livekit.cloud",
		apiKey:    "flagkey",
		apiSecret: "flagsecret",
	}, got)
}

func TestExplicitCredentials_KeyAndSecretWithoutURL(t *testing.T) {
	// Common local-server usage: key/secret supplied, URL left to the default.
	// URL must remain unset so the project (or the localhost default applied by
	// loadProjectDetails) can fill it in.
	clearLiveKitEnv(t)

	got := runWithCredentialFlags(t, []string{
		"lk",
		"--api-key", "devkey",
		"--api-secret", "secret",
	})

	assert.Empty(t, got.url)
	assert.Equal(t, "devkey", got.apiKey)
	assert.Equal(t, "secret", got.apiSecret)
}

// --- mergeCredentials: explicit overrides project, project fills the gaps -----

func TestMergeCredentials(t *testing.T) {
	cloud := agentCredentials{
		url:       "wss://project.livekit.cloud",
		apiKey:    "projkey",
		apiSecret: "projsecret",
	}

	tests := []struct {
		name     string
		explicit agentCredentials
		project  agentCredentials
		want     agentCredentials
	}{
		{
			name:     "nothing explicit -> project wins (the bug fix)",
			explicit: agentCredentials{},
			project:  cloud,
			want:     cloud,
		},
		{
			name:     "explicit url overrides project url, project fills creds",
			explicit: agentCredentials{url: localhostURL},
			project:  cloud,
			want:     agentCredentials{url: localhostURL, apiKey: "projkey", apiSecret: "projsecret"},
		},
		{
			name:     "fully explicit -> project ignored",
			explicit: agentCredentials{url: localhostURL, apiKey: "k", apiSecret: "s"},
			project:  cloud,
			want:     agentCredentials{url: localhostURL, apiKey: "k", apiSecret: "s"},
		},
		{
			name:     "explicit creds, project supplies url",
			explicit: agentCredentials{apiKey: "k", apiSecret: "s"},
			project:  cloud,
			want:     agentCredentials{url: "wss://project.livekit.cloud", apiKey: "k", apiSecret: "s"},
		},
		{
			name:     "project supplies localhost url for local dev",
			explicit: agentCredentials{apiKey: "devkey", apiSecret: "secret"},
			project:  agentCredentials{url: localhostURL},
			want:     agentCredentials{url: localhostURL, apiKey: "devkey", apiSecret: "secret"},
		},
		{
			name:     "empty project leaves explicit untouched",
			explicit: agentCredentials{url: "wss://a", apiKey: "k", apiSecret: "s"},
			project:  agentCredentials{},
			want:     agentCredentials{url: "wss://a", apiKey: "k", apiSecret: "s"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mergeCredentials(tc.explicit, tc.project))
		})
	}
}

// --- agentCredentials.complete / env ------------------------------------------

func TestAgentCredentialsComplete(t *testing.T) {
	assert.True(t, agentCredentials{url: "u", apiKey: "k", apiSecret: "s"}.complete())
	assert.False(t, agentCredentials{apiKey: "k", apiSecret: "s"}.complete(), "missing url")
	assert.False(t, agentCredentials{url: "u", apiSecret: "s"}.complete(), "missing key")
	assert.False(t, agentCredentials{url: "u", apiKey: "k"}.complete(), "missing secret")
	assert.False(t, agentCredentials{}.complete())
}

func TestAgentCredentialsEnv(t *testing.T) {
	tests := []struct {
		name  string
		creds agentCredentials
		want  []string
	}{
		{
			name:  "all set",
			creds: agentCredentials{url: "u", apiKey: "k", apiSecret: "s"},
			want:  []string{"LIVEKIT_URL=u", "LIVEKIT_API_KEY=k", "LIVEKIT_API_SECRET=s"},
		},
		{
			name:  "empty fields are skipped",
			creds: agentCredentials{apiKey: "k"},
			want:  []string{"LIVEKIT_API_KEY=k"},
		},
		{
			name:  "nothing set -> no env",
			creds: agentCredentials{},
			want:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.creds.env())
		})
	}
}

// --- resolveCredentials: end-to-end for the no-project-lookup path ------------
//
// When the command line fully specifies the connection, resolveCredentials must
// not consult the project config at all (so it is safe to run here without a
// configured CLI config / network). The project-backed paths are covered by the
// mergeCredentials table above.

func TestResolveCredentials_FullyExplicitSkipsProjectLookup(t *testing.T) {
	clearLiveKitEnv(t)

	var env []string
	app := &cli.Command{
		Name:  "lk",
		Flags: globalFlags,
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error
			env, err = resolveCredentials(cmd)
			return err
		},
	}

	err := app.Run(context.Background(), []string{
		"lk",
		"--url", "wss://example.livekit.cloud",
		"--api-key", "flagkey",
		"--api-secret", "flagsecret",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"LIVEKIT_URL=wss://example.livekit.cloud",
		"LIVEKIT_API_KEY=flagkey",
		"LIVEKIT_API_SECRET=flagsecret",
	}, env)
}

// --- cloud console link --------------------------------------------------------

func TestCloudProject(t *testing.T) {
	tests := []struct {
		url      string
		wantHost string
		wantSub  string
	}{
		{"wss://dztest2.livekit.cloud", "cloud.livekit.io", "dztest2"},
		{"https://my-proj.livekit.cloud", "cloud.livekit.io", "my-proj"},
		{"wss://dztest2.livekit.cloud:443/rtc", "cloud.livekit.io", "dztest2"}, // port + path ignored
		{"wss://DZTEST2.LiveKit.Cloud", "cloud.livekit.io", "DZTEST2"},         // host match is case-insensitive
		{"wss://dztest2.staging.livekit.cloud", "cloud.staging.livekit.io", "dztest2"},
		{"wss://my-proj.staging.livekit.cloud:443/rtc", "cloud.staging.livekit.io", "my-proj"},
		{"http://localhost:7880", "", ""},     // self-hosted
		{"ws://192.168.1.10:7880", "", ""},    // self-hosted IP
		{"wss://x.dev.livekit.cloud", "", ""}, // unrecognized multi-label host
		{"wss://example.com", "", ""},         // not livekit.cloud
		{"wss://livekit.cloud", "", ""},       // no subdomain
		{"", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			host, sub := cloudProject(tc.url)
			assert.Equal(t, tc.wantHost, host)
			assert.Equal(t, tc.wantSub, sub)
		})
	}
}

func TestCloudConsoleURL(t *testing.T) {
	assert.Equal(t,
		"https://cloud.livekit.io/projects/d_dztest2/agents/console?agentName=my-agent&autoStart=false",
		cloudConsoleURL("wss://dztest2.livekit.cloud", "my-agent"),
	)
	// staging projects point at the staging console host
	assert.Equal(t,
		"https://cloud.staging.livekit.io/projects/d_dztest2/agents/console?agentName=my-agent&autoStart=false",
		cloudConsoleURL("wss://dztest2.staging.livekit.cloud", "my-agent"),
	)
	// empty agent name (the common dev default) still yields a usable link
	assert.Equal(t,
		"https://cloud.livekit.io/projects/d_dztest2/agents/console?agentName=&autoStart=false",
		cloudConsoleURL("wss://dztest2.livekit.cloud", ""),
	)
	// agent names are query-escaped
	assert.Equal(t,
		"https://cloud.livekit.io/projects/d_dztest2/agents/console?agentName=my+agent%2F1&autoStart=false",
		cloudConsoleURL("wss://dztest2.livekit.cloud", "my agent/1"),
	)
	// non-cloud URLs produce no link
	assert.Empty(t, cloudConsoleURL("http://localhost:7880", "my-agent"))
}
