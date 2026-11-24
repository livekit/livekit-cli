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

func TestLogPatternsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	patternsCmd := findCommandByName(analyticsCmd.Commands, "log-patterns")
	require.NotNil(t, patternsCmd, "'analytics log-patterns' command must exist")
	require.NotNil(t, patternsCmd.Action)
	for _, name := range []string{"since", "start", "end", "min-level", "source", "agent-name", "agent-id", "session-id", "query", "rank", "limit", "json"} {
		assert.NotNil(t, findFlagByName(patternsCmd.Flags, name), "--%s", name)
	}
	assert.Nil(t, findFlagByName(patternsCmd.Flags, "cursor"), "the patterns are one page")

	err := runAnalytics("--experimental", "log-patterns")
	require.ErrorContains(t, err, "only available under --user-auth")
}

func TestLogPatternOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    logPatternRead
		wantErr string
	}{
		{name: "defaults", want: logPatternRead{LogPatternOptions: public.LogPatternOptions{Sources: []string{}}, defaultRange: true}},
		{
			name: "filters, rank and limit",
			args: []string{"--since", "2d", "--min-level", "info", "--source", "client", "--agent-name", "support", "--agent-id", "CA_1",
				"--session-id", "RM_1", "--query", "timeout", "--rank", "first-seen", "--limit", "10"},
			want: logPatternRead{LogPatternOptions: public.LogPatternOptions{
				Range:     public.TimeRange{Start: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)},
				Limit:     10,
				Query:     "timeout",
				MinLevel:  "info",
				Sources:   []string{"client"},
				AgentName: "support",
				AgentID:   "CA_1",
				SessionID: "RM_1",
				RankBy:    "first-seen",
			}},
		},
		{name: "platform source", args: []string{"--source", "platform"}, wantErr: "only agent and client logs have patterns"},
		{name: "unknown rank", args: []string{"--rank", "newest"}, wantErr: `invalid rank "newest"`},
		{name: "unknown level", args: []string{"--min-level", "loud"}, wantErr: `invalid log level "loud"`},
		{name: "non-positive limit", args: []string{"--limit", "0"}, wantErr: "limit must be greater than 0"},
	}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	analyticsNow = func() time.Time { return now }
	t.Cleanup(func() { analyticsNow = time.Now })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := cmdOptions(t, analyticsLogPatternFlags(), logPatternOptions, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

// TestFetchLogPatterns prints one row per pattern, then how many records the
// range holds and whether the counts are estimates; and when there are none,
// says why.
func TestFetchLogPatterns(t *testing.T) {
	const patterns = `{"items":[{"id":"42","template":"tts failed after <N> retries","level":"LOG_LEVEL_ERROR",` +
		`"source":"LOG_SOURCE_AGENT","events":"12"},{"id":"43","template":"no level","events":"3"}],` +
		`"estimated":true,"sampleShare":0.25,"totalEvents":"480"}`
	tests := []struct {
		name       string
		body       string
		opts       logPatternRead
		wantQuery  url.Values
		wantOut    []string
		wantStatus []string
	}{
		{
			name:      "patterns",
			body:      patterns,
			opts:      logPatternRead{LogPatternOptions: public.LogPatternOptions{RankBy: "events", Limit: 2}},
			wantQuery: url.Values{"rankBy": {"LOG_PATTERN_RANK_EVENTS"}, "page.pageSize": {"2"}},
			wantOut:   []string{"12", "ERROR", "agent", "tts failed after <N> retries", "no level"},
			wantStatus: []string{
				"2 patterns shown; the range holds 480 records in all patterns",
				"The counts are estimates: the API read the records of 25% of the sessions",
			},
		},
		{
			name:       "none in the default range",
			body:       `{}`,
			opts:       logPatternRead{defaultRange: true},
			wantQuery:  url.Values{},
			wantStatus: []string{"No agent log patterns at WARN or higher in the last 24 hours", "--min-level trace"},
		},
		{
			name:       "none at the level",
			body:       `{"items":[]}`,
			opts:       logPatternRead{LogPatternOptions: public.LogPatternOptions{MinLevel: "error"}},
			wantQuery:  url.Values{"minLevel": {"LOG_LEVEL_ERROR"}},
			wantStatus: []string{"No log patterns at ERROR or higher in the range"},
		},
		{
			name:       "none match",
			body:       `{"items":[]}`,
			opts:       logPatternRead{LogPatternOptions: public.LogPatternOptions{AgentID: "CA_1"}},
			wantQuery:  url.Values{"agentId": {"CA_1"}},
			wantStatus: []string{"No log patterns in the range match the filters"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := projectAPI(t, "/v0/projects/p1/log-patterns", http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchLogPatterns(context.Background(), client, "p1", tt.opts, false))

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

// TestFetchLogPatternsJSON checks --json prints the API's response, estimate
// included, with no notes on stderr.
func TestFetchLogPatternsJSON(t *testing.T) {
	const body = `{"items":[{"id":"42","template":"t","events":"12"}],"pageInfo":{},"estimated":true,"sampleShare":0.25,"totalEvents":"480"}`
	client, _ := projectAPI(t, "/v0/projects/p1/log-patterns", http.StatusOK, body)
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchLogPatterns(context.Background(), client, "p1", logPatternRead{}, true))
	assert.JSONEq(t, body, stdout.String())
	assert.Empty(t, stderr.String())
}

// TestFetchLogPatternsErrors checks a non-admin is told the read requires
// being a project admin, and user data recording being off names the
// settings page.
func TestFetchLogPatternsErrors(t *testing.T) {
	client, _ := projectAPI(t, "/v0/projects/p1/log-patterns", http.StatusForbidden, `{"code":7,"message":"this needs observability:read on the project"}`)
	err := fetchLogPatterns(context.Background(), client, "p1", logPatternRead{}, false)
	require.ErrorContains(t, err, "reading a project's log patterns requires being a project admin")

	client, _ = projectAPI(t, "/v0/projects/p1/log-patterns", http.StatusBadRequest, `{"code":9,"message":"off","details":[`+
		`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/settings"}]}`)
	err = fetchLogPatterns(context.Background(), client, "p1", logPatternRead{}, false)
	require.ErrorContains(t, err, "the project has no log patterns: user data recording is off")
	require.ErrorContains(t, err, "https://cloud.example/settings")
}

func TestInsightsCommand(t *testing.T) {
	analyticsCmd := findCommandByName(AnalyticsCommands, "analytics")
	require.NotNil(t, analyticsCmd)
	insightsCmd := findCommandByName(analyticsCmd.Commands, "insights")
	require.NotNil(t, insightsCmd, "'analytics insights' command must exist")
	require.NotNil(t, insightsCmd.Action)
	for _, name := range []string{"since", "start", "end", "agent-id", "agent-name", "json"} {
		assert.NotNil(t, findFlagByName(insightsCmd.Flags, name), "--%s", name)
	}

	err := runAnalytics("--experimental", "insights")
	require.ErrorContains(t, err, "only available under --user-auth")
}

func TestAgentInsightsOptions(t *testing.T) {
	opts, err := cmdOptions(t, analyticsInsightsFlags(), agentInsightsOptions)
	require.NoError(t, err)
	assert.Equal(t, agentInsightsRead{AgentInsightsOptions: public.AgentInsightsOptions{AgentIDs: []string{}, AgentNames: []string{}}, defaultRange: true}, opts)

	opts, err = cmdOptions(t, analyticsInsightsFlags(), agentInsightsOptions,
		"--start", "2026-11-01", "--end", "2026-11-08", "--agent-id", "CA_1", "--agent-id", "CA_2", "--agent-name", "self-hosted")
	require.NoError(t, err)
	assert.Equal(t, agentInsightsRead{AgentInsightsOptions: public.AgentInsightsOptions{
		Range:      public.TimeRange{Start: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)},
		AgentIDs:   []string{"CA_1", "CA_2"},
		AgentNames: []string{"self-hosted"},
	}}, opts)

	_, err = cmdOptions(t, analyticsInsightsFlags(), agentInsightsOptions, "--agent-name", " ")
	require.ErrorContains(t, err, "agent name must not be empty")
}

// TestFetchAgentInsights prints the totals, the latencies with measurements,
// the agents and the models; and when there is no data, says why.
func TestFetchAgentInsights(t *testing.T) {
	const insights = `{"summary":{"sessions":"2","turns":"3","interruptions":"0","inputTokens":"3303","outputTokens":"189",` +
		`"e2eLatency":{"p50Ms":2465.6,"p95Ms":2470,"p99Ms":2478,"meanMs":2478.2,"maxMs":2478.2,"count":"1"},` +
		`"toolDuration":{"count":"0"}},` +
		`"agents":[{"agentId":"CA_1","agentName":"support","sessions":"2","turns":"3","e2eLatency":{"p50Ms":2465.6,"p95Ms":2470,"count":"1"}},` +
		`{"agentName":"self-hosted","deploymentId":"dep_1","sessions":"1"}],` +
		`"models":[{"component":"MODEL_COMPONENT_LLM","provider":"openai","model":"gpt-4.1","requests":"4","sessions":"1",` +
		`"latency":{"p50Ms":944,"p95Ms":1388.5,"count":"3"},"inputTokens":"3303","outputTokens":"189","cachedInputTokens":"320"},` +
		`{"component":"MODEL_COMPONENT_TTS","provider":"cartesia","model":"sonic-3","requests":"2","ttsCharacters":"542"},` +
		`{"component":"MODEL_COMPONENT_STT","provider":"deepgram","model":"nova-3","sttAudioSeconds":34.2}]}`
	const none = `{"summary":{"sessions":"0","e2eLatency":{"count":"0"}},"agents":[],"models":[]}`
	tests := []struct {
		name       string
		body       string
		opts       agentInsightsRead
		wantQuery  url.Values
		wantOut    []string
		notOut     []string
		wantStatus string
	}{
		{
			name:      "insights",
			body:      insights,
			opts:      agentInsightsRead{AgentInsightsOptions: public.AgentInsightsOptions{AgentIDs: []string{"CA_1"}}},
			wantQuery: url.Values{"agentIds": {"CA_1"}},
			wantOut: []string{
				"3303", "End-to-end", "2466ms", "2478ms",
				"support", "CA_1", "production", "self-hosted", "dep_1",
				"gpt-4.1", "944ms", "1388ms", "3303 in, 189 out tokens (320 cached)", "542 characters", "34.2s of audio",
			},
			notOut: []string{"Tool call duration"},
		},
		{
			name:       "none in the default range",
			body:       none,
			opts:       agentInsightsRead{defaultRange: true},
			wantQuery:  url.Values{},
			wantStatus: "No agent data in the last 7 days",
		},
		{
			name:       "none for the agents",
			body:       `{}`,
			opts:       agentInsightsRead{AgentInsightsOptions: public.AgentInsightsOptions{AgentNames: []string{"gone"}}},
			wantQuery:  url.Values{"agentNames": {"gone"}},
			wantStatus: "No data in the range for the agents asked for",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, query := projectAPI(t, "/v0/projects/p1/agent-insights", http.StatusOK, tt.body)
			stdout, stderr := captureOut(t)

			require.NoError(t, fetchAgentInsights(context.Background(), client, "p1", tt.opts, false))

			assert.Equal(t, tt.wantQuery, *query)
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
			for _, not := range tt.notOut {
				assert.NotContains(t, stdout.String(), not)
			}
			if len(tt.wantOut) == 0 {
				assert.Empty(t, stdout.String())
			}
			if tt.wantStatus != "" {
				assert.Contains(t, stderr.String(), tt.wantStatus)
			}
		})
	}
}

// TestFetchAgentInsightsJSON checks --json prints the API's response, and
// still explains no data on stderr.
func TestFetchAgentInsightsJSON(t *testing.T) {
	const body = `{"summary":{"sessions":"0"},"agents":[],"models":[]}`
	client, _ := projectAPI(t, "/v0/projects/p1/agent-insights", http.StatusOK, body)
	stdout, stderr := captureOut(t)
	require.NoError(t, fetchAgentInsights(context.Background(), client, "p1", agentInsightsRead{}, true))
	assert.JSONEq(t, body, stdout.String())
	assert.Contains(t, stderr.String(), "No agent data in the range")
}

// TestFetchAgentInsightsErrors checks a caller who isn't a project member is
// told so, not that the read needs an admin, and a refused range passes
// through.
func TestFetchAgentInsightsErrors(t *testing.T) {
	client, _ := projectAPI(t, "/v0/projects/p1/agent-insights", http.StatusForbidden, `{"code":7,"message":"not authorized"}`)
	err := fetchAgentInsights(context.Background(), client, "p1", agentInsightsRead{}, false)
	require.ErrorContains(t, err, "you don't have access to this project")
	assert.NotContains(t, err.Error(), "admin")

	client, _ = projectAPI(t, "/v0/projects/p1/agent-insights", http.StatusBadRequest, `{"code":3,"message":"the time range can be at most 30 days"}`)
	err = fetchAgentInsights(context.Background(), client, "p1", agentInsightsRead{}, false)
	require.EqualError(t, err, "the time range can be at most 30 days")
}
