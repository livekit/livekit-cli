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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// TestGetSessionRecordingURL checks the path and file type GetSessionRecordingURL
// sends for each recording type, and that it hands back the signed URL and its
// times.
func TestGetSessionRecordingURL(t *testing.T) {
	tests := []struct {
		recording string
		want      string
	}{
		{recording: "audio", want: "RECORDING_FILE_TYPE_AUDIO"},
		{recording: "chat-history", want: "RECORDING_FILE_TYPE_CHAT_HISTORY"},
		{recording: " Chat-History ", want: "RECORDING_FILE_TYPE_CHAT_HISTORY"},
	}
	for _, tt := range tests {
		t.Run(tt.recording, func(t *testing.T) {
			var gotPath, gotAuth string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.Query(), r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"url":"https://bucket.example/rec?sig=x","expiresAt":"2026-10-07T12:15:00Z","recordingStartedAt":"2026-10-07T11:00:00Z"}`))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			res, err := c.GetSessionRecordingURL(context.Background(), "p1", "RM_1", RecordingURLOptions{Recording: tt.recording})
			require.NoError(t, err)

			assert.Equal(t, "/v0/projects/p1/sessions/RM_1/recording-url", gotPath)
			assert.Equal(t, url.Values{"fileType": {tt.want}}, gotQuery)
			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "https://bucket.example/rec?sig=x", *res.Url)
			assert.True(t, time.Date(2026, 10, 7, 12, 15, 0, 0, time.UTC).Equal(*res.ExpiresAt))
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC).Equal(*res.RecordingStartedAt))
		})
	}
}

// TestGetSessionRecordingURLRejectsUnknownRecording confirms a missing or
// unknown recording name fails Validate, and fails GetSessionRecordingURL
// before any request is sent.
func TestGetSessionRecordingURLRejectsUnknownRecording(t *testing.T) {
	tests := []struct {
		recording string
		wantErr   string
	}{
		{recording: "", wantErr: `recording type is required ("audio" or "chat-history")`},
		{recording: "transcript", wantErr: `invalid recording type "transcript" (expected "audio" or "chat-history")`},
	}
	for _, tt := range tests {
		t.Run(tt.recording, func(t *testing.T) {
			opts := RecordingURLOptions{Recording: tt.recording}
			require.EqualError(t, opts.Validate(), tt.wantErr)

			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionRecordingURL(context.Background(), "p1", "RM_1", opts)
			require.EqualError(t, err, tt.wantErr)
			assert.False(t, called, "no request is sent for a bad recording type")
		})
	}
}

// TestGetSessionRecordingURLMissingURL guards a 200 without a URL, which would
// otherwise download from "".
func TestGetSessionRecordingURLMissingURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, err = c.GetSessionRecordingURL(context.Background(), "p1", "RM_1", RecordingURLOptions{Recording: "audio"})
	require.EqualError(t, err, "unexpected response from server: missing url")
}

// TestGetSessionRecordingURLErrors checks the two "nothing to download"
// answers keep what the caller needs: a missing recording is NotFound, and a
// missing recording with user data recording off carries the dashboard link.
// The bodies are what the server's REST transcoder writes (a google.rpc.Status).
func TestGetSessionRecordingURLErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNotFound bool
		wantDisabled bool
		wantURL      string
	}{
		{
			name:         "no recording",
			status:       http.StatusNotFound,
			body:         `{"code":5,"message":"recording not found","details":[]}`,
			wantNotFound: true,
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off for this project, so nothing was captured to read","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/projects/p1/settings/observability"}]}`,
			wantDisabled: true,
			wantURL:      "https://cloud.example/projects/p1/settings/observability",
		},
		{
			name:   "another failed precondition",
			status: http.StatusBadRequest,
			body:   `{"code":9,"message":"something else","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"X"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionRecordingURL(context.Background(), "p1", "RM_1", RecordingURLOptions{Recording: "audio"})
			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, IsNotFound(err))
			dashboardURL, disabled := ObservabilityDisabled(err)
			assert.Equal(t, tt.wantDisabled, disabled)
			assert.Equal(t, tt.wantURL, dashboardURL)
		})
	}
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestDownloadRecording checks a signed URL is fetched without credentials and
// its body copied as served, except that a gzip body is decompressed whether or
// not the transport already did it.
func TestDownloadRecording(t *testing.T) {
	const chat = `{"items":[{"type":"message","role":"user","content":["hi"]}]}`
	tests := []struct {
		name     string
		header   http.Header
		body     []byte
		wantBody string
	}{
		{
			name:     "audio as served",
			header:   http.Header{"Content-Type": {"audio/ogg"}},
			body:     []byte("OggS\x00audio"),
			wantBody: "OggS\x00audio",
		},
		{
			name:     "chat history with Content-Encoding gzip",
			header:   http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
			body:     gzipped(t, chat),
			wantBody: chat,
		},
		{
			name:     "chat history gzip without the header",
			header:   http.Header{"Content-Type": {"application/octet-stream"}},
			body:     gzipped(t, chat),
			wantBody: chat,
		},
		{
			name:     "empty body",
			wantBody: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Values("Authorization")
				for k, v := range tt.header {
					w.Header()[k] = v
				}
				_, _ = w.Write(tt.body)
			}))
			t.Cleanup(srv.Close)

			var buf bytes.Buffer
			n, err := DownloadRecording(context.Background(), srv.URL+"/rec?X-Amz-Signature=abc", &buf)
			require.NoError(t, err)
			assert.Equal(t, tt.wantBody, buf.String())
			assert.Equal(t, int64(len(tt.wantBody)), n)
			assert.Empty(t, gotAuth, "a signed URL carries its own authorization")
		})
	}
}

// TestDownloadRecordingRefused checks a refused signed URL (expired, or the
// object gone) is an error naming the status, not a saved error page.
func TestDownloadRecordingRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>`))
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	_, err := DownloadRecording(context.Background(), srv.URL+"/rec", &buf)
	require.ErrorContains(t, err, "HTTP 403")
	require.ErrorContains(t, err, "Request has expired")
	assert.Empty(t, buf.String())
}

