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

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/assistant"
)

// X-ray mode shows the assistant's own pipeline at work: the models it uses,
// per-turn timings, the turn detector's live end-of-turn estimate, and usage.
// The data comes from the session events console mode already sends.

type (
	overlayPipeline struct {
		Type          string `json:"type"`
		STT           string `json:"stt"`
		LLM           string `json:"llm"`
		TTS           string `json:"tts"`
		TurnDetection string `json:"turn_detection"`
	}
	overlayEOT struct {
		Type        string  `json:"type"`
		Probability float32 `json:"probability"`
		Threshold   float32 `json:"threshold"`
	}
	// overlayMetrics carries one message's timings, in seconds. Fields the
	// agent didn't report are left out.
	overlayMetrics struct {
		Type               string   `json:"type"`
		Role               string   `json:"role"` // user or assistant
		EndOfTurnDelay     *float64 `json:"end_of_turn_delay,omitempty"`
		TranscriptionDelay *float64 `json:"transcription_delay,omitempty"`
		LLMTTFT            *float64 `json:"llm_ttft,omitempty"`
		TTSTTFB            *float64 `json:"tts_ttfb,omitempty"`
		E2ELatency         *float64 `json:"e2e_latency,omitempty"`
	}
	overlayUsage struct {
		Type   string            `json:"type"`
		Models []overlayModelUse `json:"models"`
	}
	overlayModelUse struct {
		Kind          string  `json:"kind"` // stt, llm, or tts
		Model         string  `json:"model"`
		InputTokens   int32   `json:"input_tokens,omitempty"`
		OutputTokens  int32   `json:"output_tokens,omitempty"`
		Characters    int32   `json:"characters,omitempty"`
		AudioDuration float64 `json:"audio_duration,omitempty"`
	}
)

func metricsMessage(role string, m *agent.MetricsReport) overlayMetrics {
	return overlayMetrics{
		Type:               "metrics",
		Role:               role,
		EndOfTurnDelay:     m.EndOfTurnDelay,
		TranscriptionDelay: m.TranscriptionDelay,
		LLMTTFT:            m.LlmNodeTtft,
		TTSTTFB:            m.TtsNodeTtfb,
		E2ELatency:         m.E2ELatency,
	}
}

func usageMessage(u *agent.AgentSessionUsage) overlayUsage {
	msg := overlayUsage{Type: "usage"}
	for _, mu := range u.GetModelUsage() {
		switch {
		case mu.GetLlm() != nil:
			l := mu.GetLlm()
			msg.Models = append(msg.Models, overlayModelUse{Kind: "llm", Model: l.GetModel(), InputTokens: l.GetInputTokens(), OutputTokens: l.GetOutputTokens()})
		case mu.GetTts() != nil:
			t := mu.GetTts()
			msg.Models = append(msg.Models, overlayModelUse{Kind: "tts", Model: t.GetModel(), Characters: t.GetCharactersCount(), AudioDuration: t.GetAudioDuration()})
		case mu.GetStt() != nil:
			st := mu.GetStt()
			msg.Models = append(msg.Models, overlayModelUse{Kind: "stt", Model: st.GetModel(), AudioDuration: st.GetAudioDuration()})
		}
	}
	return msg
}

// showAgentCode shows a code card the agent sent about itself, such as its
// own AgentSession setup.
func (s *assistantSession) showAgentCode(m agentMessage) {
	if m.Code == "" {
		return
	}
	s.agentTurnForReply()
	s.snippetN++
	id := fmt.Sprintf("self%d", s.snippetN)
	s.snippets[id] = m.Code
	s.out.Send(overlaySnippet{"snippet", s.agentTurn, id, m.Title, normalizeLang(m.Language), m.Code, false})
}

// Startup errors show as a card in the overlay with what went wrong and the
// command that fixes it, since the terminal is hidden behind the overlay.

type overlayError struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // a command to copy
}

// errNoCredentials marks an error loading the LiveKit Cloud credentials.
type errNoCredentials struct{ err error }

func (e errNoCredentials) Error() string { return e.err.Error() }
func (e errNoCredentials) Unwrap() error { return e.err }

func startupError(err error) overlayError {
	var creds errNoCredentials
	switch {
	case errors.Is(err, assistant.ErrNoUV):
		return overlayError{Type: "error",
			Title:  "Install uv to start the assistant",
			Detail: "The assistant is a Python voice agent, and lk runs it with uv, a Python package manager. Install uv, then run lk assistant again.",
			Fix:    "curl -LsSf https://astral.sh/uv/install.sh | sh"}
	case errors.As(err, &creds):
		return overlayError{Type: "error",
			Title:  "Connect lk to LiveKit Cloud",
			Detail: "The assistant uses LiveKit Inference through your LiveKit Cloud project. " + firstLine(creds.Error()),
			Fix:    "lk cloud auth"}
	default:
		return overlayError{Type: "error",
			Title:  "Couldn't start the assistant",
			Detail: truncate(strings.TrimSpace(err.Error()), 600)}
	}
}

func (s *assistantSession) showError(e overlayError) {
	s.out.Send(e)
	s.out.Send(overlayNotice{Type: "notice", Text: e.Title, Sticky: true})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
