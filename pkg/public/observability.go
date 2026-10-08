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
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// Recording names accepted by RecordingURLOptions.Recording.
const (
	RecordingAudio       = "audio"
	RecordingChatHistory = "chat-history"
)

// RecordingURLOptions chooses which of a session's recordings
// GetSessionRecordingURL signs a URL for.
type RecordingURLOptions struct {
	// Recording is "audio" (Ogg) or "chat-history" (JSON). Required.
	Recording string
}

// parseRecordingFileType maps a friendly recording name to the wire enum.
func parseRecordingFileType(s string) (oapi.LivekitPublicapiObservabilityV1RecordingFileType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case RecordingAudio:
		return oapi.RECORDINGFILETYPEAUDIO, nil
	case RecordingChatHistory:
		return oapi.RECORDINGFILETYPECHATHISTORY, nil
	case "":
		return "", errors.New(`recording type is required ("audio" or "chat-history")`)
	default:
		return "", fmt.Errorf("invalid recording type %q (expected \"audio\" or \"chat-history\")", s)
	}
}

// Validate reports a missing or unknown recording name. GetSessionRecordingURL
// makes the same check before sending a request; callers can run it earlier.
func (o RecordingURLOptions) Validate() error {
	_, err := parseRecordingFileType(o.Recording)
	return err
}

// GetSessionRecordingURL returns a short-lived signed URL for one of a session's
// recordings, with when it expires and when the recording started. The URL
// downloads straight from the project's data region (see DownloadRecording).
// A session with no such recording is NotFound (see IsNotFound), or, while the
// project's user data recording is off, a FailedPrecondition that
// ObservabilityDisabled recognizes.
func (c *Client) GetSessionRecordingURL(ctx context.Context, projectID, sessionID string, opts RecordingURLOptions) (*oapi.LivekitPublicapiObservabilityV1RecordingGetURLResponse, error) {
	fileType, err := parseRecordingFileType(opts.Recording)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.ObservabilityServiceGetSessionRecordingURLWithResponse(ctx, projectID, sessionID,
		&oapi.ObservabilityServiceGetSessionRecordingURLParams{FileType: &fileType})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.Url == nil || *resp.JSON200.Url == "" {
		return nil, errors.New("unexpected response from server: missing url")
	}
	return resp.JSON200, nil
}

// ObservabilityDisabled reports whether err is the Public API's answer to an
// observability read that found nothing while the project's user data
// recording is off. dashboardURL is the project's settings page where an admin
// turns recording on; it can be empty.
func ObservabilityDisabled(err error) (dashboardURL string, ok bool) {
	var detail struct {
		DashboardURL string `json:"dashboardUrl"`
	}
	if !errorDetail(err, "livekit.publicapi.observability.v1.ObservabilityDisabled", &detail) {
		return "", false
	}
	return detail.DashboardURL, true
}

// TranscriptItem is one entry in a session's conversation, with the variant
// the API sent decoded: exactly one of Message, ToolCall, ToolResult,
// AgentHandoff and ConfigUpdate is set, or none for a kind newer than this
// client. It marshals back to the API's own JSON.
type TranscriptItem struct {
	ID string `json:"id,omitempty"`
	// Timestamp is when the item happened: for a message, when its speaker
	// started speaking.
	Timestamp    *time.Time                                                      `json:"timestamp,omitempty"`
	Message      *oapi.LivekitPublicapiObservabilityV1TranscriptItemMessage      `json:"message,omitempty"`
	ToolCall     *oapi.LivekitPublicapiObservabilityV1TranscriptItemToolCall     `json:"toolCall,omitempty"`
	ToolResult   *oapi.LivekitPublicapiObservabilityV1TranscriptItemToolResult   `json:"toolResult,omitempty"`
	AgentHandoff *oapi.LivekitPublicapiObservabilityV1TranscriptItemAgentHandoff `json:"agentHandoff,omitempty"`
	ConfigUpdate *oapi.LivekitPublicapiObservabilityV1TranscriptItemConfigUpdate `json:"configUpdate,omitempty"`
}