// TestDownloadRecordingSendsNoCredentials checks neither a cookie nor an
// Authorization header reaches the object store, before or after a redirect,
// even when the store sets a cookie on the way and the process's default
// client holds cookies for it.
func TestDownloadRecordingSendsNoCredentials(t *testing.T) {
	type seen struct{ cookie, auth []string }
	var final seen
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		final = seen{r.Header.Values("Cookie"), r.Header.Values("Authorization")}
		_, _ = w.Write([]byte("OggS"))
	}))
	t.Cleanup(target.Close)
	var first seen
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first = seen{r.Header.Values("Cookie"), r.Header.Values("Authorization")}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "from-store"})
		http.Redirect(w, r, target.URL+"/rec?X-Amz-Signature=def", http.StatusFound)
	}))
	t.Cleanup(store.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	for _, srv := range []string{store.URL, target.URL} {
		u, err := url.Parse(srv)
		require.NoError(t, err)
		jar.SetCookies(u, []*http.Cookie{{Name: "elsewhere", Value: "secret"}})
	}
	prevJar := http.DefaultClient.Jar
	http.DefaultClient.Jar = jar
	t.Cleanup(func() { http.DefaultClient.Jar = prevJar })

	var buf bytes.Buffer
	_, err = DownloadRecording(context.Background(), store.URL+"/rec?X-Amz-Signature=abc", &buf)
	require.NoError(t, err)
	assert.Equal(t, "OggS", buf.String())
	assert.Equal(t, seen{}, first, "the first request carries no credentials")
	assert.Equal(t, seen{}, final, "the redirected request carries no credentials")
}

