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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithClientInfoSendsHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "sekret", WithClientInfo(ClientInfo{
		Name:        "cli",
		Version:     "2.19.3",
		Command:     "project list",
		CodingAgent: "claude-code",
	}))
	require.NoError(t, err)
	_, err = c.ListProjects(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "cli", got.Get(HeaderClient))
	assert.Equal(t, "2.19.3", got.Get(HeaderClientVersion))
	assert.Equal(t, "project list", got.Get(HeaderCommand))
	assert.Equal(t, "claude-code", got.Get(HeaderCodingAgent))
	assert.Equal(t, "livekit-cli/2.19.3", got.Get("User-Agent"))
	assert.Equal(t, "Bearer sekret", got.Get("Authorization"), "client info doesn't replace auth")
}

func TestWithClientInfoLeavesOutEmptyFields(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "sekret", WithClientInfo(ClientInfo{Name: "cli", Version: "2.19.3"}))
	require.NoError(t, err)
	_, err = c.ListProjects(context.Background())
	require.NoError(t, err)

	_, hasAgent := got[http.CanonicalHeaderKey(HeaderCodingAgent)]
	assert.False(t, hasAgent, "no coding agent, no header")
	_, hasCommand := got[http.CanonicalHeaderKey(HeaderCommand)]
	assert.False(t, hasCommand)
}

func TestDetectCodingAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"none", nil, ""},
		{"AI_AGENT names the agent", map[string]string{"AI_AGENT": "claude-code_2-1-317_agent"}, "claude-code"},
		{"AI_AGENT wins over a vendor variable", map[string]string{"AI_AGENT": "codex", "CLAUDECODE": "1"}, "codex"},
		{"AI_AGENT is cleaned", map[string]string{"AI_AGENT": "Some Agent!\n"}, "someagent"},
		{"AI_AGENT that cleans to nothing falls through", map[string]string{"AI_AGENT": "!!!", "CLAUDECODE": "1"}, "claude-code"},
		{"Claude Code", map[string]string{"CLAUDECODE": "1"}, "claude-code"},
		{"Gemini CLI", map[string]string{"GEMINI_CLI": "1"}, "gemini-cli"},
		{"a variable set to something else", map[string]string{"CLAUDECODE": "0"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			assert.Equal(t, tc.want, DetectCodingAgent(getenv))
		})
	}
}

func TestCleanAgentNameCapsLength(t *testing.T) {
	long := "a-very-long-agent-name-that-keeps-going-and-going"
	assert.Len(t, cleanAgentName(long), 32)
}