// TranscriptPage is one page of a session's transcript.
type TranscriptPage struct {
	// Items are oldest first, in the order the agent recorded them.
	Items []TranscriptItem
	// NextCursor is non-empty when more pages remain (pass it back as
	// PageOptions.Cursor).
	NextCursor string
	// SkippedRecords counts this page's records the server couldn't read as
	// transcript items and left out.
	SkippedRecords int
}

// GetSessionTranscript returns one page of a session's conversation: messages
// with their roles and latencies, tool calls and results, agent handoffs and
// configuration changes. The agent exports its transcript when the session
// ends, so an active session returns what exists so far, usually nothing,
// without an error. An unknown session is NotFound (see IsNotFound); an empty
// first page while the project's user data recording is off is a
// FailedPrecondition that ObservabilityDisabled recognizes.
func (c *Client) GetSessionTranscript(ctx context.Context, projectID, sessionID string, opts PageOptions) (*TranscriptPage, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	params := &oapi.ObservabilityServiceGetSessionTranscriptParams{}
	params.PagePageSize, params.PageCursor = opts.params()
	resp, err := c.gen.ObservabilityServiceGetSessionTranscriptWithResponse(ctx, projectID, sessionID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	// The generated item is a union that hides which variant it holds, so
	// decode the body again into items that say.
	var body struct {
		Items []TranscriptItem `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	page := &TranscriptPage{Items: body.Items, NextCursor: pageCursor(resp.JSON200.PageInfo)}
	if n := resp.JSON200.SkippedRecords; n != nil {
		page.SkippedRecords = *n
	}
	return page, nil
}

// gzipMagic opens every gzip stream (RFC 1952).
var gzipMagic = []byte{0x1f, 0x8b}

// recordingClient downloads recordings. It is its own client, not
// http.DefaultClient, so nothing else in the process can give it a cookie jar
// or a credential-adding transport, and so a stalled object store can't hang
// the download forever: the store must start answering within
// ResponseHeaderTimeout, and the whole download, an hour-long recording
// included, must finish within Timeout.
var recordingClient = &http.Client{
	Timeout: 10 * time.Minute,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// DownloadRecording copies the recording at a signed URL from
// GetSessionRecordingURL into w and returns the bytes written. The URL carries
// its own authorization, so the request sends no credentials: never the user's
// session token, and no cookie, even across a redirect. Its errors never hold
// the URL's query string, where the signature is. Chat history is stored
// gzip-encoded; a gzip body is decompressed whether or not the HTTP transport
// already did it, so w always gets the plain file.
func DownloadRecording(ctx context.Context, signedURL string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return 0, fmt.Errorf("download recording: %w", redactURLError(err))
	}
	resp, err := recordingClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download recording: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := redactSignature(strings.TrimSpace(string(snippet)), signedURL)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return 0, fmt.Errorf("download recording: unexpected response (HTTP %d): %s", resp.StatusCode, msg)
	}

	br := bufio.NewReader(resp.Body)
	var body io.Reader = br
	if head, _ := br.Peek(len(gzipMagic)); bytes.Equal(head, gzipMagic) {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return 0, fmt.Errorf("download recording: %w", err)
		}
		defer zr.Close()
		body = zr
	}
	n, err := io.Copy(w, body)
	if err != nil {
		return n, fmt.Errorf("download recording: %w", err)
	}
	return n, nil
}

// redactURLError drops the query string, which holds a signed URL's
// signature, from the URL a *url.Error names, so the signature can't reach a
// terminal or a log through an error message. Other errors pass through.
func redactURLError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		uerr.URL = withoutQuery(uerr.URL)
	}
	return err
}

// withoutQuery returns rawURL without its query string or fragment.
func withoutQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		if i := strings.IndexAny(rawURL, "?#"); i >= 0 {
			return rawURL[:i]
		}
		return rawURL
	}
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	return u.String()
}

// redactSignature hides the long query values of signedURL, such as its
// signature and credential, wherever they appear in msg: an object store's
// error page can echo the signature it was given.
func redactSignature(msg, signedURL string) string {
	u, err := url.Parse(signedURL)
	if err != nil {
		return msg
	}
	for _, values := range u.Query() {
		for _, v := range values {
			if len(v) >= 16 {
				msg = strings.ReplaceAll(msg, v, "[redacted]")
			}
		}
	}
	return msg
}