// TestDownloadRecordingErrorsHideSignature checks a failed download's error
// names where it went wrong without the signed URL's query string, whether the
// store couldn't be reached, before or after a redirect, or echoed the
// signature in its error page.
func TestDownloadRecordingErrorsHideSignature(t *testing.T) {
	const sig = "0123456789abcdef0123456789abcdef"
	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := unreachable.URL
	unreachable.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, unreachableURL+"/rec?X-Amz-Signature="+sig, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code><SignatureProvided>` + sig + `</SignatureProvided></Error>`))
	}))
	t.Cleanup(echo.Close)

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "unreachable", url: unreachableURL + "/rec?X-Amz-Signature=" + sig, wantErr: unreachableURL + "/rec"},
		{name: "unreachable after a redirect", url: redirect.URL + "/rec?X-Amz-Signature=" + sig, wantErr: unreachableURL + "/rec"},
		{name: "signature echoed", url: echo.URL + "/rec?X-Amz-Signature=" + sig, wantErr: "SignatureDoesNotMatch"},
		{name: "malformed", url: "http://[::1/rec?X-Amz-Signature=" + sig, wantErr: "download recording"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DownloadRecording(context.Background(), tt.url, &bytes.Buffer{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotContains(t, err.Error(), sig)
			assert.NotContains(t, err.Error(), "X-Amz-Signature")
		})
	}
}

// transcriptPage is a GetSessionTranscript response with one item of each
// kind, as the server's REST transcoder writes it.
const transcriptPage = `{
  "items": [
    {"id": "item_1", "timestamp": "2026-10-07T11:00:01Z", "message": {"role": "ROLE_USER", "text": "hi", "transcriptConfidence": 0.9, "endOfTurnDelayMs": 320}},
    {"id": "item_2", "timestamp": "2026-10-07T11:00:02Z", "message": {"role": "ROLE_AGENT", "text": "hello", "interrupted": true, "e2eLatencyMs": 820.5}},
    {"id": "item_3", "timestamp": "2026-10-07T11:00:03Z", "toolCall": {"name": "lookup", "callId": "c1", "arguments": "{\"q\":1}"}},
    {"id": "item_4", "timestamp": "2026-10-07T11:00:04Z", "toolResult": {"name": "lookup", "callId": "c1", "output": "boom", "isError": true}},
    {"id": "item_5", "timestamp": "2026-10-07T11:00:05Z", "agentHandoff": {"toAgentId": "triage"}},
    {"id": "item_6", "timestamp": "2026-10-07T11:00:06Z", "configUpdate": {"toolsAdded": ["lookup"]}}
  ],
  "pageInfo": {"nextCursor": "next", "hasMore": true},
  "skippedRecords": 2
}`

// TestGetSessionTranscript checks the request GetSessionTranscript sends for
// each option, and that every item kind comes back typed with the page's
// cursor and skipped record count.
func TestGetSessionTranscript(t *testing.T) {
	tests := []struct {
		name string
		opts PageOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "paging",
			opts: PageOptions{Limit: 25, Cursor: "abc"},
			want: url.Values{"page.pageSize": {"25"}, "page.cursor": {"abc"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(transcriptPage))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			page, err := c.GetSessionTranscript(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v0/projects/p1/sessions/RM_1/transcript", gotPath)
			assert.Equal(t, tt.want, gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			assert.Equal(t, 2, page.SkippedRecords)
			require.Len(t, page.Items, 6)

			user := page.Items[0]
			assert.Equal(t, "item_1", user.ID)
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 1, 0, time.UTC).Equal(*user.Timestamp))
			require.NotNil(t, user.Message)
			assert.Equal(t, "ROLE_USER", string(*user.Message.Role))
			assert.Equal(t, 320.0, *user.Message.EndOfTurnDelayMs)

			agent := page.Items[1].Message
			require.NotNil(t, agent)
			assert.True(t, *agent.Interrupted)
			assert.Equal(t, 820.5, *agent.E2eLatencyMs)

			require.NotNil(t, page.Items[2].ToolCall)
			assert.Equal(t, `{"q":1}`, *page.Items[2].ToolCall.Arguments)
			require.NotNil(t, page.Items[3].ToolResult)
			assert.True(t, *page.Items[3].ToolResult.IsError)
			require.NotNil(t, page.Items[4].AgentHandoff)
			assert.Equal(t, "triage", *page.Items[4].AgentHandoff.ToAgentId)
			require.NotNil(t, page.Items[5].ConfigUpdate)
			assert.Equal(t, []string{"lookup"}, *page.Items[5].ConfigUpdate.ToolsAdded)
		})
	}
}

// TestTranscriptItemJSON checks an item marshals back to the API's own shape,
// so --json prints what the server sent.
func TestTranscriptItemJSON(t *testing.T) {
	const item = `{"id":"item_5","timestamp":"2026-10-07T11:00:05Z","agentHandoff":{"toAgentId":"triage"}}`
	var it TranscriptItem
	require.NoError(t, json.Unmarshal([]byte(item), &it))
	got, err := json.Marshal(it)
	require.NoError(t, err)
	assert.JSONEq(t, item, string(got))
}

// TestGetSessionTranscriptRejectsBadLimit confirms a negative limit fails
// Validate, and fails GetSessionTranscript before any request is sent.
func TestGetSessionTranscriptRejectsBadLimit(t *testing.T) {
	opts := PageOptions{Limit: -1}
	require.EqualError(t, opts.Validate(), "limit must not be negative")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, err = c.GetSessionTranscript(context.Background(), "p1", "RM_1", opts)
	require.EqualError(t, err, "limit must not be negative")
	assert.False(t, called, "no request should be sent")
}

// TestGetSessionTranscriptErrors checks an unknown session is NotFound and an
// empty transcript with user data recording off carries ObservabilityDisabled.
func TestGetSessionTranscriptErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNotFound bool
		wantDisabled bool
	}{
		{
			name:         "unknown session",
			status:       http.StatusNotFound,
			body:         `{"code":5,"message":"session not found"}`,
			wantNotFound: true,
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/p1"}]}`,
			wantDisabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionTranscript(context.Background(), "p1", "RM_1", PageOptions{})
			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, IsNotFound(err))
			_, disabled := ObservabilityDisabled(err)
			assert.Equal(t, tt.wantDisabled, disabled)
		})
	}
}

