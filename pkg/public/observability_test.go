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
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			assert.Equal(t, "/v1/projects/p1/sessions/RM_1/recording-url", gotPath)
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
