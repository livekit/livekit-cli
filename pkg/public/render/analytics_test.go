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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
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
      "publishedSources": {"cameraTrack": true, "microphoneTrack": true},
      "sessions": [{
        "participantSessionId": "PA_alice1",
        "joinedAt": "2026-10-07T11:00:00Z",
        "leftAt": "2026-10-07T11:02:05Z",
        "durationSeconds": "125",
        "os": "mac",
        "browser": "chrome",
        "sdkVersion": "2.6.1",
        "connectionType": "UDP",
        "connectionTimeMs": 120,
        "location": "Canada",
        "region": "US East"
      }]
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
		"PA_alice1", "2m5s", "mac, chrome, SDK 2.6.1", "UDP (120ms)", "Canada", // their participant sessions
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

// participantsPage is a page of participants as the API sends it: alice
// reconnected from her phone, bob's participant sessions couldn't be read, and
// carol is still connected from a client that reported nothing.
const participantsPage = `[
  {"participantIdentity": "alice", "region": "US East", "sessions": [
    {"participantSessionId": "PA_alice1", "joinedAt": "2026-10-07T11:00:00Z", "leftAt": "2026-10-07T11:01:00Z", "durationSeconds": "60",
     "os": "mac", "browser": "chrome", "sdkVersion": "2.6.1", "connectionType": "UDP", "connectionTimeMs": 120, "location": "Canada"},
    {"participantSessionId": "PA_alice2", "joinedAt": "2026-10-07T11:01:30Z", "durationSeconds": "3723",
     "os": "ios", "deviceModel": "iPhone 15", "connectionType": "TURN", "location": "Canada"}
  ]},
  {"participantIdentity": "bob"},
  {"participantIdentity": "carol", "sessions": [{"participantSessionId": "PA_carol1"}]}
]`

func decodeParticipants(t *testing.T) []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo {
	t.Helper()
	var participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo
	require.NoError(t, json.Unmarshal([]byte(participantsPage), &participants))
	return participants
}

// TestSessionParticipantsPage checks the participants print as a table, then
// their participant sessions with the client each connected from, and --json
// prints the API's rows, participant sessions nested.
func TestSessionParticipantsPage(t *testing.T) {
	participants := decodeParticipants(t)

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, &stderr, false), false, participants, "c2"))
	got := stdout.String()
	assert.Contains(t, got, "alice")
	assert.Contains(t, got, "US East")
	for _, row := range [][]string{
		{"alice", "PA_alice1", "1m0s", "mac, chrome, SDK 2.6.1", "UDP (120ms)", "Canada"},
		{"alice", "PA_alice2", "1h2m3s", "ios, iPhone 15", "TURN", "Canada"},
		{"carol", "PA_carol1", "-", "-", "-", "-"},
	} {
		assert.Regexp(t, strings.Join(util.MapStrings(row, regexp.QuoteMeta), `[^\n]*`), got)
	}
	assert.Less(t, strings.Index(got, "PA_alice1"), strings.Index(got, "PA_alice2"), "participant sessions keep the API's order")
	assert.Less(t, strings.Index(got, "Identity"), strings.Index(got, "Participant Session"), "participants print before their sessions")
	assert.Contains(t, stderr.String(), "--cursor c2")

	stdout.Reset()
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, nil, true), true, participants, "c2"))
	assert.JSONEq(t, `{"items":`+participantsPage+`,"nextCursor":"c2"}`, stdout.String())
}

// TestSessionParticipantsPageNoSessions prints no participant sessions table
// when none were read.
func TestSessionParticipantsPageNoSessions(t *testing.T) {
	participants := []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo{{ParticipantIdentity: ptr("bob")}}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionParticipantsPage(util.NewPrinter(&stdout, &stderr, false), false, participants, ""))
	assert.Contains(t, stdout.String(), "bob")
	assert.NotContains(t, stdout.String(), "Participant Session")
	assert.Empty(t, stderr.String())
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

const recordingURLResponse = `{"url":"https://bucket.example/rec?sig=x","expiresAt":"2026-10-07T12:15:00Z","recordingStartedAt":"2026-10-07T11:00:00Z"}`