// logsPage is a GetSessionLogs response as the server's REST transcoder writes
// it: a structured record with body fields, typed attributes and its span, and
// an evaluation result with no level.
const logsPage = `{
  "records": [
    {
      "id": "log_1",
      "timestamp": "2026-10-07T11:00:01.250Z",
      "level": "LOG_LEVEL_WARN",
      "severityText": "WARNING",
      "logger": "livekit.agents",
      "message": "slow tts",
      "bodyFields": {"ttfb": 1.5},
      "attributes": {"logger.name": "livekit.agents", "retry": 2, "tags": ["a", "b"], "nested": {"ok": true}},
      "traceId": "0af7651916cd43dd8448eb211c80319c",
      "spanId": "b7ad6b7169203331"
    },
    {"id": "log_2", "timestamp": "2026-10-07T11:00:02Z", "message": "evaluation passed"}
  ],
  "pageInfo": {"nextCursor": "next", "hasMore": true}
}`

// TestGetSessionLogs checks the request GetSessionLogs sends for each option,
// and that records come back typed with the page's cursor.
func TestGetSessionLogs(t *testing.T) {
	tests := []struct {
		name string
		opts LogOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "levels, order and paging",
			opts: LogOptions{PageOptions: PageOptions{Limit: 25, Cursor: "abc"}, Levels: []string{"warn", " ERROR ", "critical"}, SortOrder: "desc"},
			want: url.Values{
				"page.pageSize": {"25"},
				"page.cursor":   {"abc"},
				"logLevels":     {"LOG_LEVEL_WARN", "LOG_LEVEL_ERROR", "LOG_LEVEL_FATAL"},
				"sortOrder":     {"SORT_ORDER_DESC"},
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
				_, _ = w.Write([]byte(logsPage))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			page, err := c.GetSessionLogs(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v0/projects/p1/sessions/RM_1/logs", gotPath)
			assert.Equal(t, tt.want, gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			require.Len(t, page.Records, 2)
			rec := page.Records[0]
			assert.Equal(t, "log_1", *rec.Id)
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 1, 250e6, time.UTC).Equal(*rec.Timestamp))
			assert.Equal(t, "LOG_LEVEL_WARN", string(*rec.Level))
			assert.Equal(t, "WARNING", *rec.SeverityText)
			assert.Equal(t, "livekit.agents", *rec.Logger)
			assert.Equal(t, "slow tts", *rec.Message)
			assert.Equal(t, "b7ad6b7169203331", *rec.SpanId)
			assert.Nil(t, page.Records[1].Level, "a record with no level")
		})
	}
}

