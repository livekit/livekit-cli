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
	"encoding/json"
	"testing"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyticsCommandTree(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd, "top-level 'analytics' command must exist")

	listCmd := findCommandByName(analyticsCmd.Commands, "list")
	require.NotNil(t, listCmd, "'analytics list' command must exist")
	require.NotNil(t, listCmd.Action, "'analytics list' must have an action")

	getCmd := findCommandByName(analyticsCmd.Commands, "get")
	require.NotNil(t, getCmd, "'analytics get' command must exist")
	require.NotNil(t, getCmd.Action, "'analytics get' must have an action")
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