// TestRecordingURL checks the text form is the bare URL on stdout, so it pipes
// into curl, and --json is the API's response.
func TestRecordingURL(t *testing.T) {
	var resp oapi.LivekitPublicapiObservabilityV1RecordingGetURLResponse
	require.NoError(t, json.Unmarshal([]byte(recordingURLResponse), &resp))

	var stdout, stderr bytes.Buffer
	require.NoError(t, RecordingURL(util.NewPrinter(&stdout, &stderr, false), false, resp))
	assert.Equal(t, "https://bucket.example/rec?sig=x\n", stdout.String())
	assert.Contains(t, stderr.String(), "expires")

	stdout.Reset()
	require.NoError(t, RecordingURL(util.NewPrinter(&stdout, nil, true), true, resp))
	assert.JSONEq(t, recordingURLResponse, stdout.String())
}

// TestRecordingSaved checks a saved recording says where it went, and --json
// gives a script the same facts.
func TestRecordingSaved(t *testing.T) {
	started := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	saved := SavedRecording{SessionID: "RM_1", Recording: "chat-history", File: "RM_1-chat-history.json", Bytes: 2048, RecordingStartedAt: &started}

	var stdout, stderr bytes.Buffer
	require.NoError(t, RecordingSaved(util.NewPrinter(&stdout, &stderr, false), false, saved))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "Saved chat history of session RM_1 to RM_1-chat-history.json (2.0 KB)")
	assert.Contains(t, stderr.String(), "recording started")

	stdout.Reset()
	require.NoError(t, RecordingSaved(util.NewPrinter(&stdout, nil, true), true, saved))
	assert.JSONEq(t, `{"sessionId":"RM_1","recording":"chat-history","file":"RM_1-chat-history.json","bytes":2048,"recordingStartedAt":"2026-10-07T11:00:00Z"}`, stdout.String())
}

// transcriptItems is one page of a transcript with an item of each kind, as
// the API sends it.
const transcriptItems = `[
  {"id": "item_1", "timestamp": "2026-10-07T11:00:01Z", "message": {"role": "ROLE_USER", "text": "hi\nthere", "transcriptConfidence": 0.93, "transcriptionDelayMs": 150, "endOfTurnDelayMs": 320.4}},
  {"id": "item_2", "timestamp": "2026-10-07T11:00:02Z", "message": {"role": "ROLE_AGENT", "text": "hello", "interrupted": true, "e2eLatencyMs": 820.5, "llmTtftMs": 310, "ttsTtfbMs": 95.25}},
  {"id": "item_3", "timestamp": "2026-10-07T11:00:02Z", "message": {"role": "ROLE_SYSTEM", "text": "be nice", "redacted": true}},
  {"id": "item_4", "timestamp": "2026-10-07T11:00:03Z", "toolCall": {"name": "lookup", "callId": "c1", "arguments": "{\"q\":1}"}},
  {"id": "item_5", "timestamp": "2026-10-07T11:00:04Z", "toolResult": {"name": "lookup", "callId": "c1", "output": "boom", "isError": true}},
  {"id": "item_6", "timestamp": "2026-10-07T11:00:05Z", "agentHandoff": {"fromAgentId": "greeter", "toAgentId": "triage"}},
  {"id": "item_7", "timestamp": "2026-10-07T11:00:06Z", "configUpdate": {"instructions": "new", "toolsAdded": ["lookup", "book"], "toolsRemoved": ["old"]}},
  {"id": "item_8", "timestamp": "2026-10-07T11:00:07Z"}
]`

func decodeTranscriptItems(t *testing.T) []public.TranscriptItem {
	t.Helper()
	var items []public.TranscriptItem
	require.NoError(t, json.Unmarshal([]byte(transcriptItems), &items))
	return items
}

