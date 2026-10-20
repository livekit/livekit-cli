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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/util"
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

	participantCmd := findCommandByName(sessionCmd.Commands, "participant")
	require.NotNil(t, participantCmd, "'analytics session participant' command must exist")
	participantListCmd := findCommandByName(participantCmd.Commands, "list")
	require.NotNil(t, participantListCmd, "'analytics session participant list' command must exist")
	require.NotNil(t, participantListCmd.Action, "'analytics session participant list' must have an action")
	cursor := findFlagByName(participantListCmd.Flags, "cursor")
	require.NotNil(t, cursor, "'analytics session participant list' must declare --cursor")
	assert.True(t, cursor.(*cli.StringFlag).Hidden, "--cursor must be hidden")
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

func TestAnalyticsFailsWithExperimentalFlagDisabled(t *testing.T) {
	// Required only checks presence; an explicit --experimental=false must still
	// be rejected by the flag's Validator rather than running the command.
	err := runAnalytics("--experimental=false", "session", "list")
	require.Error(t, err, "'analytics session list' must fail when --experimental is disabled")
	assert.Contains(t, err.Error(), "--experimental must be enabled")
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
	assert.Contains(t, err.Error(), "start date must be before end date")

	_, _, err = validateAnalyticsDateRange("2026-03-09", "2026-03-09")
	require.Error(t, err, "equal dates are an empty window")
	assert.Contains(t, err.Error(), "start date must be before end date")

	_, _, err = validateAnalyticsDateRange("", "2026-03-09")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--end requires --start")

	start, end, err = validateAnalyticsDateRange("2026-03-01", "")
	require.NoError(t, err, "--start alone leaves the end open")
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), start)
	assert.True(t, end.IsZero())

	_, _, err = validateAnalyticsDateRange("03-01-2026", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start date")

	_, _, err = validateAnalyticsDateRange("2026-03-01", "03-09-2026")
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

// sessionListCmdOptions runs sessionListOptions with the given arguments on a
// command built from fresh analyticsSessionListFlags.
func sessionListCmdOptions(t *testing.T, args ...string) (public.SessionListOptions, error) {
	t.Helper()
	var opts public.SessionListOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "list",
		Flags: analyticsSessionListFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = sessionListOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"list"}, args...)))
	return opts, optsErr
}

func TestSessionListOptions(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)
	origNow := analyticsNow
	analyticsNow = func() time.Time { return now }
	t.Cleanup(func() { analyticsNow = origNow })

	tests := []struct {
		name    string
		args    []string
		want    public.SessionListOptions
		wantErr string
	}{
		{
			name: "defaults to the last 14 days",
			want: public.SessionListOptions{
				Limit:    defaultAnalyticsLimit,
				Start:    now.AddDate(0, 0, -14),
				Statuses: []string{},
				Tags:     []string{},
			},
		},
		{
			name: "dates are UTC midnight, end exclusive",
			args: []string{"--start", "2026-10-01", "--end", "2026-10-03"},
			want: public.SessionListOptions{
				Limit:    defaultAnalyticsLimit,
				Start:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
				End:      time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
				Statuses: []string{},
				Tags:     []string{},
			},
		},
		{
			name: "filters and sort",
			args: []string{
				"--limit", "25", "--cursor", "abc",
				"--status", "active", "--status", "closed",
				"--room", "demo-", "--tag", "a", "--tag", "b",
				"--sort-order", "asc",
			},
			want: public.SessionListOptions{
				Limit:      25,
				Cursor:     "abc",
				Statuses:   []string{"active", "closed"},
				RoomPrefix: "demo-",
				Tags:       []string{"a", "b"},
				SortOrder:  "asc",
			},
		},
		{
			name: "since counts back from now, to the second",
			args: []string{"--since", "14d"},
			want: public.SessionListOptions{
				Limit:    defaultAnalyticsLimit,
				Start:    now.AddDate(0, 0, -14),
				Statuses: []string{},
				Tags:     []string{},
			},
		},
		{
			name:    "since with start",
			args:    []string{"--since", "14d", "--start", "2026-10-01"},
			wantErr: "--since can't be used with --start or --end",
		},
		{
			name:    "bad since",
			args:    []string{"--since", "two weeks"},
			wantErr: `invalid duration "two weeks"`,
		},
		{
			name:    "equal dates are an empty window",
			args:    []string{"--start", "2026-10-01", "--end", "2026-10-01"},
			wantErr: "start date must be before end date",
		},
		{
			name:    "start after end",
			args:    []string{"--start", "2026-10-02", "--end", "2026-10-01"},
			wantErr: "start date must be before end date",
		},
		{
			name:    "unknown status",
			args:    []string{"--status", "open"},
			wantErr: `invalid session status "open"`,
		},
		{
			name:    "unknown sort order",
			args:    []string{"--sort-order", "newest"},
			wantErr: `invalid sort order "newest"`,
		},
		{
			name:    "end without start",
			args:    []string{"--end", "2026-10-01"},
			wantErr: "--end requires --start",
		},
		{
			name:    "bad date",
			args:    []string{"--start", "10/01/2026"},
			wantErr: "invalid start date",
		},
		{
			name:    "non-positive limit",
			args:    []string{"--limit", "0"},
			wantErr: "limit must be greater than 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := sessionListCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

func TestParseLookback(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"14d", 14 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"6h", 6 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{" 1d ", 24 * time.Hour},
	}
	for _, tt := range tests {
		got, err := parseLookback(tt.in)
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.want, got, tt.in)
	}
	for _, bad := range []string{"", "d", "0d", "-1d", "1.5d", "-2h", "0s", "14", "14days", "1y"} {
		_, err := parseLookback(bad)
		assert.Error(t, err, bad)
	}
}

// TestBuildAnalyticsListQuerySince checks that the API-key endpoint, which
// reads whole UTC days, gets --since as the date the window begins.
func TestBuildAnalyticsListQuerySince(t *testing.T) {
	origNow := analyticsNow
	analyticsNow = func() time.Time { return time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { analyticsNow = origNow })

	var query url.Values
	cmd := &cli.Command{
		Name:  "list",
		Flags: analyticsSessionListFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error
			query, err = buildAnalyticsListQuery(cmd)
			return err
		},
	}
	require.NoError(t, cmd.Run(context.Background(), []string{"list", "--since", "6h"}))
	assert.Equal(t, "2026-10-13", query.Get("start"))
	assert.Empty(t, query.Get("end"))
}

// TestBuildAnalyticsListQueryDefaultWindow checks that the API-key endpoint
// gets the default window as the date it begins.
func TestBuildAnalyticsListQueryDefaultWindow(t *testing.T) {
	origNow := analyticsNow
	analyticsNow = func() time.Time { return time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { analyticsNow = origNow })

	var query url.Values
	cmd := &cli.Command{
		Name:  "list",
		Flags: analyticsSessionListFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error
			query, err = buildAnalyticsListQuery(cmd)
			return err
		},
	}
	require.NoError(t, cmd.Run(context.Background(), []string{"list"}))
	assert.Equal(t, "2026-09-30", query.Get("start"))
	assert.Empty(t, query.Get("end"))
}

// TestUsesDefaultWindow checks when a listing searches the default window, and
// so when an empty one gets the hint that only the last 14 days were searched:
// when no --since, --start, --end or cursor sets the window.
func TestUsesDefaultWindow(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"--limit", "5", "--status", "active"}, true},
		{[]string{"--since", "2d"}, false},
		{[]string{"--start", "2026-10-01"}, false},
		{[]string{"--end", "2026-10-01"}, false},
		// A cursor carries the window of the listing it came from.
		{[]string{"--cursor", "abc"}, false},
	}
	for _, tt := range tests {
		var got bool
		cmd := &cli.Command{
			Name:  "list",
			Flags: analyticsSessionListFlags(),
			Action: func(_ context.Context, cmd *cli.Command) error {
				got = usesDefaultWindow(cmd)
				return nil
			},
		}
		require.NoError(t, cmd.Run(context.Background(), append([]string{"list"}, tt.args...)))
		assert.Equal(t, tt.want, got, tt.args)
	}
}

