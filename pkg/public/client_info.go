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
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// The headers that identify the caller on every Public API request.
const (
	HeaderClient        = "X-LiveKit-Client"
	HeaderClientVersion = "X-LiveKit-Client-Version"
	HeaderCommand       = "X-LiveKit-Command"
	HeaderCodingAgent   = "X-LiveKit-Coding-Agent"
	HeaderSource        = "X-LiveKit-Source"
)

// EnvSource names where a coding agent learned to call the CLI, such as
// "skills". Only LiveKit's skills and templates tell an agent to set it, so its
// value on a request shows which of them the agent followed. It is an
// environment variable rather than a flag so that a CLI too old to know it
// ignores it instead of failing the command.
const EnvSource = "LK_SOURCE"

// ClientInfo tells the API which client made a request: the CLI or another
// client, its version, the command that ran, and whether a coding agent was
// running it. It carries names only. The command is the command's name, never
// its arguments or flag values, so nothing a user typed reaches the API.
type ClientInfo struct {
	Name        string // the client, such as "cli"
	Version     string // the client's version, such as "2.19.3"
	Command     string // the command, such as "analytics session list"
	CodingAgent string // the coding agent running the client, such as "claude-code"; empty when none
	Source      string // where the coding agent learned to call LiveKit, such as "skills"; empty when not set
}

// WithClientInfo sends info with every request: in the X-LiveKit-* headers,
// and in User-Agent. Empty fields are left out.
func WithClientInfo(info ClientInfo) oapi.ClientOption {
	return oapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		set := func(key, value string) {
			if value != "" {
				req.Header.Set(key, value)
			}
		}
		set(HeaderClient, info.Name)
		set(HeaderClientVersion, info.Version)
		set(HeaderCommand, info.Command)
		set(HeaderCodingAgent, info.CodingAgent)
		set(HeaderSource, info.Source)
		if info.Version != "" {
			req.Header.Set("User-Agent", "livekit-cli/"+info.Version)
		}
		return nil
	})
}

// DetectSource returns the source set in EnvSource, cleaned like a coding
// agent's name, or "" when it's unset. getenv is os.Getenv outside tests.
func DetectSource(getenv func(string) string) string {
	return cleanAgentName(getenv(EnvSource))
}

// DetectCodingAgent names the coding agent whose shell this process runs in,
// from the environment variables those agents set, or returns "" when there's
// none. getenv is os.Getenv outside tests.
//
// AI_AGENT is a convention shared across vendors; its value starts with the
// agent's name, as in "claude-code_2-1-317_agent". The rest are set by one
// agent each: Codex sets CODEX_VERSION on every command it runs, and Cursor
// sets CURSOR_AGENT in its agent's terminal (not in a terminal a person uses).
func DetectCodingAgent(getenv func(string) string) string {
	if v := getenv("AI_AGENT"); v != "" {
		name, _, _ := strings.Cut(v, "_")
		if name = cleanAgentName(name); name != "" {
			return name
		}
	}
	switch {
	case getenv("CLAUDECODE") == "1":
		return "claude-code"
	case getenv("GEMINI_CLI") == "1":
		return "gemini-cli"
	case getenv("CODEX_VERSION") != "":
		return "codex"
	case getenv("CURSOR_AGENT") != "":
		return "cursor"
	}
	return ""
}

// maxAgentNameLen caps a detected coding agent's name. Known names are under
// 20 characters, so this leaves headroom while keeping the header bounded.
const maxAgentNameLen = 64

// cleanAgentName keeps a detected name to lowercase letters, digits, dots and
// dashes, at most maxAgentNameLen characters, so an unexpected value can't put
// anything else into a header.
func cleanAgentName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if b.Len() == maxAgentNameLen {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
