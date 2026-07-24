// Copyright 2024 LiveKit, Inc.
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
	"bytes"
	"context"
	"io"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// findCommandByName returns the command with the given name from commands, or nil.
func findCommandByName(commands []*cli.Command, name string) *cli.Command {
	for _, cmd := range commands {
		if cmd != nil && cmd.Name == name {
			return cmd
		}
	}
	return nil
}

// findFlagByName returns the flag matching name (by any of its names) from flags, or nil.
func findFlagByName(flags []cli.Flag, name string) cli.Flag {
	for _, flag := range flags {
		if flag == nil {
			continue
		}
		if slices.Contains(flag.Names(), name) {
			return flag
		}
	}
	return nil
}

// withCapturedAnnounce swaps the package-level Printer for a buffer-backed one for the
// duration of the test, returning the buffer that captures status output. The Printer's
// nil-safety means we don't have to worry about state from other tests.
func withCapturedAnnounce(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := out
	var buf bytes.Buffer
	out = util.NewPrinter(io.Discard, &buf, false)
	t.Cleanup(func() { out = prev })
	return &buf
}

// resolveWith parses the given args against a fresh copy of the credential-related global
// flags and returns the resolveProject outcome. Fresh flags per call avoid state leaking
// between subtests, and isolating to these flags keeps the test independent of the
// on-disk CLI config (the branches exercised here return before any config is read).
func resolveWith(t *testing.T, args ...string) (*resolvedProject, error) {
	t.Helper()
	var rp *resolvedProject
	var rerr error
	app := &cli.Command{
		Name: "lk",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "url", Sources: cli.EnvVars("LIVEKIT_URL"), Value: "http://localhost:7880"},
			&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("LIVEKIT_API_KEY")},
			&cli.StringFlag{Name: "api-secret", Sources: cli.EnvVars("LIVEKIT_API_SECRET")},
			&cli.BoolFlag{Name: "dev"},
			&cli.StringFlag{Name: "project"},
			&cli.StringFlag{Name: "subdomain"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			rp, rerr = resolveProject(cmd, loadParams{requireURL: true})
			return nil
		},
	}
	require.NoError(t, app.Run(context.Background(), append([]string{"lk"}, args...)))
	return rp, rerr
}

func TestResolveProjectSource(t *testing.T) {
	// Isolate from any ambient LIVEKIT_* credentials in the dev's environment.
	t.Setenv("LIVEKIT_URL", "")
	t.Setenv("LIVEKIT_API_KEY", "")
	t.Setenv("LIVEKIT_API_SECRET", "")

	t.Run("dev credentials", func(t *testing.T) {
		buf := withCapturedAnnounce(t)
		rp, err := resolveWith(t, "--dev")
		require.NoError(t, err)
		require.NotNil(t, rp)
		assert.Equal(t, sourceDev, rp.source)
		assert.Equal(t, "devkey", rp.project.APIKey)

		rp.announce()
		assert.Equal(t, "Using dev credentials\n", buf.String())
	})

	t.Run("inline flags are name-less and silent", func(t *testing.T) {
		buf := withCapturedAnnounce(t)
		rp, err := resolveWith(t, "--url", "ws://x", "--api-key", "k", "--api-secret", "s")
		require.NoError(t, err)
		require.NotNil(t, rp)
		assert.Equal(t, sourceInlineFlags, rp.source)
		assert.Empty(t, rp.envVars)

		rp.announce()
		assert.Empty(t, buf.String(), "name-less sources surface nothing to the user")
	})

	t.Run("env credentials are reported", func(t *testing.T) {
		t.Setenv("LIVEKIT_URL", "ws://env-url")
		t.Setenv("LIVEKIT_API_KEY", "envkey")
		t.Setenv("LIVEKIT_API_SECRET", "envsecret")
		buf := withCapturedAnnounce(t)
		rp, err := resolveWith(t)
		require.NoError(t, err)
		require.NotNil(t, rp)
		assert.Equal(t, sourceEnv, rp.source)
		assert.ElementsMatch(t, []string{"url", "api-key", "api-secret"}, rp.envVars)

		rp.announce()
		assert.Equal(t, "Using url, api-key, api-secret from environment\n", buf.String())
	})

	t.Run("project and dev conflict", func(t *testing.T) {
		_, err := resolveWith(t, "--project", "foo", "--dev")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both project and dev flags are set")
	})

	t.Run("--quiet suppresses the breadcrumb", func(t *testing.T) {
		prev := out
		var buf bytes.Buffer
		out = util.NewPrinter(io.Discard, &buf, true /* quiet */)
		t.Cleanup(func() { out = prev })

		rp, err := resolveWith(t, "--dev")
		require.NoError(t, err)
		rp.announce()
		assert.Empty(t, buf.String(), "--quiet suppresses status output")
	})
}

func TestOptionalFlag(t *testing.T) {
	requiredFlag := &cli.StringFlag{
		Name:     "test",
		Required: true,
	}
	optionalFlag := optional(requiredFlag)

	if requiredFlag == optionalFlag {
		t.Error("optional should return a new flag")
	}
	if !requiredFlag.Required {
		t.Error("optional should not mutate the original flag")
	}
	if optionalFlag.Required {
		t.Error("optional should return a new flag with Required set to false")
	}
}

func TestHiddenFlag(t *testing.T) {
	visibleFlag := &cli.StringFlag{
		Name:   "test",
		Hidden: false,
	}
	hiddenFlag := hidden(visibleFlag)

	if visibleFlag == hiddenFlag {
		t.Error("hidden should return a new flag")
	}
	if visibleFlag.Hidden {
		t.Error("hidden should not mutate the original flag")
	}
	if !hiddenFlag.Hidden {
		t.Error("hidden should return a new flag with Hidden set to true")
	}
}