// validateListModeFlags runs analyticsListModeFlags.validate on a command built
// from fresh analyticsSessionListFlags plus the --experimental-auth selector,
// which production declares on the root.
func validateListModeFlags(t *testing.T, args ...string) error {
	t.Helper()
	var validateErr error
	cmd := &cli.Command{
		Name:  "list",
		Flags: append([]cli.Flag{&cli.BoolFlag{Name: "experimental-auth"}}, analyticsSessionListFlags()...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			validateErr = analyticsListModeFlags.validate(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"list"}, args...)))
	return validateErr
}

func TestAnalyticsListModeFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "dates in API-key mode", args: []string{"--start", "2026-10-01", "--end", "2026-10-03"}},
		{name: "since in API-key mode", args: []string{"--since", "14d"}},
		{name: "since in user session mode", args: []string{"--experimental-auth", "--since", "14d"}},
		{name: "dates in user session mode", args: []string{"--experimental-auth", "--start", "2026-10-01", "--end", "2026-10-03"}},
		{
			name: "filters in user session mode",
			args: []string{"--experimental-auth", "--cursor", "abc", "--status", "active", "--room", "demo-", "--tag", "a", "--sort-order", "asc"},
		},
		{name: "page in user session mode", args: []string{"--experimental-auth", "--page", "1"}, wantErr: "--page is not supported with --experimental-auth"},
		{name: "cursor in API-key mode", args: []string{"--cursor", "abc"}, wantErr: "--cursor is only supported with --experimental-auth"},
		{name: "status in API-key mode", args: []string{"--status", "active"}, wantErr: "--status is only supported with --experimental-auth"},
		{name: "room in API-key mode", args: []string{"--room", "demo-"}, wantErr: "--room is only supported with --experimental-auth"},
		{name: "tag in API-key mode", args: []string{"--tag", "a"}, wantErr: "--tag is only supported with --experimental-auth"},
		{name: "sort order in API-key mode", args: []string{"--sort-order", "asc"}, wantErr: "--sort-order is only supported with --experimental-auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateListModeFlags(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// participantListCmdOptions runs participantListOptions with the given
// arguments on a command built from fresh analyticsParticipantListFlags.
func participantListCmdOptions(t *testing.T, args ...string) (public.ParticipantListOptions, error) {
	t.Helper()
	var opts public.ParticipantListOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "list",
		Flags: analyticsParticipantListFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = participantListOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"list"}, args...)))
	return opts, optsErr
}

func TestParticipantListOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.ParticipantListOptions
		wantErr string
	}{
		{name: "defaults", want: public.ParticipantListOptions{PageOptions: public.PageOptions{Limit: defaultPageLimit}}},
		{
			name: "paging and sort",
			args: []string{"--limit", "25", "--cursor", "abc", "--sort-by", "left", "--sort-order", "asc"},
			want: public.ParticipantListOptions{PageOptions: public.PageOptions{Limit: 25, Cursor: "abc"}, SortBy: "left", SortOrder: "asc"},
		},
		{name: "unknown sort field", args: []string{"--sort-by", "name"}, wantErr: `invalid participant sort "name"`},
		{name: "unknown sort order", args: []string{"--sort-order", "newest"}, wantErr: `invalid sort order "newest"`},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := participantListCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// TestParticipantListRequiresExperimentalAuth checks the participant listing,
// which has no API-key endpoint, refuses to run without --experimental-auth
// before reading its arguments or any config.
func TestParticipantListRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "participant", "list", "RM_1"},
		{"--experimental", "session", "participant", "list"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// participantsAPI starts a stand-in Public API that answers the participant
// list with status and body, and records the query it was asked with.
func participantsAPI(t *testing.T, status int, body string) (*public.Client, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/projects/p1/sessions/RM_1/participants" || r.Header.Get("Authorization") != "Bearer sekret" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &query
}

// TestFetchSessionParticipants checks a page of participants prints as a
// table with their participant sessions and the command that reads the next
// cursor, sending the limit, cursor and order asked for, and --json prints
// {items, pageInfo}.
func TestFetchSessionParticipants(t *testing.T) {
	const page = `{"items":[{"participantIdentity":"alice","region":"US East",` +
		`"sessions":[{"participantSessionId":"PA_alice1","connectionType":"UDP"}]}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	opts := public.ParticipantListOptions{PageOptions: public.PageOptions{Limit: 50, Cursor: "c1"}, SortBy: "left", SortOrder: "asc"}

	client, query := participantsAPI(t, http.StatusOK, page)
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchSessionParticipants(context.Background(), client, "p1", "RM_1", opts, false))
	assert.Equal(t, url.Values{
		"page.pageSize": {"50"}, "page.cursor": {"c1"},
		"sortBy": {"PARTICIPANT_SORT_FIELD_LEFT_AT"}, "sortOrder": {"SORT_ORDER_ASC"},
	}, *query)
	for _, want := range []string{"alice", "US East", "PA_alice1", "UDP"} {
		assert.Contains(t, stdout.String(), want)
	}
	assert.Contains(t, stderr.String(), "More participants available — next page: --cursor c2")

	stdout, _ = captureOut(t)
	require.NoError(t, fetchSessionParticipants(context.Background(), client, "p1", "RM_1", public.ParticipantListOptions{}, true))
	var got struct {
		Items    []map[string]any `json:"items"`
		PageInfo util.PageInfo    `json:"pageInfo"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "alice", got.Items[0]["participantIdentity"])
	assert.Equal(t, "c2", got.PageInfo.NextCursor)
}

// TestFetchSessionParticipantsErrors checks a failed read says why: signed
// out, without access to the project, or no such session in the project.
func TestFetchSessionParticipantsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "signed out", status: http.StatusUnauthorized, body: `{"code":16,"message":"authentication required"}`, wantErr: "lk cloud auth"},
		{name: "permission denied", status: http.StatusForbidden, body: `{"code":7,"message":"permission denied"}`, wantErr: "permission denied — you don't have access to this project"},
		{name: "no such session", status: http.StatusNotFound, body: `{"code":5,"message":"session not found"}`, wantErr: "no session RM_1 in project p1 (session not found)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := participantsAPI(t, tt.status, tt.body)
			stdout, _ := captureOut(t)
			err := fetchSessionParticipants(context.Background(), client, "p1", "RM_1", public.ParticipantListOptions{}, false)
			require.ErrorContains(t, err, tt.wantErr)
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSessionRecordingCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	recordingCmd := findCommandByName(sessionCmd.Commands, "recording")
	require.NotNil(t, recordingCmd, "'analytics session recording' command must exist")
	require.NotNil(t, recordingCmd.Action)
	for _, name := range []string{"type", "output", "json"} {
		assert.NotNil(t, findFlagByName(recordingCmd.Flags, name), "--%s", name)
	}
}

// TestSessionRecordingRequiresExperimentalAuth checks the recording download,
// which has no API-key endpoint, refuses to run without --experimental-auth
// before reading its arguments or any config.
func TestSessionRecordingRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "recording", "RM_1", "--type", "audio"},
		{"--experimental", "session", "recording", "RM_1", "--type", "bogus"},
		{"--experimental", "session", "recording"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// sessionRecordingCmdOptions runs sessionRecordingOptionsFrom with the given
// arguments on a command built from fresh analyticsRecordingFlags.
func sessionRecordingCmdOptions(t *testing.T, args ...string) (sessionRecordingOptions, error) {
	t.Helper()
	var opts sessionRecordingOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "recording",
		Flags: append([]cli.Flag{&cli.BoolFlag{Name: "json"}}, analyticsRecordingFlags()...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = sessionRecordingOptionsFrom(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"recording", "RM_1"}, args...)))
	return opts, optsErr
}

func TestSessionRecordingOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    sessionRecordingOptions
		wantErr string
	}{
		{
			name: "audio",
			args: []string{"--type", "audio"},
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}},
		},
		{
			name: "chat history to -o",
			args: []string{"--type", "chat-history", "-o", "chat.json"},
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}, Output: "chat.json"},
		},
		{name: "a second argument", args: []string{"a.ogg"}, wantErr: `unexpected argument "a.ogg"; download to a file with -o FILE`},
		{name: "json saves nothing", args: []string{"--json", "-o", "a.ogg"}, wantErr: "--output can't be used with --json"},
		{
			name: "json alone",
			args: []string{"--json"},
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}},
		},
		{
			name: "audio by default",
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}},
		},
		{name: "blank type", args: []string{"--type", ""}, wantErr: `recording type is required ("audio" or "chat-history")`},
		{name: "unknown type", args: []string{"--type", "transcript"}, wantErr: `invalid recording type "transcript"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := sessionRecordingCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// recordingServers starts a stand-in object store serving a session's audio
// and gzip-encoded chat history at signed URLs, and a stand-in Public API that
// signs those URLs. Requests to the object store are recorded so a test can
// check no credentials reach it.
func recordingServers(t *testing.T, audio []byte, chatHistory string) (api *public.Client, storeAuth *[]string) {
	t.Helper()
	var auth []string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/recording.ogg":
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write(audio)
		case "/chat_history.json":
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write([]byte(chatHistory))
			_ = zw.Close()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(store.Close)

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/projects/p1/sessions/RM_1/recording-url" || r.Header.Get("Authorization") != "Bearer sekret" {
			http.NotFound(w, r)
			return
		}
		object := "/recording.ogg"
		if r.URL.Query().Get("fileType") == "RECORDING_FILE_TYPE_CHAT_HISTORY" {
			object = "/chat_history.json"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"url":%q,"expiresAt":"2026-10-07T12:15:00Z","recordingStartedAt":"2026-10-07T11:00:00Z"}`,
			store.URL+object+"?X-Amz-Signature=abc")
	}))
	t.Cleanup(apiSrv.Close)

	client, err := public.New(apiSrv.URL, "sekret")
	require.NoError(t, err)
	return client, &auth
}

// captureOut points the command printer at buffers for one test.
func captureOut(t *testing.T) (stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	prev := out
	out = util.NewPrinter(stdout, stderr, false)
	t.Cleanup(func() { out = prev })
	return stdout, stderr
}

// TestFetchSessionRecording downloads each recording from a signed URL the
// stand-in API hands out, to the default name or -o, and checks the chat
// history lands decompressed.
func TestFetchSessionRecording(t *testing.T) {
	audio := []byte("OggS\x00\x02audio-bytes")
	const chat = `{"items":[{"type":"message","role":"user","content":["hi"]}]}`

	tests := []struct {
		name     string
		opts     sessionRecordingOptions
		wantFile string
		want     string
	}{
		{
			name:     "audio into -o ., under the default name",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, Output: "."},
			wantFile: "RM_1-audio.ogg",
			want:     string(audio),
		},
		{
			name:     "chat history into -o ., under the default name, decompressed",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}, Output: "."},
			wantFile: "RM_1-chat-history.json",
			want:     chat,
		},
		{
			name:     "chat history to -o",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}, Output: "out/chat.json"},
			wantFile: "out/chat.json",
			want:     chat,
		},
		{
			name:     "audio into a directory -o names, under the default name",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, Output: "out"},
			wantFile: filepath.Join("out", "RM_1-audio.ogg"),
			want:     string(audio),
		},
		{
			name:     "audio into -o ending in a separator, as a directory",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, Output: "out/"},
			wantFile: filepath.Join("out", "RM_1-audio.ogg"),
			want:     string(audio),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir("out", 0o755))
			client, storeAuth := recordingServers(t, audio, chat)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", tt.opts, false))

			got, err := os.ReadFile(tt.wantFile)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.Regexp(t, `^http://127\.0\.0\.1:\d+/\S+\?X-Amz-Signature=abc\n$`, stdout.String(), "stdout is the signed URL")
			assert.Contains(t, stderr.String(), "of session RM_1 to "+tt.wantFile, "stderr names the path saved to")
			assert.Contains(t, stderr.String(), "The recording started")
			assert.Equal(t, []string{""}, *storeAuth, "the object store gets the signed URL and nothing else")

			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(tt.wantFile), ".*"))
			require.NoError(t, err)
			assert.Empty(t, leftovers, "no partial file is left behind")
		})
	}
}

// TestFetchSessionRecordingURL checks that without -o the command prints the
// signed URL and downloads nothing.
func TestFetchSessionRecordingURL(t *testing.T) {
	t.Chdir(t.TempDir())
	client, storeAuth := recordingServers(t, []byte("OggS"), "{}")
	stdout, stderr := captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}}
	require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false))

	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/recording\.ogg\?X-Amz-Signature=abc\n$`, stdout.String())
	assert.Contains(t, stderr.String(), "The URL expires")
	assert.Empty(t, *storeAuth, "nothing is downloaded")
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestFetchSessionRecordingJSON checks --json prints the API's response and
// downloads nothing.
func TestFetchSessionRecordingJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	client, storeAuth := recordingServers(t, []byte("OggS"), "{}")
	stdout, stderr := captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}}
	require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, true))
	assert.Empty(t, *storeAuth, "nothing is downloaded")

	// stdout is the API's response, the signed URL; nothing is saved.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &resp))
	assert.Contains(t, resp["url"], "/recording.ogg?X-Amz-Signature=abc")
	assert.Equal(t, "2026-10-07T12:15:00Z", resp["expiresAt"])
	assert.Equal(t, "2026-10-07T11:00:00Z", resp["recordingStartedAt"])
	assert.Len(t, resp, 3)
	assert.NotContains(t, stderr.String(), "Saved")
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestFetchSessionRecordingNothingToDownload checks the two answers that mean
// there's no recording each say so plainly, and leave no file behind.
func TestFetchSessionRecordingNothingToDownload(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "not recorded",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"recording not found"}`,
			wantErr: []string{"session RM_1 has no chat history", "recording not found", "wasn't recorded", "less than a minute ago", "or there is no such session in project p1"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{
				"session RM_1 has no chat history",
				"user data recording is off for this project",
				"https://cloud.example/projects/p1/settings/observability",
			},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "reading a session's chat history requires being a project admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			client, err := public.New(srv.URL, "sekret")
			require.NoError(t, err)
			captureOut(t)

			opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}}
			err = fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}

