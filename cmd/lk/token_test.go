package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/livekit/protocol/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestTokenCommandTree(t *testing.T) {
	tokenCmd := findCommandByName(TokenCommands, "token")
	require.NotNil(t, tokenCmd, "top-level 'token' command must exist")

	createCmd := findCommandByName(tokenCmd.Commands, "create")
	require.NotNil(t, createCmd, "'token create' command must exist")
	require.NotNil(t, createCmd.Action, "'token create' must have an action")
	assert.True(t, commandHasFlag(createCmd, "json"), "'token create' must have --json")
	assert.True(t, commandHasFlag(createCmd, "token-only"), "'token create' must have --token-only")

	deprecatedCreateCmd := findCommandByName(TokenCommands, "create-token")
	require.NotNil(t, deprecatedCreateCmd, "deprecated 'create-token' command must exist")
	assert.True(t, commandHasFlag(deprecatedCreateCmd, "json"), "'create-token' must have --json")
	assert.True(t, commandHasFlag(deprecatedCreateCmd, "token-only"), "'create-token' must have --token-only")
}

func TestResolveTokenCreateOutputMode(t *testing.T) {
	cmd := parseTokenOutputFlags(t)
	mode, err := resolveTokenCreateOutputMode(cmd)
	require.NoError(t, err)
	assert.Equal(t, tokenOutputModeHuman, mode)

	cmd = parseTokenOutputFlags(t, "--json")
	mode, err = resolveTokenCreateOutputMode(cmd)
	require.NoError(t, err)
	assert.Equal(t, tokenOutputModeJSON, mode)

	cmd = parseTokenOutputFlags(t, "--token-only")
	mode, err = resolveTokenCreateOutputMode(cmd)
	require.NoError(t, err)
	assert.Equal(t, tokenOutputModeTokenOnly, mode)

	cmd = parseTokenOutputFlags(t, "--json", "--token-only")
	_, err = resolveTokenCreateOutputMode(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine --json and --token-only")
}

func TestPrintTokenCreateOutput(t *testing.T) {
	out := tokenCreateOutput{
		AccessToken: "token-value",
		ProjectURL:  "https://example.livekit.cloud",
		Identity:    "test-id",
		Name:        "test-name",
		Room:        "test-room",
		Grants:      &auth.ClaimGrants{Identity: "test-id"},
	}

	stdout := captureStdout(t, func() {
		err := printTokenCreateOutput(tokenOutputModeTokenOnly, out)
		require.NoError(t, err)
	})
	assert.Equal(t, "token-value\n", stdout)

	stdout = captureStdout(t, func() {
		err := printTokenCreateOutput(tokenOutputModeJSON, out)
		require.NoError(t, err)
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded))
	assert.Equal(t, "token-value", decoded["access_token"])
	assert.Equal(t, "https://example.livekit.cloud", decoded["project_url"])
	assert.Equal(t, "test-id", decoded["identity"])

	stdout = captureStdout(t, func() {
		err := printTokenCreateOutput(tokenOutputModeHuman, out)
		require.NoError(t, err)
	})
	assert.Contains(t, stdout, "Token grants:")
	assert.Contains(t, stdout, "Project URL: https://example.livekit.cloud")
	assert.Contains(t, stdout, "Access token: token-value")
}

func commandHasFlag(cmd *cli.Command, flagName string) bool {
	for _, flag := range cmd.Flags {
		if slicesContains(flag.Names(), flagName) {
			return true
		}
	}
	return false
}

func parseTokenOutputFlags(t *testing.T, args ...string) *cli.Command {
	t.Helper()

	var parsedCmd *cli.Command
	app := &cli.Command{
		Name:  "lk",
		Flags: []cli.Flag{jsonFlag, tokenOnlyFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			parsedCmd = cmd
			return nil
		},
	}

	runArgs := append([]string{"lk"}, args...)
	require.NoError(t, app.Run(context.Background(), runArgs))
	require.NotNil(t, parsedCmd)
	return parsedCmd
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	defer func() {
		os.Stdout = originalStdout
	}()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(out)
}

func slicesContains(items []string, item string) bool {
	for _, current := range items {
		if strings.EqualFold(current, item) {
			return true
		}
	}
	return false
}
