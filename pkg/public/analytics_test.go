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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
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

// TestGetSessionReturnsDetail checks GetSession hands back the detail alongside
// the list row, and a nil detail while the server is still finalizing it.
func TestGetSessionReturnsDetail(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDetail bool
	}{
		{
			name: "with detail",
			body: `{"session":{"sessionId":"RM_1"},"detail":{"connectionSeconds":"90",` +
				`"participants":[{"participantIdentity":"alice"}],"participantsPage":{"nextCursor":"next","hasMore":true}}}`,
			wantDetail: true,
		},
		{name: "detail still finalizing", body: `{"session":{"sessionId":"RM_1"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			srv := jsonServer(t, http.StatusOK, tt.body, &gotAuth, &gotPath)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			session, detail, err := c.GetSession(context.Background(), "p1", "RM_1")
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v1/projects/p1/sessions/RM_1", gotPath)
			assert.Equal(t, "RM_1", *session.SessionId)
			if !tt.wantDetail {
				assert.Nil(t, detail)
				return
			}
			require.NotNil(t, detail)
			assert.Equal(t, "90", *detail.ConnectionSeconds)
			require.Len(t, *detail.Participants, 1)
			assert.Equal(t, "alice", *(*detail.Participants)[0].ParticipantIdentity)
			assert.Equal(t, "next", pageCursor(detail.ParticipantsPage))
		})
	}
}

// TestListSessionParticipantsQuery checks the request ListSessionParticipants
// sends for each option, and the page it returns.
func TestListSessionParticipantsQuery(t *testing.T) {
	tests := []struct {
		name string
		opts ParticipantListOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "paging",
			opts: ParticipantListOptions{PageOptions: PageOptions{Limit: 25, Cursor: "abc"}},
			want: url.Values{"page.pageSize": {"25"}, "page.cursor": {"abc"}},
		},
		{
			name: "sort by joined",
			opts: ParticipantListOptions{SortBy: "joined", SortOrder: "asc"},
			want: url.Values{"sortBy": {"PARTICIPANT_SORT_FIELD_JOINED_AT"}, "sortOrder": {"SORT_ORDER_ASC"}},
		},
		{
			name: "sort by left ignores case",
			opts: ParticipantListOptions{SortBy: "Left"},
			want: url.Values{"sortBy": {"PARTICIPANT_SORT_FIELD_LEFT_AT"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[{"participantIdentity":"alice"}],"pageInfo":{"nextCursor":"next","hasMore":true}}`))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			participants, next, err := c.ListSessionParticipants(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v1/projects/p1/sessions/RM_1/participants", gotPath)
			assert.Equal(t, tt.want, gotQuery)
			require.Len(t, participants, 1)
			assert.Equal(t, "alice", *participants[0].ParticipantIdentity)
			assert.Equal(t, "next", next)
		})
	}
}

// TestListSessionParticipantsRejectsBadOptions confirms a negative limit or a
// bad sort name fails Validate, and fails ListSessionParticipants before any
// request is sent.
func TestListSessionParticipantsRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    ParticipantListOptions
		wantErr string
	}{
		{name: "negative limit", opts: ParticipantListOptions{PageOptions: PageOptions{Limit: -1}}, wantErr: "limit must not be negative"},
		{name: "sort by", opts: ParticipantListOptions{SortBy: "name"}, wantErr: `invalid participant sort "name"`},
		{name: "sort order", opts: ParticipantListOptions{SortOrder: "newest"}, wantErr: `invalid sort order "newest"`},
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

			_, _, err = c.ListSessionParticipants(context.Background(), "p1", "RM_1", tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, called, "no request should be sent")
		})
	}
}

