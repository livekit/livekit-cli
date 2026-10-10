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
	tests := []struct {
		name    string
		args    []string
		want    public.SessionListOptions
		wantErr string
	}{
		{
			name: "defaults",
			want: public.SessionListOptions{Limit: defaultAnalyticsLimit, Statuses: []string{}, Tags: []string{}},
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
		if r.URL.Path != "/v1/projects/p1/sessions/RM_1/participants" || r.Header.Get("Authorization") != "Bearer sekret" {
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
// table with a hint to re-run with the next cursor, sending the limit, cursor
// and order asked for, and --json prints {items, nextCursor}.
func TestFetchSessionParticipants(t *testing.T) {
	const page = `{"items":[{"participantIdentity":"alice","region":"US East"}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	opts := public.ParticipantListOptions{PageOptions: public.PageOptions{Limit: 50, Cursor: "c1"}, SortBy: "left", SortOrder: "asc"}

	client, query := participantsAPI(t, http.StatusOK, page)
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchSessionParticipants(context.Background(), client, "p1", "RM_1", opts, false))
	assert.Equal(t, url.Values{
		"page.pageSize": {"50"}, "page.cursor": {"c1"},
		"sortBy": {"PARTICIPANT_SORT_FIELD_LEFT_AT"}, "sortOrder": {"SORT_ORDER_ASC"},
	}, *query)
	for _, want := range []string{"alice", "US East"} {
		assert.Contains(t, stdout.String(), want)
	}
	assert.Contains(t, stderr.String(), "More participants available — re-run with --cursor c2")

	stdout, _ = captureOut(t)
	require.NoError(t, fetchSessionParticipants(context.Background(), client, "p1", "RM_1", public.ParticipantListOptions{}, true))
	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "alice", got.Items[0]["participantIdentity"])
	assert.Equal(t, "c2", got.NextCursor)
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
	for _, name := range []string{"type", "output", "url-only", "json"} {
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
		Flags: analyticsRecordingFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = sessionRecordingOptionsFrom(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"recording"}, args...)))
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
			name: "chat history to a file",
			args: []string{"--type", "chat-history", "-o", "chat.json"},
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}, Output: "chat.json"},
		},
		{
			name: "url only",
			args: []string{"--type", "audio", "--url-only"},
			want: sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, URLOnly: true},
		},
		{name: "type is required", wantErr: `recording type is required ("audio" or "chat-history")`},
		{name: "unknown type", args: []string{"--type", "transcript"}, wantErr: `invalid recording type "transcript"`},
		{
			name:    "url only saves nothing",
			args:    []string{"--type", "audio", "--url-only", "-o", "a.ogg"},
			wantErr: "--output can't be used with --url-only",
		},
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
		if r.URL.Path != "/v1/projects/p1/sessions/RM_1/recording-url" || r.Header.Get("Authorization") != "Bearer sekret" {
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
			name:     "audio to the default name",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}},
			wantFile: "RM_1-audio.ogg",
			want:     string(audio),
		},
		{
			name:     "chat history to the default name, decompressed",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}},
			wantFile: "RM_1-chat-history.json",
			want:     chat,
		},
		{
			name:     "chat history to -o",
			opts:     sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "chat-history"}, Output: "out/chat.json"},
			wantFile: "out/chat.json",
			want:     chat,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir("out", 0o755))
			client, storeAuth := recordingServers(t, audio, chat)
			_, stderr := captureOut(t)

			require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", tt.opts, false))

			got, err := os.ReadFile(tt.wantFile)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.Contains(t, stderr.String(), tt.wantFile)
			assert.Equal(t, []string{""}, *storeAuth, "the object store gets the signed URL and nothing else")

			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(tt.wantFile), ".*"))
			require.NoError(t, err)
			assert.Empty(t, leftovers, "no partial file is left behind")
		})
	}
}

// TestFetchSessionRecordingURLOnly checks --url-only prints the signed URL and
// downloads nothing.
func TestFetchSessionRecordingURLOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	client, storeAuth := recordingServers(t, []byte("OggS"), "{}")
	stdout, _ := captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}, URLOnly: true}
	require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false))

	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/recording\.ogg\?X-Amz-Signature=abc\n$`, stdout.String())
	assert.Empty(t, *storeAuth, "nothing is downloaded")
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestFetchSessionRecordingJSON checks --json reports the saved file.
func TestFetchSessionRecordingJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	client, _ := recordingServers(t, []byte("OggS"), "{}")
	stdout, _ := captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}}
	require.NoError(t, fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, true))
	assert.JSONEq(t, `{"sessionId":"RM_1","recording":"audio","file":"RM_1-audio.ogg","bytes":4,"recordingStartedAt":"2026-10-07T11:00:00Z"}`, stdout.String())
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
}

// TestFetchSessionRecordingExistingFile checks a download never replaces a
// file at the default name, failing before it asks for a URL, and replaces
// one -o names.
func TestFetchSessionRecordingExistingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("RM_1-audio.ogg", []byte("earlier"), 0o600))
	client, storeAuth := recordingServers(t, []byte("OggS"), "{}")
	captureOut(t)

	opts := sessionRecordingOptions{RecordingURLOptions: public.RecordingURLOptions{Recording: "audio"}}
	err := fetchSessionRecording(context.Background(), client, "p1", "RM_1", opts, false)
	require.EqualError(t, err, "RM_1-audio.ogg already exists; pass -o RM_1-audio.ogg to replace it, or -o FILE to save elsewhere")
	got, err := os.ReadFile("RM_1-audio.ogg")
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(got))
	assert.Empty(t, *storeAuth, "nothing is downloaded")

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