// TestSessionTranscriptText checks each item prints as one line with its role
// and latencies, and the page's skipped records and next cursor are reported.
func TestSessionTranscriptText(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	page := public.TranscriptPage{Items: decodeTranscriptItems(t), NextCursor: "c2", SkippedRecords: 1}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTranscript(util.NewPrinter(&stdout, &stderr, false), false, page, "unused"))

	assert.Equal(t, strings.Join([]string{
		"11:00:01  USER         hi there  (transcription 150ms · end_of_turn 320ms · confidence 0.93)",
		"11:00:02  AGENT        hello  [interrupted]  (e2e 820ms · llm_ttft 310ms · tts_ttfb 95.2ms)",
		"11:00:02  SYSTEM       be nice  [redacted]",
		`11:00:03  TOOL CALL    lookup({"q":1})`,
		"11:00:04  TOOL RESULT  lookup: boom  [error]",
		"11:00:05  HANDOFF      greeter → triage",
		"11:00:06  CONFIG       instructions changed · tools added: lookup, book · tools removed: old",
		"11:00:07  UNKNOWN      item_8 (a kind this lk doesn't know; see --json)",
		"",
	}, "\n"), stdout.String())
	assert.Contains(t, stderr.String(), "1 record couldn't be read as a transcript item")
	assert.Contains(t, stderr.String(), "More items available — re-run with --cursor c2")
	assert.NotContains(t, stderr.String(), "unused")
}

// TestTranscriptLineClipsToolText keeps a long tool output to one short line.
func TestTranscriptLineClipsToolText(t *testing.T) {
	output := strings.Repeat("é", 300) + "\nmore"
	line := transcriptLine(public.TranscriptItem{ToolResult: &oapi.LivekitPublicapiObservabilityV1TranscriptItemToolResult{Name: ptr("dump"), Output: &output}})
	assert.Equal(t, "-         TOOL RESULT  dump: "+strings.Repeat("é", 199)+"…", line)
}

// TestSessionTranscriptStripsEscapes checks what an LLM, a tool or the agent
// put in a transcript reaches a terminal with its escape sequences stripped,
// each item still on one line.
func TestSessionTranscriptStripsEscapes(t *testing.T) {
	text := "hi" + escapes + "\nthere"
	items := []public.TranscriptItem{
		{Message: &oapi.LivekitPublicapiObservabilityV1TranscriptItemMessage{Text: &text}},
		{ToolCall: &oapi.LivekitPublicapiObservabilityV1TranscriptItemToolCall{Name: &text, Arguments: &text}},
		{ToolResult: &oapi.LivekitPublicapiObservabilityV1TranscriptItemToolResult{Name: &text, Output: &text}},
		{AgentHandoff: &oapi.LivekitPublicapiObservabilityV1TranscriptItemAgentHandoff{FromAgentId: &text, ToAgentId: &text}},
		{ConfigUpdate: &oapi.LivekitPublicapiObservabilityV1TranscriptItemConfigUpdate{ToolsAdded: &[]string{text}, ToolsRemoved: &[]string{text}}},
		{ID: text},
	}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTranscript(terminalPrinter(&stdout, &stderr), false, public.TranscriptPage{Items: items}, ""))
	assertNoEscapes(t, stdout.String())
	assert.Equal(t, 10, strings.Count(stdout.String(), "hi]0;pwned[2J there"))
	assert.Equal(t, len(items), strings.Count(stdout.String(), "\n"))
}

// TestSessionTranscriptEmpty prints the caller's reason for an empty page.
func TestSessionTranscriptEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTranscript(util.NewPrinter(&stdout, &stderr, false), false, public.TranscriptPage{}, "Session RM_1 is still active"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "Session RM_1 is still active\n", stderr.String())

	// --json keeps stdout parseable and still says why on stderr.
	stdout.Reset()
	stderr.Reset()
	require.NoError(t, SessionTranscript(util.NewPrinter(&stdout, &stderr, false), true, public.TranscriptPage{}, "Session RM_1 is still active"))
	assert.JSONEq(t, `{"items":[]}`, stdout.String())
	assert.Equal(t, "Session RM_1 is still active\n", stderr.String())
}

// TestSessionTranscriptNoItemsNotEmpty checks a page with no items but
// skipped records or a next cursor isn't empty, as the server counts it, so
// it never says there's nothing beside a hint that there's more.
func TestSessionTranscriptNoItemsNotEmpty(t *testing.T) {
	for _, page := range []public.TranscriptPage{{SkippedRecords: 2}, {NextCursor: "c2"}} {
		var stdout, stderr bytes.Buffer
		require.NoError(t, SessionTranscript(util.NewPrinter(&stdout, &stderr, false), false, page, "No more transcript items"))
		assert.Empty(t, stdout.String())
		assert.NotContains(t, stderr.String(), "No more transcript items")
	}
}

