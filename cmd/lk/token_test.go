package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
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

func TestTokenOutputFlagsAreMutuallyExclusive(t *testing.T) {
	var actionCalled bool
	app := &cli.Command{
		Name:                   "lk",
		MutuallyExclusiveFlags: tokenOutputMutuallyExclusiveFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			actionCalled = true
			return nil
		},
	}

	err := app.Run(context.Background(), []string{"lk", "--json", "--token-only"})
	require.Error(t, err)
	assert.False(t, actionCalled)
	assert.Contains(t, err.Error(), "option json cannot be set along with option token-only")
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

	var stdout bytes.Buffer
	err := printTokenCreateOutput(&stdout, true, false, out)
	require.NoError(t, err)
	assert.Equal(t, "token-value\n", stdout.String())

	stdout.Reset()
	err = printTokenCreateOutput(&stdout, false, true, out)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	assert.Equal(t, "token-value", decoded["access_token"])
	assert.Equal(t, "https://example.livekit.cloud", decoded["project_url"])
	assert.Equal(t, "test-id", decoded["identity"])

	stdout.Reset()
	err = printTokenCreateOutput(&stdout, false, false, out)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Token grants:")
	assert.Contains(t, stdout.String(), "Project URL: https://example.livekit.cloud")
	assert.Contains(t, stdout.String(), "Access token: token-value")
}

func commandHasFlag(cmd *cli.Command, flagName string) bool {
	for _, flag := range commandFlags(cmd) {
		if slicesContains(flag.Names(), flagName) {
			return true
		}
	}
	return false
}

func commandFlags(cmd *cli.Command) []cli.Flag {
	flags := append([]cli.Flag{}, cmd.Flags...)
	for _, group := range cmd.MutuallyExclusiveFlags {
		for _, path := range group.Flags {
			flags = append(flags, path...)
		}
	}
	return flags
}

func slicesContains(items []string, item string) bool {
	for _, current := range items {
		if strings.EqualFold(current, item) {
			return true
		}
	}
	return false
}

// collectAttributeCommands returns every command in the tree that defines an "attribute" string slice flag.
func collectAttributeCommands(commands []*cli.Command) []*cli.Command {
	var found []*cli.Command
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		for _, flag := range cmd.Flags {
			if f, ok := flag.(*cli.StringSliceFlag); ok && f.Name == "attribute" {
				found = append(found, cmd)
				break
			}
		}
		found = append(found, collectAttributeCommands(cmd.Commands)...)
	}
	return found
}

func TestAttributeFlagKeepsCommasInValues(t *testing.T) {
	var cmds []*cli.Command
	cmds = append(cmds, collectAttributeCommands(TokenCommands)...)
	cmds = append(cmds, collectAttributeCommands(RoomCommands)...)
	cmds = append(cmds, collectAttributeCommands(PerfCommands)...)
	require.NotEmpty(t, cmds)

	for _, c := range cmds {
		t.Run(c.Name, func(t *testing.T) {
			var got map[string]string
			var parseErr error
			// Run the real command's flags and slice separator setting with a stand-in action.
			cmd := &cli.Command{
				Name:                      c.Name,
				Flags:                     c.Flags,
				DisableSliceFlagSeparator: c.DisableSliceFlagSeparator,
				Action: func(_ context.Context, cmd *cli.Command) error {
					got, parseErr = parseKeyValuePairs(cmd, "attribute")
					return nil
				},
			}
			err := cmd.Run(context.Background(), []string{c.Name, "--attribute", "tags=a,b", "--attribute", "mode=x"})
			require.NoError(t, err)
			require.NoError(t, parseErr)
			assert.Equal(t, map[string]string{"tags": "a,b", "mode": "x"}, got)
		})
	}
}

func TestParseAllowSources(t *testing.T) {
	sources, err := parseAllowSources([]string{"camera,microphone", "screen_share"})
	require.NoError(t, err)
	assert.Equal(t, []livekit.TrackSource{
		livekit.TrackSource_CAMERA,
		livekit.TrackSource_MICROPHONE,
		livekit.TrackSource_SCREEN_SHARE,
	}, sources)

	_, err = parseAllowSources([]string{"camera,video"})
	require.Error(t, err)
}
