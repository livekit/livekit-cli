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
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// projectAPI starts a stand-in Public API that answers path with status and
// body, recording the query it was sent.
func projectAPI(t *testing.T, path string, status int, body string) (*public.Client, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekret" || r.URL.Path != path {
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

// cmdOptions runs readOptions with the given arguments on a command built
// from flags.
func cmdOptions[O any](t *testing.T, flags []cli.Flag, readOptions func(*cli.Command) (O, error), args ...string) (O, error) {
	t.Helper()
	var opts O
	var optsErr error
	cmd := &cli.Command{
		Name:  "read",
		Flags: flags,
		Action: func(_ context.Context, cmd *cli.Command) error {
			opts, optsErr = readOptions(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"read"}, args...)))
	return opts, optsErr
}

// TestTimeRangeOptions checks --since counts back from now, --start and
// --end take a date or an RFC 3339 time, and a bad or empty range fails.
func TestTimeRangeOptions(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	analyticsNow = func() time.Time { return now }
	t.Cleanup(func() { analyticsNow = time.Now })

	tests := []struct {
		name    string
		args    []string
		want    public.TimeRange
		wantErr string
	}{
		{name: "unset", want: public.TimeRange{}},
		{name: "since", args: []string{"--since", "2d"}, want: public.TimeRange{Start: now.Add(-48 * time.Hour)}},
		{
			name: "dates",
			args: []string{"--start", "2026-11-20", "--end", "2026-11-21"},
			want: public.TimeRange{Start: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)},
		},
		{
			name: "times",
			args: []string{"--start", "2026-11-20T08:30:00-08:00", "--end", "2026-11-20T17:00:00.5Z"},
			want: public.TimeRange{
				Start: time.Date(2026, 11, 20, 16, 30, 0, 0, time.UTC),
				End:   time.Date(2026, 11, 20, 17, 0, 0, 500e6, time.UTC),
			},
		},
		{name: "only an end", args: []string{"--end", "2026-11-21"}, want: public.TimeRange{End: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)}},
		{name: "since with start", args: []string{"--since", "1h", "--start", "2026-11-20"}, wantErr: "--since can't be used with --start or --end"},
		{name: "bad since", args: []string{"--since", "yesterday"}, wantErr: `invalid duration "yesterday"`},
		{name: "bad start", args: []string{"--start", "11/20"}, wantErr: `invalid --start "11/20"`},
		{name: "empty range", args: []string{"--start", "2026-11-21", "--end", "2026-11-21"}, wantErr: "--start must be before --end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cmdOptions(t, timeRangeFlags("24h"), timeRangeOptions, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.True(t, tt.want.Start.Equal(got.Start), "start: want %v, got %v", tt.want.Start, got.Start)
			assert.True(t, tt.want.End.Equal(got.End), "end: want %v, got %v", tt.want.End, got.End)
		})
	}
}

func TestProjectLogsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	logsCmd := findCommandByName(analyticsCmd.Commands, "logs")
	require.NotNil(t, logsCmd, "'analytics logs' command must exist")
	require.NotNil(t, logsCmd.Action)
	for _, name := range []string{"since", "start", "end", "log-level", "source", "session-id", "query", "sort-order", "limit", "cursor", "json"} {
		assert.NotNil(t, findFlagByName(logsCmd.Flags, name), "--%s", name)
	}
}

// TestProjectLogsRequiresUserAuth checks the project logs read, which has no
// API-key endpoint, refuses to run without --user-auth before reading its
// flags or any config.
func TestProjectLogsRequiresUserAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--experimental", "logs"},
		{"--experimental", "logs", "--source", "system"},
	} {
		err := runAnalytics(args...)
		require.ErrorContains(t, err, "only available under --user-auth")
	}
}

func TestProjectLogOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    projectLogRead
		wantErr string
	}{
		{
			name: "defaults",
			want: projectLogRead{ProjectLogOptions: public.ProjectLogOptions{Levels: []string{}, Sources: []string{}}, defaultRange: true},
		},
		{
			name: "filters, order and paging",
			args: []string{"--start", "2026-11-20", "--log-level", "error", "--source", "agent", "--session-id", " RM_1 ",
				"--query", "@lk.cloud_agent_id:CA_1", "--sort-order", "asc", "--limit", "20", "--cursor", "abc"},
			want: projectLogRead{ProjectLogOptions: public.ProjectLogOptions{
				PageOptions: public.PageOptions{Limit: 20, Cursor: "abc"},
				Range:       public.TimeRange{Start: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)},
				Levels:      []string{"error"},
				Sources:     []string{"agent"},
				SessionID:   "RM_1",
				Query:       "@lk.cloud_agent_id:CA_1",
				SortOrder:   "asc",
			}},
		},
		{name: "unknown source", args: []string{"--source", "system"}, wantErr: `invalid log source "system"`},
		{name: "unknown level", args: []string{"--log-level", "loud"}, wantErr: `invalid log level "loud"`},
		{name: "bad range", args: []string{"--start", "tomorrow"}, wantErr: `invalid --start "tomorrow"`},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := cmdOptions(t, analyticsProjectLogFlags(), projectLogOptions, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// TestFetchProjectLogs prints a page one line per record with the filters it
// asked for and, when a page is empty, says why.
func TestFetchProjectLogs(t *testing.T) {
	const page = `{"items":[` +
		`{"id":"log_1","level":"LOG_LEVEL_ERROR","logger":"livekit.agents","message":"tts failed","sessionId":"RM_1","source":"LOG_SOURCE_AGENT"},` +
		`{"id":"log_2","level":"LOG_LEVEL_INFO","message":"API_CALL","source":"LOG_SOURCE_PLATFORM"}],` +
		`"pageInfo":{"nextCursor":"c2","hasMore":true}}`
	tests := []struct {
		name       string
		body       string
		opts       projectLogRead
		wantQuery  url.Values
		wantOut    []string
		wantStatus []string
	}{
		{
			name:      "records",
			body:      page,
			opts:      projectLogRead{ProjectLogOptions: public.ProjectLogOptions{Sources: []string{"agent", "platform"}}},
			wantQuery: url.Values{"sources": {"LOG_SOURCE_AGENT", "LOG_SOURCE_PLATFORM"}},
			wantOut: []string{
				"ERROR   agent     RM_1  livekit.agents  tts failed",
				"INFO    platform  -     -               API_CALL",
			},
			wantStatus: []string{"More records available — next page: --cursor c2"},
		},
		{
			name:       "none in the default range",
			body:       `{}`,
			opts:       projectLogRead{defaultRange: true},
			wantQuery:  url.Values{},
			wantStatus: []string{"No log records in the last 24 hours", "--since DURATION"},
		},
		{
			name:       "none match",
			body:       `{"items":[]}`,
			opts:       projectLogRead{ProjectLogOptions: public.ProjectLogOptions{Query: "timeout"}},
			wantQuery:  url.Values{"query": {"timeout"}},
			wantStatus: []string{"No log records in the range match the filters"},
		},
		{
			name:       "last page",
			body:       `{"items":[]}`,
			opts:       projectLogRead{ProjectLogOptions: public.ProjectLogOptions{PageOptions: public.PageOptions{Cursor: "c2"}}},
			wantQuery:  url.Values{"page.cursor": {"c2"}},
			wantStatus: []string{"No more log records"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := projectAPI(t, "/v0/projects/p1/logs", http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchProjectLogs(context.Background(), client, "p1", tt.opts, false))

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

// TestFetchProjectLogsJSON checks --json prints the records and their cursor,
// and still explains an empty page on stderr.
func TestFetchProjectLogsJSON(t *testing.T) {
	client, _ := projectAPI(t, "/v0/projects/p1/logs", http.StatusOK, `{"items":[{"id":"log_1","message":"hi"}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`)
	stdout, _ := captureOut(t)
	require.NoError(t, fetchProjectLogs(context.Background(), client, "p1", projectLogRead{}, true))
	assert.JSONEq(t, `{"items":[{"id":"log_1","message":"hi"}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`, stdout.String())

	client, _ = projectAPI(t, "/v0/projects/p1/logs", http.StatusOK, `{"items":[]}`)
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchProjectLogs(context.Background(), client, "p1", projectLogRead{}, true))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
	assert.Contains(t, stderr.String(), "No log records in the range")
}

// TestFetchProjectLogsErrors checks why a project has no logs to print: the
// caller isn't an admin, the query doesn't parse, or user data recording is
// off.
func TestFetchProjectLogsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr []string
	}{
		{
			name:    "not an admin",
			status:  http.StatusForbidden,
			body:    `{"code":7,"message":"this needs observability:read on the project"}`,
			wantErr: []string{"this needs observability:read on the project", "reading a project's logs requires being a project admin"},
		},
		{
			name:    "invalid query",
			status:  http.StatusBadRequest,
			body:    `{"code":3,"message":"query isn't valid log search syntax"}`,
			wantErr: []string{"query isn't valid log search syntax", "@lk.cloud_agent_id:CA_ID"},
		},
		{
			name:    "range expired",
			status:  http.StatusBadRequest,
			body:    `{"code":3,"message":"range ends before the 14-day log retention period"}`,
			wantErr: []string{"range ends before the 14-day log retention period"},
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantErr: []string{"the project has no logs", "user data recording is off for this project", "https://cloud.example/projects/p1/settings/observability"},
		},
		{
			name:    "signed out",
			status:  http.StatusUnauthorized,
			body:    `{"code":16,"message":"authentication required"}`,
			wantErr: []string{"authentication required", "lk cloud auth"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := projectAPI(t, "/v0/projects/p1/logs", tt.status, tt.body)
			stdout, _ := captureOut(t)

			err := fetchProjectLogs(context.Background(), client, "p1", projectLogRead{}, false)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}