// TestSessionTranscriptJSON checks --json prints the items as the API sent
// them, with the page's cursor and skipped records.
func TestSessionTranscriptJSON(t *testing.T) {
	page := public.TranscriptPage{Items: decodeTranscriptItems(t), NextCursor: "c2", SkippedRecords: 1}
	var stdout bytes.Buffer
	require.NoError(t, SessionTranscript(util.NewPrinter(&stdout, nil, true), true, page, ""))
	assert.JSONEq(t, `{"items":`+transcriptItems+`,"nextCursor":"c2","skippedRecords":1}`, stdout.String())
}

// logRecords is one page of agent logs as the API sends it: records with and
// without a logger, one with only the level name the agent logged, one with
// no level at all, and a message across lines.
const logRecords = `[
  {"id": "log_1", "timestamp": "2026-10-07T11:00:01.250Z", "level": "LOG_LEVEL_INFO", "severityText": "INFO", "logger": "livekit.agents", "message": "starting", "attributes": {"logger.name": "livekit.agents"}},
  {"id": "log_2", "timestamp": "2026-10-07T11:00:02Z", "level": "LOG_LEVEL_WARN", "severityText": "WARNING", "logger": "app", "message": "slow tts\n  ttfb=1.5s", "bodyFields": {"ttfb": 1.5}, "traceId": "0af7651916cd43dd8448eb211c80319c", "spanId": "b7ad6b7169203331"},
  {"id": "log_3", "timestamp": "2026-10-07T11:00:03Z", "severityText": "notice", "message": "custom level"},
  {"id": "log_4", "message": "evaluation passed"}
]`

func decodeLogRecords(t *testing.T) []oapi.LivekitPublicapiObservabilityV1LogRecord {
	t.Helper()
	var records []oapi.LivekitPublicapiObservabilityV1LogRecord
	require.NoError(t, json.Unmarshal([]byte(logRecords), &records))
	return records
}

// TestSessionLogsText checks each record prints as one line with its time,
// level, logger and message, and a next page says to re-run with its cursor.
// TestSessionLogsStripEscapes checks what the agent logged, which can quote
// an LLM or a participant, reaches a terminal with its escape sequences
// stripped and its messages still lined up.
func TestSessionLogsStripEscapes(t *testing.T) {
	text := "hi" + escapes
	records := []oapi.LivekitPublicapiObservabilityV1LogRecord{
		{Logger: &text, Message: &text, SeverityText: &text},
		{Logger: ptr("app"), Message: ptr("ok")},
	}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionLogs(terminalPrinter(&stdout, &stderr), false, public.LogPage{Records: records}, ""))
	assertNoEscapes(t, stdout.String())
	assert.Equal(t, strings.Join([]string{
		"-             HI]0;PWNED[2J  hi]0;pwned[2J  hi]0;pwned[2J",
		"-             -       app            ok",
		"",
	}, "\n"), stdout.String())
}

func TestSessionLogsText(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	page := public.LogPage{Records: decodeLogRecords(t), NextCursor: "c2"}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionLogs(util.NewPrinter(&stdout, &stderr, false), false, page, "unused"))

	assert.Equal(t, strings.Join([]string{
		"11:00:01.250  INFO    livekit.agents  starting",
		"11:00:02.000  WARN    app             slow tts ttfb=1.5s",
		"11:00:03.000  NOTICE  -               custom level",
		"-             -       -               evaluation passed",
		"",
	}, "\n"), stdout.String())
	assert.Contains(t, stderr.String(), "More records available — re-run with --cursor c2")
	assert.NotContains(t, stderr.String(), "unused")
}

// TestSessionLogsEmpty prints the caller's reason for an empty page, on
// stderr in both modes.
func TestSessionLogsEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionLogs(util.NewPrinter(&stdout, &stderr, false), false, public.LogPage{}, "No agent logs"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "No agent logs\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, SessionLogs(util.NewPrinter(&stdout, &stderr, false), true, public.LogPage{}, "No agent logs"))
	assert.JSONEq(t, `{"items":[]}`, stdout.String())
	assert.Equal(t, "No agent logs\n", stderr.String())
}

