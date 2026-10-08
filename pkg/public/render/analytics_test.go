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
	"testing"

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
	assert.Contains(t, stderr.String(), "session participant list RM_1 --cursor c2")
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
	assert.Contains(t, stderr.String(), "session participant list RM_1, using the same flags")
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

func ptr[T any](v T) *T { return &v }

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