// TestLogRecordJSON checks a record marshals back to the API's own shape,
// typed attributes and body fields included, so --json prints what the server
// sent.
func TestLogRecordJSON(t *testing.T) {
	const record = `{"id":"log_1","timestamp":"2026-10-07T11:00:01Z","level":"LOG_LEVEL_INFO","message":"hi",` +
		`"bodyFields":{"ttfb":1.5},"attributes":{"retry":2,"tags":["a","b"],"nested":{"ok":true},"none":null}}`
	var rec oapi.LivekitPublicapiObservabilityV1LogRecord
	require.NoError(t, json.Unmarshal([]byte(record), &rec))
	got, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.JSONEq(t, record, string(got))
}

// TestGetSessionLogsRejectsBadOptions confirms unknown levels and sort orders
// and a negative limit fail Validate, and fail GetSessionLogs before any
// request is sent.
func TestGetSessionLogsRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    LogOptions
		wantErr string
	}{
		{name: "negative limit", opts: LogOptions{PageOptions: PageOptions{Limit: -1}}, wantErr: "limit must not be negative"},
		{
			name:    "unknown level",
			opts:    LogOptions{Levels: []string{"info", "loud"}},
			wantErr: `invalid log level "loud" (expected trace, debug, info, warn, error or fatal)`,
		},
		{name: "unspecified level", opts: LogOptions{Levels: []string{"unspecified"}}, wantErr: `invalid log level "unspecified"`},
		{name: "sort order", opts: LogOptions{SortOrder: "newest"}, wantErr: `invalid sort order "newest"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.opts.Validate(), tt.wantErr)

			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionLogs(context.Background(), "p1", "RM_1", tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, called, "no request should be sent")
		})
	}
}

// TestGetSessionLogsErrors checks an unknown session is NotFound and an empty
// unfiltered read with user data recording off carries ObservabilityDisabled.
func TestGetSessionLogsErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNotFound bool
		wantDisabled bool
	}{
		{
			name:         "unknown session",
			status:       http.StatusNotFound,
			body:         `{"code":5,"message":"session not found"}`,
			wantNotFound: true,
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/p1"}]}`,
			wantDisabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionLogs(context.Background(), "p1", "RM_1", LogOptions{})
			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, IsNotFound(err))
			_, disabled := ObservabilityDisabled(err)
			assert.Equal(t, tt.wantDisabled, disabled)
		})
	}
}

// tracesPage is a GetSessionTraces response as the server's REST transcoder
// writes it: a root span with typed attributes, and a child that failed with
// an exception event.
const tracesPage = `{
  "spans": [
    {
      "traceId": "0af7651916cd43dd8448eb211c80319c",
      "spanId": "a1a1a1a1a1a1a1a1",
      "name": "agent_session",
      "kind": "SPAN_KIND_INTERNAL",
      "startTime": "2026-10-07T11:00:00Z",
      "endTime": "2026-10-07T11:05:00Z",
      "status": "SPAN_STATUS_OK",
      "attributes": {"lk.agent_name": "triage", "lk.room_name": "r1", "retries": 2, "nested": {"ok": true}}
    },
    {
      "traceId": "0af7651916cd43dd8448eb211c80319c",
      "spanId": "b2b2b2b2b2b2b2b2",
      "parentSpanId": "a1a1a1a1a1a1a1a1",
      "name": "llm_request",
      "kind": "SPAN_KIND_CLIENT",
      "startTime": "2026-10-07T11:00:01Z",
      "endTime": "2026-10-07T11:00:01.820Z",
      "status": "SPAN_STATUS_ERROR",
      "statusMessage": "rate limited",
      "attributes": {"lk.response.ttft": 0.31},
      "events": [{"name": "exception", "timestamp": "2026-10-07T11:00:01.800Z", "attributes": {"exception.type": "RateLimitError"}}]
    }
  ],
  "pageInfo": {"nextCursor": "next", "hasMore": true}
}`