// TestSessionLogsJSON checks --json prints the records as the API sent them,
// with the page's cursor.
func TestSessionLogsJSON(t *testing.T) {
	page := public.LogPage{Records: decodeLogRecords(t), NextCursor: "c2"}
	var stdout bytes.Buffer
	require.NoError(t, SessionLogs(util.NewPrinter(&stdout, nil, true), true, page, ""))

	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "c2", got.NextCursor)
	require.Len(t, got.Items, 4)
	assert.Equal(t, map[string]any{"ttfb": 1.5}, got.Items[1]["bodyFields"])
	assert.Equal(t, "b7ad6b7169203331", got.Items[1]["spanId"])
	assert.Equal(t, "2026-10-07T11:00:01.25Z", got.Items[0]["timestamp"])
}

// traceSpans is a session's spans as the API sends them, by start time: a
// root with two children, one of which has a failed child and one still
// running, and a span whose parent wasn't exported, which the tree shows as a
// root.
const traceSpans = `[
  {"spanId": "a1", "name": "agent_session", "kind": "SPAN_KIND_INTERNAL", "startTime": "2026-10-07T11:00:00Z", "endTime": "2026-10-07T11:05:00Z", "status": "SPAN_STATUS_OK", "attributes": {"lk.agent_name": "triage"}},
  {"spanId": "b2", "parentSpanId": "a1", "name": "user_turn", "startTime": "2026-10-07T11:00:01Z", "endTime": "2026-10-07T11:00:01.320Z"},
  {"spanId": "c3", "parentSpanId": "a1", "name": "agent_turn", "startTime": "2026-10-07T11:00:01.400Z", "endTime": "2026-10-07T11:00:03.150Z"},
  {"spanId": "d4", "parentSpanId": "c3", "name": "llm_request", "startTime": "2026-10-07T11:00:01.500Z", "endTime": "2026-10-07T11:00:02.320Z", "status": "SPAN_STATUS_ERROR", "statusMessage": "rate\n limited", "events": [{"name": "exception"}]},
  {"spanId": "e5", "parentSpanId": "c3", "name": "tts_request", "startTime": "2026-10-07T11:00:02.400Z"},
  {"spanId": "f6", "parentSpanId": "ffff", "name": "on_enter", "startTime": "2026-10-07T11:00:04Z", "endTime": "2026-10-07T11:00:04.0004Z", "status": "SPAN_STATUS_ERROR"}
]`

func decodeSpans(t *testing.T, body string) []oapi.LivekitPublicapiObservabilityV1Span {
	t.Helper()
	var spans []oapi.LivekitPublicapiObservabilityV1Span
	require.NoError(t, json.Unmarshal([]byte(body), &spans))
	return spans
}

// TestSessionTracesText checks the spans print as a tree built from their
// parent ids, each line with its start time and duration, a failed span with
// its status message, and a span whose parent is missing as a root.
func TestSessionTracesText(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	page := public.TracePage{Spans: decodeSpans(t, traceSpans)}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, &stderr, false), false, page, "unused"))

	assert.Equal(t, strings.Join([]string{
		"11:00:00.000      5m0s  agent_session",
		"11:00:01.000     320ms  ├─ user_turn",
		"11:00:01.400     1.75s  └─ agent_turn",
		"11:00:01.500     820ms     ├─ llm_request  [error: rate limited]",
		"11:00:02.400         -     └─ tts_request",
		"11:00:04.000     0.4ms  on_enter  [error]",
		"",
	}, "\n"), stdout.String())
	assert.Empty(t, stderr.String())
}

// TestSessionTracesCycle checks spans whose parents name each other, which an
// agent shouldn't export, still print once each instead of looping.
// TestSessionTracesStripEscapes checks a span's name and status message,
// which the agent's code and the libraries it calls chose, reach a terminal
// with their escape sequences stripped.
func TestSessionTracesStripEscapes(t *testing.T) {
	text := "hi" + escapes
	status := oapi.SPANSTATUSERROR
	spans := []oapi.LivekitPublicapiObservabilityV1Span{
		{SpanId: ptr("a"), Name: &text, Status: &status, StatusMessage: &text},
		{SpanId: ptr("b"), ParentSpanId: ptr("a"), Name: &text},
	}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTraces(terminalPrinter(&stdout, &stderr), false, public.TracePage{Spans: spans}, ""))
	assertNoEscapes(t, stdout.String())
	assert.Equal(t, strings.Join([]string{
		"-                    -  hi]0;pwned[2J  [error: hi]0;pwned[2J]",
		"-                    -  └─ hi]0;pwned[2J",
		"",
	}, "\n"), stdout.String())
}

