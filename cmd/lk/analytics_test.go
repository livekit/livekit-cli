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
	"encoding/json"
	"testing"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAnalyticsCommandTree(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd, "top-level 'analytics' command must exist")

	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd, "'analytics session' command must exist")

	listCmd := findCommandByName(sessionCmd.Commands, "list")
	require.NotNil(t, listCmd, "'analytics session list' command must exist")
	require.NotNil(t, listCmd.Action, "'analytics session list' must have an action")

	getCmd := findCommandByName(sessionCmd.Commands, "get")
	require.NotNil(t, getCmd, "'analytics session get' command must exist")
	require.NotNil(t, getCmd.Action, "'analytics session get' must have an action")
}

func TestAnalyticsCommandRequiresExperimentalFlag(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd, "top-level 'analytics' command must exist")

	experimental := findFlagByName(analyticsCmd.Flags, "experimental")
	require.NotNil(t, experimental, "'analytics' command must declare an --experimental flag")

	boolFlag, ok := experimental.(*cli.BoolFlag)
	require.True(t, ok, "--experimental must be a bool flag")
	assert.True(t, boolFlag.Required, "--experimental flag must be required")
}

// runAnalytics runs the analytics command tree in isolation (as a subcommand of
// a bare root) so flag validation is exercised without touching global CLI state.
func runAnalytics(args ...string) error {
	app := &cli.Command{Name: "lk", Commands: AnalyticsCommands}
	return app.Run(context.Background(), append([]string{"lk", "analytics"}, args...))
}

func TestAnalyticsFailsWithoutExperimentalFlag(t *testing.T) {
	// Every leaf must reject invocation when --experimental is omitted, before
	// any action (and its network calls) runs.
	err := runAnalytics("session", "list")
	require.Error(t, err, "'analytics session list' must fail without --experimental")
	assert.Contains(t, err.Error(), `Required flag "experimental" not set`)

	err = runAnalytics("session", "get", "sess_123")
	require.Error(t, err, "'analytics session get' must fail without --experimental")
	assert.Contains(t, err.Error(), `Required flag "experimental" not set`)
}

func TestAnalyticsPassesFlagValidationWithExperimentalFlag(t *testing.T) {
	// With --experimental set, flag validation passes; any resulting error comes
	// from the action itself (e.g. project resolution), not the required flag.
	err := runAnalytics("--experimental", "session", "list")
	if err != nil {
		assert.NotContains(t, err.Error(), `Required flag "experimental" not set`)
	}
}

func TestValidateAnalyticsDateRange(t *testing.T) {
	start, end, err := validateAnalyticsDateRange("2026-03-01", "2026-03-09")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), end)

	_, _, err = validateAnalyticsDateRange("2026-03-10", "2026-03-09")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start date must be less than or equal to end date")

	_, _, err = validateAnalyticsDateRange("03-01-2026", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start date")

	_, _, err = validateAnalyticsDateRange("", "03-09-2026")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid end date")
}

func TestMapAnalyticsHTTPError(t *testing.T) {
	err := mapAnalyticsHTTPError(401, "scale plan or higher is required")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Scale plan or higher")

	err = mapAnalyticsHTTPError(403, "forbidden")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")

	err = mapAnalyticsHTTPError(500, "internal error")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestRawJSONToString(t *testing.T) {
	val := json.RawMessage(`"1234"`)
	assert.Equal(t, "1234", rawJSONToString(val))

	val = json.RawMessage(`1234`)
	assert.Equal(t, "1234", rawJSONToString(val))

	val = json.RawMessage(``)
	assert.Equal(t, "-", rawJSONToString(val))
}

func TestBuildAnalyticsListQueryUsesDefaultLimit(t *testing.T) {
	var queryLimit string
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "limit", Value: defaultAnalyticsLimit},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			query, err := buildAnalyticsListQuery(cmd)
			queryLimit = query.Get("limit")
			return err
		},
	}

	require.NoError(t, cmd.Run(context.Background(), []string{"analytics-list"}))
	assert.Equal(t, "10", queryLimit)
}

func TestFormatBytes(t *testing.T) {
	assert.Equal(t, "999 B", formatBytes(json.RawMessage(`999`)))
	assert.Equal(t, "1.2 KB", formatBytes(json.RawMessage(`1234`)))
	assert.Equal(t, "1.3 MB", formatBytes(json.RawMessage(`1260393`)))
	assert.Equal(t, "-", formatBytes(nil))
	assert.Equal(t, "unknown", formatBytes(json.RawMessage(`"unknown"`)))
}

func TestResolveAnalyticsProjectID(t *testing.T) {
	originalProject := project
	defer func() {
		project = originalProject
	}()

	project = &config.ProjectConfig{Name: "staging", ProjectId: "p_123"}
	projectID, err := resolveAnalyticsProjectID()
	require.NoError(t, err)
	assert.Equal(t, "p_123", projectID)

	project = &config.ProjectConfig{Name: "staging", ProjectId: ""}
	_, err = resolveAnalyticsProjectID()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selected project [staging] is missing project_id")
	assert.Contains(t, err.Error(), "Select a cloud project via --project or run `lk cloud auth`")
}
