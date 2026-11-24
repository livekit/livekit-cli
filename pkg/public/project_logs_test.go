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

package public

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queryServer starts a stand-in Public API that answers every request with
// status and body, recording the request's auth header, path and query.
func queryServer(t *testing.T, status int, body string) (c *Client, gotAuth, gotPath *string, gotQuery *url.Values) {
	t.Helper()
	gotAuth, gotPath, gotQuery = new(string), new(string), new(url.Values)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth, *gotPath, *gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)
	return c, gotAuth, gotPath, gotQuery
}

// noRequestServer starts a stand-in Public API that records whether it was
// called, for checks that must fail before any request is sent.
func noRequestServer(t *testing.T) (*Client, *bool) {
	t.Helper()
	called := new(bool)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { *called = true }))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)
	return c, called
}

const projectLogsPage = `{"items":[` +
	`{"id":"log_1","timestamp":"2026-11-21T00:20:35.208Z","level":"LOG_LEVEL_ERROR","logger":"livekit.agents",` +
	`"message":"tts failed","sessionId":"RM_1","source":"LOG_SOURCE_AGENT"},` +
	`{"id":"log_2","timestamp":"2026-11-21T00:20:30Z","level":"LOG_LEVEL_INFO","message":"API_CALL","source":"LOG_SOURCE_PLATFORM"}],` +
	`"pageInfo":{"nextCursor":"next","hasMore":true}}`

func TestListProjectLogs(t *testing.T) {
	start := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 21, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts ProjectLogOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "every filter, order and paging",
			opts: ProjectLogOptions{
				PageOptions: PageOptions{Limit: 25, Cursor: "abc"},
				Range:       TimeRange{Start: start, End: end},
				Levels:      []string{"warn", " ERROR "},
				Sources:     []string{"agent", "Platform"},
				SessionID:   "RM_1",
				Query:       `@lk.cloud_agent_id:CA_1 "timed out"`,
				SortOrder:   "asc",
			},
			want: url.Values{
				"page.pageSize":   {"25"},
				"page.cursor":     {"abc"},
				"range.startTime": {"2026-11-20T00:00:00Z"},
				"range.endTime":   {"2026-11-21T12:30:00Z"},
				"logLevels":       {"LOG_LEVEL_WARN", "LOG_LEVEL_ERROR"},
				"sources":         {"LOG_SOURCE_AGENT", "LOG_SOURCE_PLATFORM"},
				"sessionId":       {"RM_1"},
				"query":           {`@lk.cloud_agent_id:CA_1 "timed out"`},
				"sortOrder":       {"SORT_ORDER_ASC"},
			},
		},
		{
			name: "only an end",
			opts: ProjectLogOptions{Range: TimeRange{End: end}},
			want: url.Values{"range.endTime": {"2026-11-21T12:30:00Z"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, gotAuth, gotPath, gotQuery := queryServer(t, http.StatusOK, projectLogsPage)

			page, err := c.ListProjectLogs(context.Background(), "p1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", *gotAuth)
			assert.Equal(t, "/v0/projects/p1/logs", *gotPath)
			assert.Equal(t, tt.want, *gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			require.Len(t, page.Records, 2)
			assert.Equal(t, "RM_1", *page.Records[0].SessionId)
			assert.Equal(t, "LOG_SOURCE_PLATFORM", string(*page.Records[1].Source))
			assert.Nil(t, page.Records[1].SessionId, "a record of no session")
		})
	}
}

// TestListProjectLogsRejectsBadOptions confirms unknown levels, sources and
// sort orders and a negative limit fail Validate, and fail ListProjectLogs
// before any request is sent.
func TestListProjectLogsRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    ProjectLogOptions
		wantErr string
	}{
		{name: "negative limit", opts: ProjectLogOptions{PageOptions: PageOptions{Limit: -1}}, wantErr: "limit must not be negative"},
		{name: "unknown level", opts: ProjectLogOptions{Levels: []string{"loud"}}, wantErr: `invalid log level "loud"`},
		{
			name:    "unknown source",
			opts:    ProjectLogOptions{Sources: []string{"agent", "system"}},
			wantErr: `invalid log source "system" (expected agent, client or platform)`,
		},
		{name: "sort order", opts: ProjectLogOptions{SortOrder: "newest"}, wantErr: `invalid sort order "newest"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.opts.Validate(), tt.wantErr)

			c, called := noRequestServer(t)
			_, err := c.ListProjectLogs(context.Background(), "p1", tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, *called, "no request should be sent")
		})
	}
}

// TestListProjectLogsErrors checks a non-admin is PermissionDenied and an
// empty unfiltered read with user data recording off carries
// ObservabilityDisabled.
func TestListProjectLogsErrors(t *testing.T) {
	t.Run("not an admin", func(t *testing.T) {
		c, _, _, _ := queryServer(t, http.StatusForbidden, `{"code":7,"message":"not authorized"}`)
		_, err := c.ListProjectLogs(context.Background(), "p1", ProjectLogOptions{})
		assert.True(t, IsPermissionDenied(err))
		assert.EqualError(t, err, "not authorized")
	})
	t.Run("invalid query", func(t *testing.T) {
		c, _, _, _ := queryServer(t, http.StatusBadRequest, `{"code":3,"message":"query isn't valid log search syntax"}`)
		_, err := c.ListProjectLogs(context.Background(), "p1", ProjectLogOptions{Query: "lk.cloud_agent_id:CA_1"})
		assert.True(t, InvalidLogQuery(err))
		assert.False(t, InvalidLogQuery(&APIError{Status: http.StatusBadRequest, Message: "range starts in the future"}))
	})
	t.Run("recording off", func(t *testing.T) {
		c, _, _, _ := queryServer(t, http.StatusBadRequest, `{"code":9,"message":"user data recording is off","details":[`+
			`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/settings"}]}`)
		_, err := c.ListProjectLogs(context.Background(), "p1", ProjectLogOptions{})
		url, ok := ObservabilityDisabled(err)
		assert.True(t, ok)
		assert.Equal(t, "https://cloud.example/settings", url)
	})
}