// TestGetSessionTraces checks the request GetSessionTraces sends for each
// option, and that spans come back typed with the page's cursor.
func TestGetSessionTraces(t *testing.T) {
	tests := []struct {
		name string
		opts PageOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "paging",
			opts: PageOptions{Limit: 100, Cursor: "abc"},
			want: url.Values{"page.pageSize": {"100"}, "page.cursor": {"abc"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tracesPage))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			page, err := c.GetSessionTraces(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v0/projects/p1/sessions/RM_1/traces", gotPath)
			assert.Equal(t, tt.want, gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			require.Len(t, page.Spans, 2)
			root, child := page.Spans[0], page.Spans[1]
			assert.Equal(t, "a1a1a1a1a1a1a1a1", *root.SpanId)
			assert.Nil(t, root.ParentSpanId)
			assert.Equal(t, oapi.SPANKINDINTERNAL, *root.Kind)
			assert.Equal(t, oapi.SPANSTATUSOK, *root.Status)
			assert.True(t, time.Date(2026, 10, 7, 11, 5, 0, 0, time.UTC).Equal(*root.EndTime))

			assert.Equal(t, "a1a1a1a1a1a1a1a1", *child.ParentSpanId)
			assert.Equal(t, "llm_request", *child.Name)
			assert.Equal(t, oapi.SPANSTATUSERROR, *child.Status)
			assert.Equal(t, "rate limited", *child.StatusMessage)
			require.Len(t, *child.Events, 1)
			assert.Equal(t, "exception", *(*child.Events)[0].Name)
		})
	}
}

// TestSpanJSON checks a span marshals back to the API's own shape, typed
// attributes and events included, so --json prints what the server sent.
func TestSpanJSON(t *testing.T) {
	const span = `{"spanId":"b2","parentSpanId":"a1","name":"llm_request","kind":"SPAN_KIND_CLIENT",` +
		`"startTime":"2026-10-07T11:00:01Z","status":"SPAN_STATUS_ERROR",` +
		`"attributes":{"lk.response.ttft":0.31,"tags":["a","b"],"nested":{"ok":true},"none":null},` +
		`"events":[{"name":"exception","attributes":{"exception.type":"RateLimitError"}}]}`
	var s oapi.LivekitPublicapiObservabilityV1Span
	require.NoError(t, json.Unmarshal([]byte(span), &s))
	got, err := json.Marshal(s)
	require.NoError(t, err)
	assert.JSONEq(t, span, string(got))
}

// TestGetSessionTracesRejectsBadLimit confirms a negative limit fails
// Validate, and fails GetSessionTraces before any request is sent.
func TestGetSessionTracesRejectsBadLimit(t *testing.T) {
	opts := PageOptions{Limit: -1}
	require.EqualError(t, opts.Validate(), "limit must not be negative")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, err = c.GetSessionTraces(context.Background(), "p1", "RM_1", opts)
	require.EqualError(t, err, "limit must not be negative")
	assert.False(t, called, "no request should be sent")
}