func TestSessionTracesCycle(t *testing.T) {
	spans := decodeSpans(t, `[
	  {"spanId": "x", "parentSpanId": "y", "name": "x"},
	  {"spanId": "y", "parentSpanId": "x", "name": "y"},
	  {"spanId": "z", "parentSpanId": "z", "name": "z"}
	]`)
	var stdout bytes.Buffer
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, nil, false), false, public.TracePage{Spans: spans}, ""))
	assert.Equal(t, strings.Join([]string{
		"-                    -  x",
		"-                    -  └─ y",
		"-                    -  z",
		"",
	}, "\n"), stdout.String())
}

// TestSessionTracesMore checks a read that stopped at its limit says how to
// read the rest.
func TestSessionTracesMore(t *testing.T) {
	page := public.TracePage{Spans: decodeSpans(t, traceSpans), NextCursor: "c2"}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, &stderr, false), false, page, ""))
	assert.Contains(t, stderr.String(), "Printed 6 spans; more remain")
	assert.Contains(t, stderr.String(), "--limit")
	assert.Contains(t, stderr.String(), "or re-run with --cursor c2 for the next ones")
}

// TestSessionTracesEmpty prints the caller's reason for no spans, on stderr in
// both modes.
func TestSessionTracesEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, &stderr, false), false, public.TracePage{}, "No spans"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "No spans\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, &stderr, false), true, public.TracePage{}, "No spans"))
	assert.JSONEq(t, `{"items":[]}`, stdout.String())
	assert.Equal(t, "No spans\n", stderr.String())
}

// TestSessionTracesJSON checks --json prints the spans as the API sent them,
// in its order, with the cursor where the read stopped.
func TestSessionTracesJSON(t *testing.T) {
	page := public.TracePage{Spans: decodeSpans(t, traceSpans), NextCursor: "c2"}
	var stdout bytes.Buffer
	require.NoError(t, SessionTraces(util.NewPrinter(&stdout, nil, true), true, page, ""))

	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "c2", got.NextCursor)
	require.Len(t, got.Items, 6)
	assert.Equal(t, "a1", got.Items[0]["spanId"])
	assert.Equal(t, map[string]any{"lk.agent_name": "triage"}, got.Items[0]["attributes"])
	assert.Equal(t, "SPAN_STATUS_ERROR", got.Items[3]["status"])
	assert.Equal(t, "c3", got.Items[3]["parentSpanId"])
}

// metricPoints is one page of agent metrics as the API sends it: a gauge whose
// value is zero, a sum in an annotated unit, a histogram, an exponential
// histogram the agent recorded no min or max for, a dimensionless sum with a
// fractional value, and a point of a kind newer than this client.
const metricPoints = `[
  {"name": "lk.agents.active_sessions", "kind": "METRIC_KIND_GAUGE", "endTime": "2026-10-07T11:00:30Z", "attributes": {"lk.agent_name": "triage"}, "value": 0},
  {"name": "lk.agents.usage.llm_input_tokens", "unit": "{token}", "kind": "METRIC_KIND_SUM", "startTime": "2026-10-07T11:00:00Z", "endTime": "2026-10-07T11:00:30Z", "attributes": {"model_name": "gpt-4o"}, "value": 1520},
  {"name": "lk.agents.turn.e2e_latency", "unit": "s", "kind": "METRIC_KIND_HISTOGRAM", "startTime": "2026-10-07T11:00:00Z", "endTime": "2026-10-07T11:00:30.250Z", "histogram": {"count": "3", "sum": 2.4, "min": 0.6, "max": 1.1, "bucketBounds": [0.5, 1], "bucketCounts": ["0", "2", "1"]}},
  {"name": "gen_ai.client.operation.duration", "unit": "s", "kind": "METRIC_KIND_EXPONENTIAL_HISTOGRAM", "startTime": "2026-10-07T11:00:00Z", "endTime": "2026-10-07T11:01:00Z", "histogram": {"count": "2", "sum": 1.5}},
  {"name": "lk.agents.interruptions", "unit": "1", "kind": "METRIC_KIND_SUM", "endTime": "2026-10-07T11:01:00Z", "value": 0.3333333333},
  {"name": "lk.agents.future", "endTime": "2026-10-07T11:01:00Z"}
]`

