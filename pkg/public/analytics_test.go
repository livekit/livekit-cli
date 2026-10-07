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

// TestListProjectSessionsQuery checks the query string ListProjectSessions sends
// for each option, through the generated client's own encoding.
func TestListProjectSessionsQuery(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		opts SessionListOptions
		want url.Values
	}{
		{
			name: "zero options send nothing",
			want: url.Values{},
		},
		{
			name: "paging",
			opts: SessionListOptions{Limit: 25, Cursor: "abc"},
			want: url.Values{"page.pageSize": {"25"}, "page.cursor": {"abc"}},
		},
		{
			name: "time range",
			opts: SessionListOptions{Start: start, End: end},
			want: url.Values{
				"filter.range.startTime": {"2026-10-01T00:00:00Z"},
				"filter.range.endTime":   {"2026-10-03T00:00:00Z"},
			},
		},
		{
			name: "statuses repeat and ignore case",
			opts: SessionListOptions{Statuses: []string{"Active", "closed"}},
			want: url.Values{"filter.statuses": {"SESSION_STATUS_ACTIVE", "SESSION_STATUS_CLOSED"}},
		},
		{
			name: "room prefix and tags",
			opts: SessionListOptions{RoomPrefix: "demo-", Tags: []string{"a", "b"}},
			want: url.Values{"filter.roomName": {"demo-"}, "filter.tags": {"a", "b"}},
		},
		{
			name: "sort order",
			opts: SessionListOptions{SortOrder: "asc"},
			want: url.Values{"sortOrder": {"SORT_ORDER_ASC"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery = r.URL.Path, r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[{"sessionId":"RM_1"}],"pageInfo":{"nextCursor":"next"}}`))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			sessions, next, err := c.ListProjectSessions(context.Background(), "p1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "/v1/projects/p1/sessions", gotPath)
			assert.Equal(t, tt.want, gotQuery)
			require.Len(t, sessions, 1)
			assert.Equal(t, "RM_1", *sessions[0].SessionId)
			assert.Equal(t, "next", next)
		})
	}
}

// TestListProjectSessionsRejectsUnknownNames confirms a bad status or sort order
// fails Validate, and fails ListProjectSessions before any request is sent.
func TestListProjectSessionsRejectsUnknownNames(t *testing.T) {
	tests := []struct {
		name    string
		opts    SessionListOptions
		wantErr string
	}{
		{name: "status", opts: SessionListOptions{Statuses: []string{"active", "bogus"}}, wantErr: `invalid session status "bogus"`},
		{name: "sort order", opts: SessionListOptions{SortOrder: "newest"}, wantErr: `invalid sort order "newest"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.opts.Validate(), tt.wantErr)

			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, _, err = c.ListProjectSessions(context.Background(), "p1", tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, called, "no request should be sent")
		})
	}
}