// TestGetSessionMissingSession reports a 200 without a session as an error.
func TestGetSessionMissingSession(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{}`, nil, nil)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, _, err = c.GetSession(context.Background(), "p1", "RM_1")
	require.ErrorContains(t, err, "missing session")
}

// eventsPage is a ListSessionEvents response as the server's REST transcoder
// writes it: a participant joining, with its allowlisted payload, and a room
// event that names no participant.
const eventsPage = `{
  "items": [
    {"type": "PARTICIPANT_JOINED", "timestamp": "2026-10-07T11:00:01.250Z", "participantIdentity": "alice",
     "participantSessionId": "PA_aaaaaaaaaaaa", "payload": {"participantKind": "STANDARD", "connectionType": "UDP"}},
    {"type": "ROOM_ENDED", "timestamp": "2026-10-07T11:05:00Z", "payload": {"reason": "departure timeout"}}
  ],
  "pageInfo": {"nextCursor": "next", "hasMore": true}
}`

// TestListSessionEvents checks the request ListSessionEvents sends for each
// option, and that events come back typed with the page's cursor.
func TestListSessionEvents(t *testing.T) {
	tests := []struct {
		name string
		opts EventOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "types, participant session, order and paging",
			opts: EventOptions{
				PageOptions:          PageOptions{Limit: 25, Cursor: "abc"},
				Types:                []string{"track_published", " Participant_Left "},
				ParticipantSessionID: "PA_aaaaaaaaaaaa",
				SortOrder:            "desc",
			},
			want: url.Values{
				"page.pageSize":        {"25"},
				"page.cursor":          {"abc"},
				"types":                {"TRACK_PUBLISHED", "PARTICIPANT_LEFT"},
				"participantSessionId": {"PA_aaaaaaaaaaaa"},
				"sortOrder":            {"SORT_ORDER_DESC"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(eventsPage))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			page, err := c.ListSessionEvents(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v1/projects/p1/sessions/RM_1/events", gotPath)
			assert.Equal(t, tt.want, gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			require.Len(t, page.Events, 2)
			ev := page.Events[0]
			assert.Equal(t, "PARTICIPANT_JOINED", *ev.Type)
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 1, 250e6, time.UTC).Equal(*ev.Timestamp))
			assert.Equal(t, "alice", *ev.ParticipantIdentity)
			assert.Equal(t, "PA_aaaaaaaaaaaa", *ev.ParticipantSessionId)
			require.NotNil(t, ev.Payload)
			assert.Contains(t, *ev.Payload, "participantKind")
			assert.Nil(t, page.Events[1].ParticipantSessionId, "a room event names no participant session")
		})
	}
}

// TestSessionEventJSON checks an event marshals back to the API's own shape,
// payload included, so --json prints what the server sent.
func TestSessionEventJSON(t *testing.T) {
	const event = `{"type":"API_CALL","timestamp":"2026-10-07T11:00:01Z",` +
		`"payload":{"service":"RoomService","method":"CreateRoom","status":0,"durationNs":"1500000","nested":{"ok":true},"list":["a"]}}`
	var ev oapi.LivekitPublicapiAnalyticsV1SessionEvent
	require.NoError(t, json.Unmarshal([]byte(event), &ev))
	got, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.JSONEq(t, event, string(got))
}

// TestListSessionEventsRejectsBadOptions confirms unknown types and sort
// orders, a participant session id that isn't one, and a negative limit fail
// Validate, and fail ListSessionEvents before any request is sent.
func TestListSessionEventsRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    EventOptions
		wantErr string
	}{
		{name: "negative limit", opts: EventOptions{PageOptions: PageOptions{Limit: -1}}, wantErr: "limit must not be negative"},
		{
			name:    "unknown type",
			opts:    EventOptions{Types: []string{"participant_joined", "joined"}},
			wantErr: `invalid event type "joined" (expected one of `,
		},
		{name: "blank type", opts: EventOptions{Types: []string{" "}}, wantErr: `invalid event type " "`},
		{
			name:    "identity for participant session",
			opts:    EventOptions{ParticipantSessionID: "alice"},
			wantErr: `invalid participant session id "alice" (expected a PA_ id`,
		},
		{name: "sort order", opts: EventOptions{SortOrder: "newest"}, wantErr: `invalid sort order "newest"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.opts.Validate(), tt.wantErr)

			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.ListSessionEvents(context.Background(), "p1", "RM_1", tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, called, "no request should be sent")
		})
	}
}

// TestEventTypeNames checks the friendly names are the API's in lowercase,
// sorted, and include the dashboard's defaults and track events.
func TestEventTypeNames(t *testing.T) {
	names := EventTypeNames()
	assert.IsIncreasing(t, names)
	for _, want := range []string{"participant_joined", "room_ended", "api_call", "track_published"} {
		assert.Contains(t, names, want)
	}
	assert.Equal(t, "track_published", EventTypeName("TRACK_PUBLISHED"))
}

// TestListSessionEventsUnknownSession checks an unknown session is NotFound.
func TestListSessionEventsUnknownSession(t *testing.T) {
	srv := jsonServer(t, http.StatusNotFound, `{"code":5,"message":"session not found"}`, nil, nil)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, err = c.ListSessionEvents(context.Background(), "p1", "RM_1", EventOptions{})
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}
