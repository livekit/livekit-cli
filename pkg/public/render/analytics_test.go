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

package render

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// decodeSessionResponse decodes a GetSession body the way the client does, so
// the tests read like the server's JSON.
func decodeSessionResponse(t *testing.T, body string) oapi.LivekitPublicapiAnalyticsV1SessionsGetResponse {
	t.Helper()
	var resp oapi.LivekitPublicapiAnalyticsV1SessionsGetResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	return resp
}

const sessionWithDetail = `{
  "session": {"sessionId": "RM_1", "roomName": "demo", "status": "SESSION_STATUS_CLOSED", "numParticipants": 2},
  "detail": {
    "bandwidthIn": "1500000",
    "bandwidthOut": "2500",
    "connectionSeconds": "3723",
    "quality": [{"value": 0}, {"value": 0.9}, {"value": 0.7}],
    "publishBps": [{"value": 2000000}, {"value": 0}],
    "participants": [{
      "participantIdentity": "alice",
      "participantName": "Alice",
      "location": "United States",
      "region": "US East",
      "publishedSources": {"cameraTrack": true, "microphoneTrack": true}
    }],
    "participantsPage": {"nextCursor": "c2", "hasMore": true}
  }
}`

func TestSessionDetailText(t *testing.T) {
	resp := decodeSessionResponse(t, sessionWithDetail)
	var stdout, stderr bytes.Buffer
	p := util.NewPrinter(&stdout, &stderr, false)

	require.NoError(t, SessionDetail(p, false, *resp.Session, resp.Detail))

	got := stdout.String()
	for _, want := range []string{
		"RM_1", "demo", "SESSION_STATUS_CLOSED", // the list row
		"1.5 MB", "2.5 KB", "1h2m3s", // totals
		"Quality", "80%", "90%", // average and peak skip the empty bucket
		"Publish bitrate", "2.0 Mbps",
		"alice", "Alice", "United States", "US East", "camera, microphone", // first page of participants
	} {
		assert.Contains(t, got, want)
	}
	assert.Contains(t, stderr.String(), "lk analytics session participant list RM_1 --cursor c2")
}

func TestSessionDetailTextLastParticipantsPage(t *testing.T) {
	resp := decodeSessionResponse(t, `{"session":{"sessionId":"RM_1"},"detail":{"participants":[],"participantsPage":{}}}`)
	var stdout, stderr bytes.Buffer
	p := util.NewPrinter(&stdout, &stderr, false)

	require.NoError(t, SessionDetail(p, false, *resp.Session, resp.Detail))

	assert.Contains(t, stderr.String(), "No participants found")
	assert.NotContains(t, stderr.String(), "--cursor")
}

func TestSessionDetailTextParticipantsUnread(t *testing.T) {
	resp := decodeSessionResponse(t, `{"session":{"sessionId":"RM_1"},"detail":{}}`)
	var stdout, stderr bytes.Buffer
	p := util.NewPrinter(&stdout, &stderr, false)

	require.NoError(t, SessionDetail(p, false, *resp.Session, resp.Detail))

	assert.Contains(t, stderr.String(), "Participants couldn't be read")
	assert.Contains(t, stderr.String(), "lk analytics session participant list RM_1, using the same flags")
}

func TestSessionDetailTextFinalizing(t *testing.T) {
	resp := decodeSessionResponse(t, `{"session":{"sessionId":"RM_1","roomName":"demo"}}`)
	var stdout, stderr bytes.Buffer
	p := util.NewPrinter(&stdout, &stderr, false)

	require.NoError(t, SessionDetail(p, false, *resp.Session, resp.Detail))

	assert.Contains(t, stdout.String(), "demo")
	assert.NotContains(t, stdout.String(), "Bandwidth In")
	assert.Contains(t, stderr.String(), "still being finalized")
}

func TestSessionParticipantsPage(t *testing.T) {
	participants := []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo{{ParticipantIdentity: ptr("alice"), Region: ptr("US East")}}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, &stderr, false), false, participants, "c2"))
	assert.Contains(t, stdout.String(), "alice")
	assert.Contains(t, stdout.String(), "US East")
	assert.Contains(t, stderr.String(), "--cursor c2")

	stdout.Reset()
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, nil, true), true, participants, "c2"))
	assert.JSONEq(t, `{"items":[{"participantIdentity":"alice","region":"US East"}],"nextCursor":"c2"}`, stdout.String())
}

// TestSessionParticipantsPageEmpty checks a page with no participants says
// there are none only when it is the last, so it never says so beside a hint
// that there are more.
func TestSessionParticipantsPageEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, &stderr, false), false, nil, "c2"))
	assert.NotContains(t, stderr.String(), "No participants found")
	assert.Contains(t, stderr.String(), "--cursor c2")

	stderr.Reset()
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, &stderr, false), false, nil, ""))
	assert.Contains(t, stderr.String(), "No participants found")
	assert.NotContains(t, stderr.String(), "--cursor")
	assert.Empty(t, stdout.String())
}