// TestFetchSessionRecordingDownloadFails checks a refused signed URL leaves no
// file, not even a partial one, and keeps an existing file intact.
func TestFetchSessionRecordingDownloadFails(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("RM_1-audio.ogg", []byte("earlier"), 0o600))
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Message>Request has expired</Message></Error>"))
	}))
	t.Cleanup(store.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"url":%q}`, store.URL+"/recording.ogg")
	}))
	t.Cleanup(api.Close)
	client, err := public.New(api.URL, "sekret")
	require.NoError(t, err)
	captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, Output: "RM_1-audio.ogg"}
	err = fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false)
	require.ErrorContains(t, err, "Request has expired")

	got, err := os.ReadFile("RM_1-audio.ogg")
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(got))
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	// Under the default name, the numbered name it claimed is let go.
	opts.Output = "."
	err = fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false)
	require.ErrorContains(t, err, "Request has expired")
	entries, err = os.ReadDir(".")
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// TestFetchSessionRecordingExistingFile checks a download never replaces a
// file at the default name, failing before it asks for a URL, and replaces
// one -o names.
func TestFetchSessionRecordingExistingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("RM_1-audio.ogg", []byte("earlier"), 0o600))
	client, _ := recordingServers(t, []byte("OggS"), "{}")

	// The default name is numbered rather than replacing a file.
	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, Output: "."}
	for _, want := range []string{"RM_1-audio-1.ogg", "RM_1-audio-2.ogg"} {
		_, stderr := captureOut(t)
		require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false))
		assert.Contains(t, stderr.String(), "to "+want+" (")
		got, err := os.ReadFile(want)
		require.NoError(t, err)
		assert.Equal(t, "OggS", string(got))
	}
	got, err := os.ReadFile("RM_1-audio.ogg")
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(got))

	// A file -o names is replaced.
	captureOut(t)
	opts.Output = "RM_1-audio.ogg"
	require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false))
	got, err = os.ReadFile("RM_1-audio.ogg")
	require.NoError(t, err)
	assert.Equal(t, "OggS", string(got))
}

func TestDefaultRecordingFile(t *testing.T) {
	assert.Equal(t, "RM_1-audio.ogg", defaultRecordingFile("RM_1", "audio"))
	assert.Equal(t, "RM_1-chat-history.json", defaultRecordingFile("RM_1", "chat-history"))
	assert.Equal(t, "a_b-audio.ogg", defaultRecordingFile("a/b", "audio"), "a session id never names a directory")
}

func TestSessionTranscriptCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	transcriptCmd := findCommandByName(sessionCmd.Commands, "transcript")
	require.NotNil(t, transcriptCmd, "'analytics session transcript' command must exist")
	require.NotNil(t, transcriptCmd.Action)
	for _, name := range []string{"limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(transcriptCmd.Flags, name), "--%s", name)
	}
}

// TestSessionTranscriptRequiresExperimentalAuth checks the transcript, which
// has no API-key endpoint, refuses to run without --experimental-auth before
// reading its arguments or any config.
func TestSessionTranscriptRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "transcript", "RM_1"},
		{"--experimental", "session", "transcript", "RM_1", "--limit", "0"},
		{"--experimental", "session", "transcript"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// transcriptCmdOptions runs pageOptions with the given arguments on a
// command built from fresh analyticsTranscriptFlags.
func transcriptCmdOptions(t *testing.T, args ...string) (public.PageOptions, error) {
	t.Helper()
	var opts public.PageOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "transcript",
		Flags: analyticsTranscriptFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = pageOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"transcript"}, args...)))
	return opts, optsErr
}

func TestTranscriptOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.PageOptions
		wantErr string
	}{
		{name: "defaults", want: public.PageOptions{}},
		{name: "paging", args: []string{"--limit", "100", "--cursor", "abc"}, want: public.PageOptions{Limit: 100, Cursor: "abc"}},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := transcriptCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// transcriptAPI starts a stand-in Public API that answers the transcript read
// with transcript and GetSession with session, the session's JSON. Each path
// it is asked for is recorded.
func transcriptAPI(t *testing.T, transcriptStatus int, transcript, session string) (*public.Client, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer sekret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/projects/p1/sessions/RM_1/transcript":
			w.WriteHeader(transcriptStatus)
			_, _ = w.Write([]byte(transcript))
		case "/v0/projects/p1/sessions/RM_1":
			_, _ = fmt.Fprintf(w, `{"session":%s}`, session)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &paths
}

// TestReadPages checks a read takes full pages but no more items than it has
// left, follows the cursor to the last page or its limit, keeps going past a
// page with no items, and stops on a cursor that doesn't move.
func TestReadPages(t *testing.T) {
	type call struct {
		limit  int32
		cursor string
	}
	pages := map[string]struct {
		items []int
		next  string
	}{
		"":   {[]int{1, 2}, "c2"},
		"c2": {nil, "c3"}, // every record skipped, say
		"c3": {[]int{3}, ""},
		"s1": {[]int{9}, "s1"},
	}
	fetch := func(calls *[]call) func(public.PageOptions) ([]int, string, error) {
		return func(o public.PageOptions) ([]int, string, error) {
			*calls = append(*calls, call{o.Limit, o.Cursor})
			p := pages[o.Cursor]
			return p.items, p.next, nil
		}
	}

	// No limit reads one page and sends no page size: the server picks it.
	var calls []call
	items, next, err := readPages(public.PageOptions{}, fetch(&calls))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, items)
	assert.Equal(t, "c2", next)
	assert.Equal(t, []call{{0, ""}}, calls)

	calls = nil
	items, next, err = readPages(public.PageOptions{Limit: 1000}, fetch(&calls))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, items)
	assert.Empty(t, next)
	assert.Equal(t, []call{{1000, ""}, {998, "c2"}, {998, "c3"}}, calls)

	calls = nil
	items, next, err = readPages(public.PageOptions{Limit: 3}, fetch(&calls))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, items)
	assert.Empty(t, next)
	assert.Equal(t, []call{{3, ""}, {1, "c2"}, {1, "c3"}}, calls)

	calls = nil
	items, next, err = readPages(public.PageOptions{Limit: 2}, fetch(&calls))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, items)
	assert.Equal(t, "c2", next)
	assert.Len(t, calls, 1)

	calls = nil
	items, next, err = readPages(public.PageOptions{Limit: 1000, Cursor: "s1"}, fetch(&calls))
	require.NoError(t, err)
	assert.Equal(t, []int{9}, items)
	assert.Equal(t, "s1", next)
	assert.Len(t, calls, 1)

	_, _, err = readPages(public.PageOptions{Limit: 10}, func(public.PageOptions) ([]int, string, error) {
		return nil, "", errors.New("boom")
	})
	require.EqualError(t, err, "boom")
}

// TestFetchSessionTranscriptPages checks the transcript reads every page into
// one transcript, adding up the records each page left out.
func TestFetchSessionTranscriptPages(t *testing.T) {
	pages := map[string]string{
		"": `{"items":[{"id":"item_1","message":{"role":"ROLE_USER","text":"hi"}}],` +
			`"skippedRecords":1,"pageInfo":{"nextCursor":"c2","hasMore":true}}`,
		"c2": `{"items":[{"id":"item_2","message":{"role":"ROLE_AGENT","text":"hello"}}],"skippedRecords":2}`,
	}
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("page.cursor"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[r.URL.Query().Get("page.cursor")]))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	stdout, stderr := captureOut(t)

	require.NoError(t, fetchSessionTranscript(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 1000}, false))
	assert.Equal(t, []string{"", "c2"}, cursors)
	plain := ansiEscapeRe.ReplaceAllString(stdout.String(), "")
	assert.Contains(t, plain, "● User")
	assert.Contains(t, plain, "● Agent")
	assert.Contains(t, stderr.String(), "3 records couldn't be read as transcript items")
	assert.NotContains(t, stderr.String(), "More items available")
}

// TestFetchSessionTranscript prints a page one line per item and, when the
// first page is empty, says why: the session is still active, or it ended
// without a transcript to read yet. A later empty page needs no lookup, and a
// page with no items but skipped records or a next cursor isn't empty.
func TestFetchSessionTranscript(t *testing.T) {
	const page = `{"items":[` +
		`{"id":"item_1","message":{"role":"ROLE_USER","text":"hi","endOfTurnDelayMs":320}},` +
		`{"id":"item_2","message":{"role":"ROLE_AGENT","text":"hello","e2eLatencyMs":820}}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	tests := []struct {
		name       string
		body       string
		session    string
		opts       public.PageOptions
		wantOut    []string
		wantStatus []string
		noStatus   []string
		wantPaths  []string
	}{
		{
			name:       "items",
			body:       page,
			wantOut:    []string{"● User\n    hi\n    ⏱ end_of_turn 320ms", "● Agent\n    hello\n    ⏱ e2e 820ms"},
			wantStatus: []string{"More items available — next page: --cursor c2"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript"},
		},
		{
			name:       "active session",
			body:       `{"items":[]}`,
			session:    `{"sessionId":"RM_1","status":"SESSION_STATUS_ACTIVE"}`,
			wantStatus: []string{"Session RM_1 is still active", "appears after it ends"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript", "/v0/projects/p1/sessions/RM_1"},
		},
		{
			name:       "closed session",
			body:       `{}`,
			session:    `{"sessionId":"RM_1","status":"SESSION_STATUS_CLOSED"}`,
			wantStatus: []string{"Session RM_1 has no transcript to read", "a minute or two after the session ends"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript", "/v0/projects/p1/sessions/RM_1"},
		},
		{
			// A session that stopped reporting stays ACTIVE but has an end time.
			name:       "active session that stopped reporting",
			body:       `{"items":[]}`,
			session:    `{"sessionId":"RM_1","status":"SESSION_STATUS_ACTIVE","endedAt":"2026-10-07T11:05:00Z"}`,
			wantStatus: []string{"Session RM_1 has no transcript to read"},
			noStatus:   []string{"still active"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript", "/v0/projects/p1/sessions/RM_1"},
		},
		{
			name:       "last page",
			body:       `{"items":[]}`,
			opts:       public.PageOptions{Cursor: "c2"},
			wantStatus: []string{"No more transcript items"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript"},
		},
		{
			name:       "no items, more to read",
			body:       `{"items":[],"skippedRecords":2,"pageInfo":{"nextCursor":"c2","hasMore":true}}`,
			session:    `{"sessionId":"RM_1","status":"SESSION_STATUS_ACTIVE"}`,
			wantStatus: []string{"2 records couldn't be read", "More items available — next page: --cursor c2"},
			noStatus:   []string{"still active", "no transcript"},
			wantPaths:  []string{"/v0/projects/p1/sessions/RM_1/transcript"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, paths := transcriptAPI(t, http.StatusOK, tt.body, tt.session)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionTranscript(context.Background(), client, "p1", "RM_1", tt.opts, false))

			plain := ansiEscapeRe.ReplaceAllString(stdout.String(), "")
			for _, want := range tt.wantOut {
				assert.Contains(t, plain, want)
			}
			if len(tt.wantOut) == 0 {
				assert.Empty(t, stdout.String())
			}
			for _, want := range tt.wantStatus {
				assert.Contains(t, stderr.String(), want)
			}
			for _, unwanted := range tt.noStatus {
				assert.NotContains(t, stderr.String(), unwanted)
			}
			assert.Equal(t, tt.wantPaths, *paths)
		})
	}
}

// TestFetchSessionTranscriptJSON checks --json prints the items and still
// explains an empty transcript on stderr.
func TestFetchSessionTranscriptJSON(t *testing.T) {
	client, _ := transcriptAPI(t, http.StatusOK, `{"items":[]}`, `{"sessionId":"RM_1","status":"SESSION_STATUS_ACTIVE"}`)
	stdout, stderr := captureOut(t)

	require.NoError(t, fetchSessionTranscript(context.Background(), client, "p1", "RM_1", public.PageOptions{}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "still active")
}

// TestFetchSessionTranscriptErrors checks why a session has no transcript to
// print: it doesn't exist, or user data recording is off.
func TestFetchSessionTranscriptErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "unknown session",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"session not found"}`,
			wantErr: []string{"no session RM_1 in project p1", "session not found"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{
				"session RM_1 has no transcript",
				"user data recording is off for this project",
				"https://cloud.example/projects/p1/settings/observability",
			},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "reading a session's transcript requires being a project admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := transcriptAPI(t, tt.status, tt.body, `{}`)
			stdout, _ := captureOut(t)

			err := fetchSessionTranscript(context.Background(), client, "p1", "RM_1", public.PageOptions{}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSessionLogsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	logsCmd := findCommandByName(sessionCmd.Commands, "logs")
	require.NotNil(t, logsCmd, "'analytics session logs' command must exist")
	require.NotNil(t, logsCmd.Action)
	for _, name := range []string{"log-level", "sort-order", "limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(logsCmd.Flags, name), "--%s", name)
	}
}

// TestSessionLogsRequiresExperimentalAuth checks the logs read, which has no
// API-key endpoint, refuses to run without --experimental-auth before reading
// its arguments or any config.
func TestSessionLogsRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "logs", "RM_1"},
		{"--experimental", "session", "logs", "RM_1", "--log-level", "loud"},
		{"--experimental", "session", "logs"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// logCmdOptions runs logOptions with the given arguments on a command built
// from fresh analyticsLogFlags.
func logCmdOptions(t *testing.T, args ...string) (public.LogOptions, error) {
	t.Helper()
	var opts public.LogOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "logs",
		Flags: analyticsLogFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = logOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"logs"}, args...)))
	return opts, optsErr
}

func TestLogOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.LogOptions
		wantErr string
	}{
		{name: "defaults", want: public.LogOptions{Levels: []string{}}},
		{
			name: "levels, order and paging",
			args: []string{"--log-level", "warn", "--log-level", "ERROR", "--sort-order", "desc", "--limit", "100", "--cursor", "abc"},
			want: public.LogOptions{PageOptions: public.PageOptions{Limit: 100, Cursor: "abc"}, Levels: []string{"warn", "ERROR"}, SortOrder: "desc"},
		},
		{name: "unknown level", args: []string{"--log-level", "info", "--log-level", "loud"}, wantErr: `invalid log level "loud"`},
		{name: "unknown sort order", args: []string{"--sort-order", "newest"}, wantErr: `invalid sort order "newest"`},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := logCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// logsAPI starts a stand-in Public API that answers the logs read with status
// and body, recording the query it was sent.
func logsAPI(t *testing.T, status int, body string) (*public.Client, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekret" || r.URL.Path != "/v0/projects/p1/sessions/RM_1/logs" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &query
}

// TestFetchSessionLogs prints a page one line per record with the levels it
// asked for and, when a page is empty, says why: no records at those levels,
// none at all, or no more.
func TestFetchSessionLogs(t *testing.T) {
	const page = `{"records":[` +
		`{"id":"log_1","level":"LOG_LEVEL_WARN","logger":"app","message":"slow tts"},` +
		`{"id":"log_2","level":"LOG_LEVEL_ERROR","logger":"app","message":"tts failed"}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	tests := []struct {
		name       string
		body       string
		opts       public.LogOptions
		wantQuery  url.Values
		wantOut    []string
		wantStatus []string
	}{
		{
			name:       "records",
			body:       page,
			opts:       public.LogOptions{Levels: []string{"warn", "error"}},
			wantQuery:  url.Values{"logLevels": {"LOG_LEVEL_WARN", "LOG_LEVEL_ERROR"}},
			wantOut:    []string{"WARN    app  slow tts", "ERROR   app  tts failed"},
			wantStatus: []string{"More records available — next page: --cursor c2"},
		},
		{
			name:       "no records at those levels",
			body:       `{"records":[]}`,
			opts:       public.LogOptions{Levels: []string{"fatal"}},
			wantQuery:  url.Values{"logLevels": {"LOG_LEVEL_FATAL"}},
			wantStatus: []string{"Session RM_1 has no agent log records at the levels asked for (fatal)"},
		},
		{
			name:       "no records",
			body:       `{}`,
			wantQuery:  url.Values{},
			wantStatus: []string{"Session RM_1 has no agent logs", "a session without an agent has none"},
		},
		{
			name:       "last page",
			body:       `{"records":[]}`,
			opts:       public.LogOptions{PageOptions: public.PageOptions{Cursor: "c2"}},
			wantQuery:  url.Values{"page.cursor": {"c2"}},
			wantStatus: []string{"No more log records"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := logsAPI(t, http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionLogs(context.Background(), client, "p1", "RM_1", tt.opts, false))

			assert.Equal(t, tt.wantQuery, *query)
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
			if len(tt.wantOut) == 0 {
				assert.Empty(t, stdout.String())
			}
			for _, want := range tt.wantStatus {
				assert.Contains(t, stderr.String(), want)
			}
		})
	}
}

// TestFetchSessionLogsJSON checks --json prints the records and still
// explains an empty page on stderr.
func TestFetchSessionLogsJSON(t *testing.T) {
	client, _ := logsAPI(t, http.StatusOK, `{"records":[]}`)
	stdout, stderr := captureOut(t)

	require.NoError(t, fetchSessionLogs(context.Background(), client, "p1", "RM_1", public.LogOptions{}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "has no agent logs")
}

// TestFetchSessionLogsErrors checks why a session has no logs to print: it
// doesn't exist, or user data recording is off.
func TestFetchSessionLogsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "unknown session",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"session not found"}`,
			wantErr: []string{"no session RM_1 in project p1", "session not found"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{
				"session RM_1 has no agent logs",
				"user data recording is off for this project",
				"https://cloud.example/projects/p1/settings/observability",
			},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "reading a session's agent logs requires being a project admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := logsAPI(t, tt.status, tt.body)
			stdout, _ := captureOut(t)

			err := fetchSessionLogs(context.Background(), client, "p1", "RM_1", public.LogOptions{}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSessionTracesCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	tracesCmd := findCommandByName(sessionCmd.Commands, "traces")
	require.NotNil(t, tracesCmd, "'analytics session traces' command must exist")
	require.NotNil(t, tracesCmd.Action)
	for _, name := range []string{"limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(tracesCmd.Flags, name), "--%s", name)
	}
}

// TestSessionTracesRequiresExperimentalAuth checks the trace read, which has
// no API-key endpoint, refuses to run without --experimental-auth before
// reading its arguments or any config.
func TestSessionTracesRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "traces", "RM_1"},
		{"--experimental", "session", "traces", "RM_1", "--limit", "0"},
		{"--experimental", "session", "traces"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// traceCmdOptions runs pageOptions with the given arguments on a command
// built from fresh analyticsTraceFlags.
func traceCmdOptions(t *testing.T, args ...string) (public.PageOptions, error) {
	t.Helper()
	var opts public.PageOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "traces",
		Flags: analyticsTraceFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = pageOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"traces"}, args...)))
	return opts, optsErr
}

func TestTraceOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.PageOptions
		wantErr string
	}{
		{name: "defaults", want: public.PageOptions{}},
		{name: "limit and cursor", args: []string{"--limit", "250", "--cursor", "abc"}, want: public.PageOptions{Limit: 250, Cursor: "abc"}},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := traceCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// tracesAPI starts a stand-in Public API that answers the trace read with
// status and, for each cursor it is sent ("" for the first page), that page's
// body. It records the query of every request.
func tracesAPI(t *testing.T, status int, pages map[string]string) (*public.Client, *[]url.Values) {
	t.Helper()
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekret" || r.URL.Path != "/v0/projects/p1/sessions/RM_1/traces" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(pages[r.URL.Query().Get("page.cursor")]))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &queries
}

// tracePages is a session's trace across two pages, the second starting with
// the children of a span on the first.
var tracePages = map[string]string{
	"": `{"spans":[` +
		`{"spanId":"a1","name":"agent_session"},` +
		`{"spanId":"b2","parentSpanId":"a1","name":"agent_turn"}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`,
	"c2": `{"spans":[` +
		`{"spanId":"c3","parentSpanId":"b2","name":"llm_request"},` +
		`{"spanId":"d4","parentSpanId":"b2","name":"tts_request"}]}`,
}

// TestFetchSessionTraces checks the command reads every page up to its limit,
// asking for full pages but no more spans than it has left, and prints one
// tree across the pages; a read that stops at its limit says how to read the
// rest.
func TestFetchSessionTraces(t *testing.T) {
	tests := []struct {
		name        string
		opts        public.PageOptions
		wantQueries []url.Values
		wantOut     []string
		wantStatus  []string
	}{
		{
			name:        "no limit reads one page of the server's size",
			opts:        public.PageOptions{},
			wantQueries: []url.Values{{}},
			wantOut:     []string{"agent_session", "└─ agent_turn"},
			wantStatus:  []string{"Printed 2 spans; more remain", "read the next ones with --cursor c2"},
		},
		{
			name: "every page",
			opts: public.PageOptions{Limit: 1000},
			wantQueries: []url.Values{
				{"page.pageSize": {"1000"}},
				{"page.pageSize": {"998"}, "page.cursor": {"c2"}},
			},
			wantOut: []string{"agent_session", "└─ agent_turn", "   ├─ llm_request", "   └─ tts_request"},
		},
		{
			name:        "stops at the limit",
			opts:        public.PageOptions{Limit: 2},
			wantQueries: []url.Values{{"page.pageSize": {"2"}}},
			wantOut:     []string{"agent_session", "└─ agent_turn"},
			wantStatus:  []string{"Printed 2 spans; more remain", "read the next ones with --cursor c2"},
		},
		{
			name: "last page asks for what is left",
			opts: public.PageOptions{Limit: 3},
			wantQueries: []url.Values{
				{"page.pageSize": {"3"}},
				{"page.pageSize": {"1"}, "page.cursor": {"c2"}},
			},
		},
		{
			name:        "from a cursor",
			opts:        public.PageOptions{Limit: 1000, Cursor: "c2"},
			wantQueries: []url.Values{{"page.pageSize": {"1000"}, "page.cursor": {"c2"}}},
			wantOut:     []string{"llm_request", "tts_request"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, queries := tracesAPI(t, http.StatusOK, tracePages)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionTraces(context.Background(), client, "p1", "RM_1", tt.opts, false))

			assert.Equal(t, tt.wantQueries, *queries)
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
			for _, want := range tt.wantStatus {
				assert.Contains(t, stderr.String(), want)
			}
			if len(tt.wantStatus) == 0 {
				assert.Empty(t, stderr.String())
			}
		})
	}
}

// TestFetchSessionTracesEmpty checks an empty read says why: the session has
// no spans, or a cursor's read has no more.
func TestFetchSessionTracesEmpty(t *testing.T) {
	for _, tt := range []struct {
		cursor     string
		wantStatus string
	}{
		{wantStatus: "Session RM_1 has no trace spans"},
		{cursor: "c9", wantStatus: "No more spans"},
	} {
		client, _ := tracesAPI(t, http.StatusOK, map[string]string{"": `{}`, "c9": `{"spans":[]}`})
		stdout, stderr := captureOut(t)

		require.NoError(t, fetchSessionTraces(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 10, Cursor: tt.cursor}, false))
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), tt.wantStatus)
	}
}

// TestFetchSessionTracesJSON checks --json prints every span read, across
// pages, and still explains an empty read on stderr.
func TestFetchSessionTracesJSON(t *testing.T) {
	client, _ := tracesAPI(t, http.StatusOK, tracePages)
	stdout, _ := captureOut(t)
	require.NoError(t, fetchSessionTraces(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 10}, true))
	var got struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	require.Len(t, got.Items, 4)
	assert.Equal(t, "b2", got.Items[3]["parentSpanId"])

	// A read stopped at its limit keeps the last page's pageInfo.
	client, _ = tracesAPI(t, http.StatusOK, tracePages)
	stdout, _ = captureOut(t)
	require.NoError(t, fetchSessionTraces(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 2}, true))
	var stopped struct {
		PageInfo util.PageInfo `json:"pageInfo"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &stopped))
	assert.Equal(t, util.PageInfo{NextCursor: "c2", HasMore: true}, stopped.PageInfo)

	client, _ = tracesAPI(t, http.StatusOK, map[string]string{"": `{}`})
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchSessionTraces(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 10}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "has no trace spans")
}

// TestFetchSessionTracesErrors checks why a session has no spans to print: it
// doesn't exist, or user data recording is off.
func TestFetchSessionTracesErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "unknown session",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"session not found"}`,
			wantErr: []string{"no session RM_1 in project p1", "session not found"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{
				"session RM_1 has no trace spans",
				"user data recording is off for this project",
				"https://cloud.example/projects/p1/settings/observability",
			},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "reading a session's trace spans requires being a project admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := tracesAPI(t, tt.status, map[string]string{"": tt.body})
			stdout, _ := captureOut(t)

			err := fetchSessionTraces(context.Background(), client, "p1", "RM_1", public.PageOptions{Limit: 10}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSessionMetricsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	metricsCmd := findCommandByName(sessionCmd.Commands, "metrics")
	require.NotNil(t, metricsCmd, "'analytics session metrics' command must exist")
	require.NotNil(t, metricsCmd.Action)
	for _, name := range []string{"name", "limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(metricsCmd.Flags, name), "--%s", name)
	}
	cursor := findFlagByName(metricsCmd.Flags, "cursor")
	assert.True(t, cursor.(*cli.StringFlag).Hidden, "--cursor must be hidden")
	assert.Contains(t, metricsCmd.Description, "won't exactly match the dashboard's metrics panel")
}

// TestSessionMetricsRequiresExperimentalAuth checks the metrics read, which
// has no API-key endpoint, refuses to run without --experimental-auth before
// reading its arguments or any config.
func TestSessionMetricsRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "metrics", "RM_1"},
		{"--experimental", "session", "metrics", "RM_1", "--name", " "},
		{"--experimental", "session", "metrics"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// metricCmdOptions runs metricOptions with the given arguments on a command
// built from fresh analyticsMetricFlags.
func metricCmdOptions(t *testing.T, args ...string) (public.MetricOptions, error) {
	t.Helper()
	var opts public.MetricOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "metrics",
		Flags: analyticsMetricFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = metricOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"metrics"}, args...)))
	return opts, optsErr
}

func TestMetricOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.MetricOptions
		wantErr string
	}{
		{name: "defaults", want: public.MetricOptions{Names: []string{}}},
		{
			name: "names and paging",
			args: []string{"--name", "lk.agents.turn.e2e_latency", "--name", "lk.agents.usage.llm_input_tokens", "--limit", "100", "--cursor", "abc"},
			want: public.MetricOptions{PageOptions: public.PageOptions{Limit: 100, Cursor: "abc"}, Names: []string{"lk.agents.turn.e2e_latency", "lk.agents.usage.llm_input_tokens"}},
		},
		{name: "blank name", args: []string{"--name", " "}, wantErr: "metric name must not be empty"},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := metricCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// metricsAPI starts a stand-in Public API that answers the metrics read with
// status and body, recording the query it was sent.
func metricsAPI(t *testing.T, status int, body string) (*public.Client, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekret" || r.URL.Path != "/v0/projects/p1/sessions/RM_1/metrics" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &query
}

// TestFetchSessionMetrics prints a page one line per point with the names it
// asked for and, when a page is empty, says why: no points for those names,
// none at all, or no more.
func TestFetchSessionMetrics(t *testing.T) {
	const page = `{"points":[` +
		`{"name":"lk.agents.turn.e2e_latency","unit":"s","kind":"METRIC_KIND_HISTOGRAM","endTime":"2026-10-07T11:00:30Z",` +
		`"histogram":{"count":"3","sum":2.4,"min":0.6,"max":1.1}},` +
		`{"name":"lk.agents.usage.llm_input_tokens","unit":"{token}","kind":"METRIC_KIND_SUM","endTime":"2026-10-07T11:00:30Z","value":1520}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	tests := []struct {
		name       string
		body       string
		opts       public.MetricOptions
		wantQuery  url.Values
		wantOut    []string
		wantStatus []string
	}{
		{
			name:      "points",
			body:      page,
			opts:      public.MetricOptions{Names: []string{"lk.agents.turn.e2e_latency", "lk.agents.usage.llm_input_tokens"}},
			wantQuery: url.Values{"names": {"lk.agents.turn.e2e_latency", "lk.agents.usage.llm_input_tokens"}},
			wantOut: []string{
				"lk.agents.turn.e2e_latency        count 3, sum 2.4 s, min 0.6 s, max 1.1 s",
				"lk.agents.usage.llm_input_tokens  1520 token",
			},
			wantStatus: []string{"More points available — next page: --cursor c2"},
		},
		{
			name:       "no points for those names",
			body:       `{"points":[]}`,
			opts:       public.MetricOptions{Names: []string{"lk.agents.nope"}},
			wantQuery:  url.Values{"names": {"lk.agents.nope"}},
			wantStatus: []string{"Session RM_1 has no points for the metrics asked for (lk.agents.nope)"},
		},
		{
			name:       "no points",
			body:       `{}`,
			wantQuery:  url.Values{},
			wantStatus: []string{"Session RM_1 has no agent metrics", "only agents that export OpenTelemetry metrics"},
		},
		{
			name:       "last page",
			body:       `{"points":[]}`,
			opts:       public.MetricOptions{PageOptions: public.PageOptions{Cursor: "c2"}},
			wantQuery:  url.Values{"page.cursor": {"c2"}},
			wantStatus: []string{"No more metric points"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := metricsAPI(t, http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionMetrics(context.Background(), client, "p1", "RM_1", tt.opts, false))

			assert.Equal(t, tt.wantQuery, *query)
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
			if len(tt.wantOut) == 0 {
				assert.Empty(t, stdout.String())
			}
			for _, want := range tt.wantStatus {
				assert.Contains(t, stderr.String(), want)
			}
		})
	}
}

// TestFetchSessionMetricsJSON checks --json prints the points and still
// explains an empty page on stderr.
func TestFetchSessionMetricsJSON(t *testing.T) {
	client, _ := metricsAPI(t, http.StatusOK, `{"points":[]}`)
	stdout, stderr := captureOut(t)

	require.NoError(t, fetchSessionMetrics(context.Background(), client, "p1", "RM_1", public.MetricOptions{}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "has no agent metrics")
}

// TestFetchSessionMetricsErrors checks why a session has no metrics to print:
// it doesn't exist, or user data recording is off.
func TestFetchSessionMetricsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "unknown session",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"session not found"}`,
			wantErr: []string{"no session RM_1 in project p1", "session not found"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{
				"session RM_1 has no agent metrics",
				"user data recording is off for this project",
				"https://cloud.example/projects/p1/settings/observability",
			},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "reading a session's agent metrics requires being a project admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := metricsAPI(t, tt.status, tt.body)
			stdout, _ := captureOut(t)

			err := fetchSessionMetrics(context.Background(), client, "p1", "RM_1", public.MetricOptions{}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSessionEventsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	sessionCmd := findCommandByName(analyticsCmd.Commands, "session")
	require.NotNil(t, sessionCmd)
	eventsCmd := findCommandByName(sessionCmd.Commands, "events")
	require.NotNil(t, eventsCmd, "'analytics session events' command must exist")
	require.NotNil(t, eventsCmd.Action)
	for _, name := range []string{"type", "participant", "sort-order", "limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(eventsCmd.Flags, name), "--%s", name)
	}
	assert.True(t, findFlagByName(eventsCmd.Flags, "cursor").(*cli.StringFlag).Hidden, "--cursor must be hidden")
}

// TestSessionEventsRequiresExperimentalAuth checks the events read, which has
// no API-key endpoint, refuses to run without --experimental-auth before
// reading its arguments or any config.
func TestSessionEventsRequiresExperimentalAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "session", "events", "RM_1"},
		{"--experimental", "session", "events", "RM_1", "--type", "joined"},
		{"--experimental", "session", "events"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --experimental-auth")
	}
}

// eventCmdOptions runs eventOptions with the given arguments on a command
// built from fresh analyticsEventFlags.
func eventCmdOptions(t *testing.T, args ...string) (public.EventOptions, error) {
	t.Helper()
	var opts public.EventOptions
	var optsErr error
	cmd := &cli.Command{
		Name:  "events",
		Flags: analyticsEventFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = eventOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"events"}, args...)))
	return opts, optsErr
}

func TestEventOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    public.EventOptions
		wantErr string
	}{
		{name: "defaults", want: public.EventOptions{Types: []string{}}},
		{
			name: "types, participant session, order and paging",
			args: []string{"--type", "track_published", "--type", "PARTICIPANT_LEFT", "--participant", "PA_aaaaaaaaaaaa",
				"--sort-order", "desc", "--limit", "100", "--cursor", "abc"},
			want: public.EventOptions{
				PageOptions: public.PageOptions{Limit: 100, Cursor: "abc"}, Types: []string{"track_published", "PARTICIPANT_LEFT"},
				ParticipantSessionID: "PA_aaaaaaaaaaaa", SortOrder: "desc",
			},
		},
		{name: "unknown type", args: []string{"--type", "participant_joined", "--type", "joined"}, wantErr: `invalid event type "joined"`},
		{name: "identity for participant session", args: []string{"--participant", "alice"}, wantErr: `invalid participant session id "alice"`},
		{name: "unknown sort order", args: []string{"--sort-order", "newest"}, wantErr: `invalid sort order "newest"`},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := eventCmdOptions(t, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// eventsAPI starts a stand-in Public API that answers the events read with
// status and body, recording the query it was sent.
func eventsAPI(t *testing.T, status int, body string) (*public.Client, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekret" || r.URL.Path != "/v0/projects/p1/sessions/RM_1/events" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "sekret")
	require.NoError(t, err)
	return client, &query
}

// TestFetchSessionEvents prints a page one line per event with the filters it
// asked for and, when a page is empty, says why: none of those types or for
// that participant session, none at all, or no more.
func TestFetchSessionEvents(t *testing.T) {
	const page = `{"items":[` +
		`{"type":"PARTICIPANT_JOINED","participantIdentity":"alice","participantSessionId":"PA_aaaaaaaaaaaa","payload":{"connectionType":"UDP"}},` +
		`{"type":"TRACK_PUBLISHED","participantIdentity":"alice","participantSessionId":"PA_aaaaaaaaaaaa","payload":{"trackType":"AUDIO"}}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	tests := []struct {
		name       string
		body       string
		opts       public.EventOptions
		wantQuery  url.Values
		wantOut    []string
		wantStatus []string
	}{
		{
			name:      "events",
			body:      page,
			opts:      public.EventOptions{Types: []string{"participant_joined", "track_published"}, ParticipantSessionID: "PA_aaaaaaaaaaaa"},
			wantQuery: url.Values{"types": {"PARTICIPANT_JOINED", "TRACK_PUBLISHED"}, "participantSessionId": {"PA_aaaaaaaaaaaa"}},
			wantOut: []string{
				"participant_joined  alice  PA_aaaaaaaaaaaa  connectionType=UDP",
				"track_published     alice  PA_aaaaaaaaaaaa  trackType=AUDIO",
			},
			wantStatus: []string{"More events available — next page: --cursor c2"},
		},
		{
			name:       "none of those types",
			body:       `{"items":[]}`,
			opts:       public.EventOptions{Types: []string{"track_muted"}},
			wantQuery:  url.Values{"types": {"TRACK_MUTED"}},
			wantStatus: []string{"Session RM_1 has no events of the types asked for (track_muted)"},
		},
		{
			name:       "none for that participant session",
			body:       `{"items":[]}`,
			opts:       public.EventOptions{ParticipantSessionID: "PA_aaaaaaaaaaaa"},
			wantQuery:  url.Values{"participantSessionId": {"PA_aaaaaaaaaaaa"}},
			wantStatus: []string{"Session RM_1 has no events for participant session PA_aaaaaaaaaaaa"},
		},
		{
			name:       "no events",
			body:       `{}`,
			wantQuery:  url.Values{},
			wantStatus: []string{"Session RM_1 has no events", "kept for 60 days"},
		},
		{
			name:       "last page",
			body:       `{"items":[]}`,
			opts:       public.EventOptions{PageOptions: public.PageOptions{Cursor: "c2"}},
			wantQuery:  url.Values{"page.cursor": {"c2"}},
			wantStatus: []string{"No more events"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := eventsAPI(t, http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchSessionEvents(context.Background(), client, "p1", "RM_1", tt.opts, false))

			assert.Equal(t, tt.wantQuery, *query)
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
			if len(tt.wantOut) == 0 {
				assert.Empty(t, stdout.String())
			}
			for _, want := range tt.wantStatus {
				assert.Contains(t, stderr.String(), want)
			}
		})
	}
}

// TestFetchSessionEventsJSON checks --json prints the events and still
// explains an empty page on stderr.
func TestFetchSessionEventsJSON(t *testing.T) {
	client, _ := eventsAPI(t, http.StatusOK, `{"items":[]}`)
	stdout, stderr := captureOut(t)

	require.NoError(t, fetchSessionEvents(context.Background(), client, "p1", "RM_1", public.EventOptions{}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "has no events")
}

// TestFetchSessionEventsErrors checks why a session's events can't be printed:
// it doesn't exist, the filter is invalid, or the user is signed out.
func TestFetchSessionEventsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "unknown session",
			status:  http.StatusNotFound,
			body:    `{"code":5,"message":"session not found"}`,
			wantErr: []string{"no session RM_1 in project p1", "session not found"},
		},
		{
			name:    "type the server doesn't know",
			status:  http.StatusBadRequest,
			body:    `{"code":3,"message":"invalid argument: \"SIP_CALL_UPDATE\" is not an event type"}`,
			wantErr: []string{"is not an event type"},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
		{
			name:    "permission denied",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"permission denied"}`,
			wantErr: []string{"permission denied", "you don't have access to this project"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := eventsAPI(t, tt.status, tt.body)
			stdout, _ := captureOut(t)

			err := fetchSessionEvents(context.Background(), client, "p1", "RM_1", public.EventOptions{}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

// TestSessionAPIError checks a permission denial on a Public-API-only session
// read says what the read requires, never to use API-key credentials, which
// these reads can't use, while other errors keep cloudAPIError's hints.
func TestSessionAPIError(t *testing.T) {
	denied := &public.APIError{Status: http.StatusForbidden, Message: "permission denied"}

	err := sessionAPIError(denied, "transcript", projectAdminAccess)
	assert.EqualError(t, err, "permission denied — reading a session's transcript requires being a project admin")
	assert.True(t, public.IsPermissionDenied(err))

	err = sessionAPIError(denied, "participants", projectReadAccess)
	assert.EqualError(t, err, "permission denied — you don't have access to this project")

	signedOut := &public.APIError{Status: http.StatusUnauthorized, Message: "authentication required"}
	assert.ErrorContains(t, sessionAPIError(signedOut, "transcript", projectAdminAccess), "lk cloud auth")
}