func decodeMetricPoints(t *testing.T) []public.MetricPoint {
	t.Helper()
	var points []public.MetricPoint
	require.NoError(t, json.Unmarshal([]byte(metricPoints), &points))
	return points
}

// TestSessionMetricsText checks each point prints as one line with its time,
// name and value or histogram summary, and a next page says to re-run with its
// cursor.
// TestSessionMetricsStripEscapes checks a metric's name and unit, which the
// agent's code chose, reach a terminal with their escape sequences stripped
// and the values still lined up.
func TestSessionMetricsStripEscapes(t *testing.T) {
	text := "hi" + escapes
	points := []public.MetricPoint{
		{Name: text, Unit: text, Value: ptr(1.0)},
		{Name: "lk.x", Value: ptr(2.0)},
	}

	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionMetrics(terminalPrinter(&stdout, &stderr), false, public.MetricPage{Points: points}, ""))
	assertNoEscapes(t, stdout.String())
	assert.Equal(t, strings.Join([]string{
		"-             hi]0;pwned[2J  1 hi]0;pwned[2J",
		"-             lk.x           2",
		"",
	}, "\n"), stdout.String())
}

func TestSessionMetricsText(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	page := public.MetricPage{Points: decodeMetricPoints(t), NextCursor: "c2"}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionMetrics(util.NewPrinter(&stdout, &stderr, false), false, page, "unused"))

	assert.Equal(t, strings.Join([]string{
		"11:00:30.000  lk.agents.active_sessions         0",
		"11:00:30.000  lk.agents.usage.llm_input_tokens  1520 token",
		"11:00:30.250  lk.agents.turn.e2e_latency        count 3, sum 2.4 s, min 0.6 s, max 1.1 s",
		"11:01:00.000  gen_ai.client.operation.duration  count 2, sum 1.5 s",
		"11:01:00.000  lk.agents.interruptions           0.333333",
		"11:01:00.000  lk.agents.future                  -",
		"",
	}, "\n"), stdout.String())
	assert.Contains(t, stderr.String(),
		"More points available — re-run with --cursor c2")
	assert.NotContains(t, stderr.String(), "unused")
}

// TestSessionMetricsEmpty prints the caller's reason for an empty page, on
// stderr in both modes.
func TestSessionMetricsEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionMetrics(util.NewPrinter(&stdout, &stderr, false), false, public.MetricPage{}, "No metrics"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "No metrics\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, SessionMetrics(util.NewPrinter(&stdout, &stderr, false), true, public.MetricPage{}, "No metrics"))
	assert.JSONEq(t, `{"items":[]}`, stdout.String())
	assert.Equal(t, "No metrics\n", stderr.String())
}

// TestSessionMetricsJSON checks --json prints the points as the API sent
// them, with the page's cursor.
func TestSessionMetricsJSON(t *testing.T) {
	page := public.MetricPage{Points: decodeMetricPoints(t), NextCursor: "c2"}
	var stdout bytes.Buffer
	require.NoError(t, SessionMetrics(util.NewPrinter(&stdout, nil, true), true, page, ""))

	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "c2", got.NextCursor)
	require.Len(t, got.Items, 6)
	assert.Equal(t, 0.0, got.Items[0]["value"])
	assert.Equal(t, map[string]any{"lk.agent_name": "triage"}, got.Items[0]["attributes"])
	assert.Equal(t, "METRIC_KIND_HISTOGRAM", got.Items[2]["kind"])
	assert.Equal(t, map[string]any{
		"count": "3", "sum": 2.4, "min": 0.6, "max": 1.1,
		"bucketBounds": []any{0.5, 1.0}, "bucketCounts": []any{"0", "2", "1"},
	}, got.Items[2]["histogram"])
}