func TestSessionDetailTextEmptyParticipantsPageWithMore(t *testing.T) {
	resp := decodeSessionResponse(t, `{"session":{"sessionId":"RM_1"},"detail":{"participants":[],"participantsPage":{"nextCursor":"c2","hasMore":true}}}`)
	var stdout, stderr bytes.Buffer
	p := util.NewPrinter(&stdout, &stderr, false)

	require.NoError(t, SessionDetail(p, false, *resp.Session, resp.Detail))

	assert.NotContains(t, stderr.String(), "No participants found")
	assert.Contains(t, stderr.String(), "lk analytics session participant list RM_1 --cursor c2")
}

func ptr[T any](v T) *T { return &v }

// escapes is untrusted text carrying terminal escape sequences: an OSC that
// sets the window title, ended by BEL, and a CSI that clears the screen.
const escapes = "\x1b]0;pwned\x07\x1b[2J"

// terminalPrinter writes straight to the buffers, as NewPrinter does to a
// truecolor terminal. NewPrinter strips escape sequences from writers that
// aren't terminals, so its tests can't see what a terminal would get.
func terminalPrinter(stdout, stderr *bytes.Buffer) *util.Printer {
	return &util.Printer{Out: stdout, Err: stderr}
}

// tableStyling matches the SGR sequences (colors and bold) a table styles its
// borders and cells with.
var tableStyling = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// assertNoEscapes checks out reaches a terminal with no ESC or BEL beyond a
// table's own styling, so no escape sequence from the input can act.
func assertNoEscapes(t *testing.T, out string) {
	t.Helper()
	out = tableStyling.ReplaceAllString(out, "")
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\x07")
}

func TestStripControls(t *testing.T) {
	for in, want := range map[string]string{
		"alice":                            "alice",
		"\x1b]0;pwned\x07alice":            "]0;pwnedalice",
		"\x1b[2Jalice\x1b[0m":              "[2Jalice[0m",
		"a\tb\nc":                          "a\tb\nc", // tab and newline stay
		"a\rb\x00c\x7fd":                   "abcd",
		"\u009b2J\u009d0;pwned\u009cagent": "2J0;pwnedagent", // C1 CSI, OSC and ST
		"naïve 日本 🎉":                       "naïve 日本 🎉",
		"bad\xffbyte":                      "bad" + string(utf8.RuneError) + "byte", // so no lone C1 byte gets through
	} {
		assert.Equal(t, want, stripControls(in), "%q", in)
	}
}

// TestParticipantsStripEscapes checks a participant's identity and name, which
// the participant chose, reach a terminal with their escape sequences
// stripped, in the session detail and the participant list; --json escapes
// them itself.
func TestParticipantsStripEscapes(t *testing.T) {
	participants := []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo{{
		ParticipantIdentity: ptr("alice" + escapes),
		ParticipantName:     ptr(escapes + "Alice"),
	}}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionParticipantsPage(terminalPrinter(&stdout, &stderr), false, participants, ""))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), "alice]0;pwned[2J")

	stdout.Reset()
	detail := &oapi.LivekitPublicapiAnalyticsV1SessionDetail{
		Participants:     &participants,
		ParticipantsPage: &oapi.LivekitPublicapiCommonV1PageInfo{},
	}
	require.NoError(t, SessionDetail(terminalPrinter(&stdout, &stderr), false, oapi.LivekitPublicapiAnalyticsV1Session{SessionId: ptr("RM_1")}, detail))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), "alice]0;pwned[2J")

	stdout.Reset()
	require.NoError(t, SessionParticipantsPage(terminalPrinter(&stdout, &stderr), true, participants, ""))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), `alice\u001b]0;pwned\u0007`)
}

// TestSessionsStripEscapes checks a session's room name and tags, which
// whoever created the room chose, reach a terminal with their escape sequences
// stripped, in the session list and the session detail; --json escapes them
// itself.
func TestSessionsStripEscapes(t *testing.T) {
	session := oapi.LivekitPublicapiAnalyticsV1Session{
		SessionId: ptr("RM_1"),
		RoomName:  ptr("demo" + escapes),
		Tags:      &[]string{escapes + "tag"},
	}
	sessions := []oapi.LivekitPublicapiAnalyticsV1Session{session}

	var stdout, stderr bytes.Buffer
	require.NoError(t, Sessions(terminalPrinter(&stdout, &stderr), false, sessions))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), "demo]0;pwned[2J")

	stdout.Reset()
	require.NoError(t, SessionsPage(terminalPrinter(&stdout, &stderr), false, sessions, ""))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), "demo]0;pwned[2J")

	stdout.Reset()
	require.NoError(t, SessionDetail(terminalPrinter(&stdout, &stderr), false, session, nil))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), "demo]0;pwned[2J")

	stdout.Reset()
	require.NoError(t, SessionsPage(terminalPrinter(&stdout, &stderr), true, sessions, ""))
	assertNoEscapes(t, stdout.String())
	assert.Contains(t, stdout.String(), `demo\u001b]0;pwned\u0007`)
	assert.Contains(t, stdout.String(), `\u001b]0;pwned\u0007\u001b[2Jtag`)
}

// TestSessionDetailJSON checks --json emits the API's {session, detail} shape,
// timelines and participants page included.
func TestSessionDetailJSON(t *testing.T) {
	resp := decodeSessionResponse(t, sessionWithDetail)
	var stdout bytes.Buffer
	p := util.NewPrinter(&stdout, nil, true)

	require.NoError(t, SessionDetail(p, true, *resp.Session, resp.Detail))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(sessionWithDetail), &want))
	assert.Equal(t, want, got)
}