// TestGetSessionTracesErrors checks an unknown session is NotFound and an
// empty first page with user data recording off carries ObservabilityDisabled.
func TestGetSessionTracesErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNotFound bool
		wantDisabled bool
	}{
		{
			name:         "unknown session",
			status:       http.StatusNotFound,
			body:         `{"code":5,"message":"session not found"}`,
			wantNotFound: true,
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/p1"}]}`,
			wantDisabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionTraces(context.Background(), "p1", "RM_1", PageOptions{})
			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, IsNotFound(err))
			_, disabled := ObservabilityDisabled(err)
			assert.Equal(t, tt.wantDisabled, disabled)
		})
	}
}

// metricsPage is a GetSessionMetrics response as the server's REST transcoder
// writes it: a gauge with no start time whose value is zero, a sum, a
// histogram with its buckets and an exponential histogram with none.
const metricsPage = `{
  "points": [
    {
      "name": "lk.agents.active_sessions",
      "kind": "METRIC_KIND_GAUGE",
      "endTime": "2026-10-07T11:00:30Z",
      "attributes": {"lk.agent_name": "triage"},
      "value": 0
    },
    {
      "name": "lk.agents.usage.llm_input_tokens",
      "unit": "{token}",
      "kind": "METRIC_KIND_SUM",
      "startTime": "2026-10-07T11:00:00Z",
      "endTime": "2026-10-07T11:00:30Z",
      "attributes": {"model_name": "gpt-4o", "room_id": "RM_1"},
      "value": 1520
    },
    {
      "name": "lk.agents.turn.e2e_latency",
      "unit": "s",
      "kind": "METRIC_KIND_HISTOGRAM",
      "startTime": "2026-10-07T11:00:00Z",
      "endTime": "2026-10-07T11:00:30Z",
      "histogram": {"count": "3", "sum": 2.4, "min": 0.6, "max": 1.1, "bucketBounds": [0.5, 1], "bucketCounts": ["0", "2", "1"]}
    },
    {
      "name": "gen_ai.client.operation.duration",
      "unit": "s",
      "kind": "METRIC_KIND_EXPONENTIAL_HISTOGRAM",
      "startTime": "2026-10-07T11:00:00Z",
      "endTime": "2026-10-07T11:00:30Z",
      "histogram": {"count": "2", "sum": 1.5}
    }
  ],
  "pageInfo": {"nextCursor": "next", "hasMore": true}
}`

// TestGetSessionMetrics checks the request GetSessionMetrics sends for each
// option, and that points come back with their value or histogram decoded and
// the page's cursor.
func TestGetSessionMetrics(t *testing.T) {
	tests := []struct {
		name string
		opts MetricOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "names and paging",
			opts: MetricOptions{PageOptions: PageOptions{Limit: 100, Cursor: "abc"}, Names: []string{"lk.agents.turn.e2e_latency", " gen_ai.client.operation.duration "}},
			want: url.Values{
				"page.pageSize": {"100"},
				"page.cursor":   {"abc"},
				"names":         {"lk.agents.turn.e2e_latency", "gen_ai.client.operation.duration"},
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
				_, _ = w.Write([]byte(metricsPage))
			}))
			t.Cleanup(srv.Close)

			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			page, err := c.GetSessionMetrics(context.Background(), "p1", "RM_1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", gotAuth)
			assert.Equal(t, "/v0/projects/p1/sessions/RM_1/metrics", gotPath)
			assert.Equal(t, tt.want, gotQuery)

			assert.Equal(t, "next", page.NextCursor)
			require.Len(t, page.Points, 4)
			gauge, sum, hist, expHist := page.Points[0], page.Points[1], page.Points[2], page.Points[3]

			assert.Equal(t, "lk.agents.active_sessions", gauge.Name)
			assert.Equal(t, oapi.METRICKINDGAUGE, gauge.Kind)
			assert.Nil(t, gauge.StartTime, "a gauge has no start time")
			require.NotNil(t, gauge.Value, "a zero value is still a value")
			assert.Zero(t, *gauge.Value)
			assert.Nil(t, gauge.Histogram)

			assert.Equal(t, "{token}", sum.Unit)
			assert.Equal(t, oapi.METRICKINDSUM, sum.Kind)
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC).Equal(*sum.StartTime))
			assert.True(t, time.Date(2026, 10, 7, 11, 0, 30, 0, time.UTC).Equal(*sum.EndTime))
			assert.Equal(t, 1520.0, *sum.Value)
			assert.Contains(t, sum.Attributes, "model_name")

			assert.Nil(t, hist.Value)
			require.NotNil(t, hist.Histogram)
			assert.Equal(t, "3", *hist.Histogram.Count)
			assert.Equal(t, []float64{0.5, 1}, *hist.Histogram.BucketBounds)
			assert.Equal(t, []string{"0", "2", "1"}, *hist.Histogram.BucketCounts)

			assert.Equal(t, oapi.METRICKINDEXPONENTIALHISTOGRAM, expHist.Kind)
			require.NotNil(t, expHist.Histogram)
			assert.Nil(t, expHist.Histogram.BucketBounds)
			assert.Nil(t, expHist.Histogram.Min, "unset when the agent didn't record it")
		})
	}
}