// sessionEvents is one page of a session's events as the API sends them: the
// room created, a participant joining and publishing a track, an API call
// that names no participant, and an event with no payload.
const sessionEvents = `[
  {"type": "ROOM_CREATED", "timestamp": "2026-10-07T11:00:00Z"},
  {"type": "PARTICIPANT_JOINED", "timestamp": "2026-10-07T11:00:01.250Z", "participantIdentity": "alice", "participantSessionId": "PA_aaaaaaaaaaaa",
   "payload": {"participantKind": "STANDARD", "connectionType": "UDP", "isMigration": false}},
  {"type": "TRACK_PUBLISHED", "timestamp": "2026-10-07T11:00:02Z", "participantIdentity": "alice", "participantSessionId": "PA_aaaaaaaaaaaa",
   "payload": {"trackId": "TR_1", "trackType": "AUDIO", "trackSource": "MICROPHONE", "mimeType": "audio/opus", "muted": false}},
  {"type": "API_CALL", "timestamp": "2026-10-07T11:00:03Z", "payload": {"service": "RoomService", "method": "UpdateRoomMetadata", "status": 0, "twirpErrorMessage": "", "durationNs": "1500000"}},
  {"type": "ROOM_ENDED", "timestamp": "2026-10-07T11:05:00Z", "payload": {"reason": "departure timeout"}}
]`

func decodeSessionEvents(t *testing.T) []oapi.LivekitPublicapiAnalyticsV1SessionEvent {
	t.Helper()
	var events []oapi.LivekitPublicapiAnalyticsV1SessionEvent
	require.NoError(t, json.Unmarshal([]byte(sessionEvents), &events))
	return events
}

// TestSessionEventsText checks each event prints as one line with its time,
// friendly type, participant identity and participant session, and its
// payload as sorted key=value pairs, and a next page says to re-run with its
// cursor.
func TestSessionEventsText(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	page := public.EventPage{Events: decodeSessionEvents(t), NextCursor: "c2"}
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionEvents(util.NewPrinter(&stdout, &stderr, false), false, page, "unused"))

	assert.Equal(t, strings.Join([]string{
		"11:00:00.000  room_created        -      -",
		"11:00:01.250  participant_joined  alice  PA_aaaaaaaaaaaa  connectionType=UDP isMigration=false participantKind=STANDARD",
		"11:00:02.000  track_published     alice  PA_aaaaaaaaaaaa  mimeType=audio/opus muted=false trackId=TR_1 trackSource=MICROPHONE trackType=AUDIO",
		`11:00:03.000  api_call            -      -                durationNs=1500000 method=UpdateRoomMetadata service=RoomService status=0 twirpErrorMessage=""`,
		`11:05:00.000  room_ended          -      -                reason="departure timeout"`,
		"",
	}, "\n"), stdout.String())
	assert.Contains(t, stderr.String(),
		"More events available — re-run with --cursor c2")
	assert.NotContains(t, stderr.String(), "unused")
}

// TestEventPayloadClips keeps a long payload to one short line.
func TestEventPayloadClips(t *testing.T) {
	ev := decodeSessionEvents(t)[0]
	long := strings.Repeat("x", 300)
	var payload oapi.GoogleProtobufStruct
	require.NoError(t, json.Unmarshal([]byte(`{"reason":"`+long+`\nmore"}`), &payload))
	ev.Payload = &payload
	got := eventPayload(ev)
	assert.Equal(t, eventPayloadMax, len([]rune(got)))
	assert.True(t, strings.HasPrefix(got, "reason="+`"xxx`))
	assert.True(t, strings.HasSuffix(got, "…"))
}

// TestSessionEventsEmpty prints the caller's reason for an empty page, on
// stderr in both modes.
func TestSessionEventsEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, SessionEvents(util.NewPrinter(&stdout, &stderr, false), false, public.EventPage{}, "No events"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "No events\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, SessionEvents(util.NewPrinter(&stdout, &stderr, false), true, public.EventPage{}, "No events"))
	assert.JSONEq(t, `{"items":[]}`, stdout.String())
	assert.Equal(t, "No events\n", stderr.String())
}

// TestSessionEventsJSON checks --json prints the events as the API sent them,
// with the page's cursor.
func TestSessionEventsJSON(t *testing.T) {
	page := public.EventPage{Events: decodeSessionEvents(t), NextCursor: "c2"}
	var stdout bytes.Buffer
	require.NoError(t, SessionEvents(util.NewPrinter(&stdout, nil, true), true, page, ""))

	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "c2", got.NextCursor)
	var want []map[string]any
	require.NoError(t, json.Unmarshal([]byte(sessionEvents), &want))
	want[1]["timestamp"] = "2026-10-07T11:00:01.25Z" // time.Time drops the trailing zero
	assert.Equal(t, want, got.Items)
}