// TestMetricPointJSON checks a point marshals back to the API's own shape,
// typed attributes and its value or histogram included, so --json prints what
// the server sent.
func TestMetricPointJSON(t *testing.T) {
	for _, point := range []string{
		`{"name":"lk.agents.active_sessions","kind":"METRIC_KIND_GAUGE","endTime":"2026-10-07T11:00:30Z","value":0}`,
		`{"name":"lk.agents.turn.e2e_latency","unit":"s","kind":"METRIC_KIND_HISTOGRAM",` +
			`"startTime":"2026-10-07T11:00:00Z","endTime":"2026-10-07T11:00:30Z",` +
			`"attributes":{"model_name":"gpt-4o","retries":2,"tags":["a","b"],"nested":{"ok":true},"none":null},` +
			`"histogram":{"count":"3","sum":2.4,"min":0.6,"max":1.1,"bucketBounds":[0.5,1],"bucketCounts":["0","2","1"]}}`,
	} {
		var p MetricPoint
		require.NoError(t, json.Unmarshal([]byte(point), &p))
		got, err := json.Marshal(p)
		require.NoError(t, err)
		assert.JSONEq(t, point, string(got))
	}
}

// TestGetSessionMetricsRejectsBadOptions confirms a negative limit and a blank
// metric name fail Validate, and fail GetSessionMetrics before any request is
// sent.
func TestGetSessionMetricsRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    MetricOptions
		wantErr string
	}{
		{name: "negative limit", opts: MetricOptions{PageOptions: PageOptions{Limit: -1}}, wantErr: "limit must not be negative"},
		{name: "blank name", opts: MetricOptions{Names: []string{"lk.agents.turn.e2e_latency", " "}}, wantErr: "metric name must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, tt.opts.Validate(), tt.wantErr)

			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionMetrics(context.Background(), "p1", "RM_1", tt.opts)
			require.EqualError(t, err, tt.wantErr)
			assert.False(t, called, "no request should be sent")
		})
	}
}

// TestGetSessionMetricsErrors checks an unknown session is NotFound and an
// empty unfiltered first page with user data recording off carries
// ObservabilityDisabled.
func TestGetSessionMetricsErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNotFound bool
		wantDisabled bool
	}{
		{
			name:         "unknown session",
			status:       http.StatusNotFound,
			body:         `{"code":5,"message":"session not found"}`,
			wantNotFound: true,
		},
		{
			name:   "recording off",
			status: http.StatusBadRequest,
			body: `{"code":9,"message":"user data recording is off","details":[` +
				`{"@type":"type.googleapis.com/livekit.publicapi.observability.v1.ObservabilityDisabled","dashboardUrl":"https://cloud.example/p1"}]}`,
			wantDisabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c, err := New(srv.URL, "sekret")
			require.NoError(t, err)

			_, err = c.GetSessionMetrics(context.Background(), "p1", "RM_1", MetricOptions{})
			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, IsNotFound(err))
			_, disabled := ObservabilityDisabled(err)
			assert.Equal(t, tt.wantDisabled, disabled)
		})
	}
}
