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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func text(role, content string) map[string]any {
	return map[string]any{"role": role, "parts": []any{map[string]any{"type": "text", "content": content}}}
}

func toolCall(id, name string, args map[string]any) map[string]any {
	return map[string]any{"role": "assistant", "parts": []any{
		map[string]any{"type": "tool_call", "id": id, "name": name, "arguments": args},
	}}
}

func toolResult(id, name string, response any) map[string]any {
	return map[string]any{"role": "tool", "parts": []any{
		map[string]any{"type": "tool_call_response", "id": id, "name": name, "response": response},
	}}
}

// spans decodes spans as the Public API returns them.
func spans(t *testing.T, items ...map[string]any) []oapi.LivekitPublicapiObservabilityV1Span {
	t.Helper()
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	var out []oapi.LivekitPublicapiObservabilityV1Span
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func llmSpan(id string, attrs map[string]any) map[string]any {
	return map[string]any{"name": "llm_request", "spanId": id, "attributes": attrs}
}

var instructions = []any{map[string]any{"type": "text", "content": "You are Sam."}}

func TestReplayCallsRebuildInputDeltas(t *testing.T) {
	calls, err := replayCallsFromSpans(spans(t,
		map[string]any{"name": "agent_turn", "spanId": "a0"},
		llmSpan("s1", map[string]any{
			"gen_ai.request.model":       "openai/gpt-4.1-mini",
			"gen_ai.system_instructions": instructions,
			"gen_ai.input.messages":      []any{text("user", "hi"), text("system", "guide")},
			"gen_ai.output.messages":     []any{text("assistant", "hello")},
		}),
		// continues s1: its trailing guide is replaced, and its instructions apply
		llmSpan("s2", map[string]any{
			"gen_ai.request.model":       "openai/gpt-4.1-mini",
			"lk.input.base_span_id":      "s1",
			"lk.input.dropped_from_base": 1,
			"gen_ai.input.messages":      []any{text("assistant", "hello"), text("user", "bye"), text("system", "guide")},
			"gen_ai.output.messages":     []any{text("assistant", "goodbye")},
		}),
	))
	require.NoError(t, err)
	require.Len(t, calls, 2)
	require.Equal(t, instructions, calls[1].request.SystemInstructions)
	require.Equal(t, []any{text("user", "hi"), text("assistant", "hello"), text("user", "bye"), text("system", "guide")},
		decoded(t, calls[1].request.InputMessages))
}

func TestReplayCallsNeedRecordedContent(t *testing.T) {
	_, err := replayCallsFromSpans(spans(t, llmSpan("s1", map[string]any{"gen_ai.request.model": "m"})))
	require.ErrorContains(t, err, "no message content")

	_, err = replayCallsFromSpans(spans(t, map[string]any{"name": "agent_session", "spanId": "a"}))
	require.ErrorContains(t, err, "no LLM requests")
}

func decoded(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func recordedCall(t *testing.T, input ...any) replayCall {
	t.Helper()
	var req replayRequest
	raw, err := json.Marshal(map[string]any{
		"model":               "openai/gpt-4.1-mini",
		"provider":            "livekit",
		"system_instructions": instructions,
		"input_messages":      input,
	})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &req))
	return replayCall{request: req}
}

func TestReplayDiff(t *testing.T) {
	call := recordedCall(t, text("user", "+1 415 555 0142"), toolCall("c1", "lookup_caller", map[string]any{"phone": "+1 415 555 0142"}),
		toolResult("c1", "lookup_caller", map[string]any{"found": true}))

	same := call.request
	require.Empty(t, call.diff(same), "a request equal to the recorded one")

	noTools := call.request
	noTools.ToolDefinitions = []any{}
	require.Empty(t, call.diff(noTools), "an empty list is the attribute the span omits")

	changedTool := recordedCall(t, text("user", "+1 415 555 0142"), toolCall("c1", "lookup_caller", map[string]any{"phone": "+1 415 555 0142"}),
		toolResult("c1", "lookup_caller", map[string]any{"found": false})).request
	require.Contains(t, call.diff(changedTool), "input message 3 of 3 differs")

	prompt := call.request
	prompt.SystemInstructions = []any{map[string]any{"type": "text", "content": "You are Alex."}}
	require.Contains(t, call.diff(prompt), "instructions differ")

	model := call.request
	model.Model = "openai/gpt-5"
	require.Contains(t, call.diff(model), `model: recorded "openai/gpt-4.1-mini", replayed "openai/gpt-5"`)

	shorter := recordedCall(t, text("user", "+1 415 555 0142")).request
	require.Contains(t, call.diff(shorter), "input message 2 of 3 differs")
}

func TestReplayUserTurns(t *testing.T) {
	greeting := text("assistant", "Hello! How can I help?")
	calls := []replayCall{
		recordedCall(t), // the greeting
		recordedCall(t, greeting, text("user", "hi")), // first user turn
		recordedCall(t, greeting, text("user", "hi"), text("assistant", "phone?"), text("user", "555")),
		// a tool call continues the same turn
		recordedCall(t, greeting, text("user", "hi"), text("assistant", "phone?"), text("user", "555"),
			toolCall("c1", "lookup_caller", nil), toolResult("c1", "lookup_caller", "{}")),
		// a context trimmed to its latest stretch still continues what was said
		recordedCall(t, text("user", "555"), text("assistant", "found you"), text("user", "thanks")),
	}
	require.Equal(t, [][]string{nil, {"hi"}, {"555"}, {}, {"thanks"}}, normalizeTurns(replayUserTurns(calls)))
}

func normalizeTurns(turns [][]string) [][]string {
	for i, t := range turns {
		if len(t) == 0 && t != nil {
			turns[i] = []string{}
		}
	}
	return turns
}

func postReplay(t *testing.T, r *replayServer, req replayRequest) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r.handle(w, httptest.NewRequest(http.MethodPost, "/llm", bytes.NewReader(body)))
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return w, resp
}

func newTestReplayServer(calls []replayCall) *replayServer {
	return &replayServer{calls: calls, changed: make(chan struct{})}
}

func TestReplayServerAnswersInOrder(t *testing.T) {
	first := recordedCall(t, text("user", "hi"))
	first.output = []any{text("assistant", "hello")}
	first.responseID = "chatcmpl-1"
	second := recordedCall(t, text("user", "hi"), text("assistant", "hello"), text("user", "bye"))
	r := newTestReplayServer([]replayCall{first, second})

	w, resp := postReplay(t, r, first.request)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "chatcmpl-1", resp["response_id"])
	require.Equal(t, decoded(t, first.output), resp["output_messages"])

	w, _ = postReplay(t, r, second.request)
	require.Equal(t, http.StatusOK, w.Code)

	w, resp = postReplay(t, r, second.request)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, resp["error"], "the session made only 2 LLM requests")
}

func TestReplayServerFailsOutOfOrderRequests(t *testing.T) {
	first := recordedCall(t, text("user", "hi"))
	second := recordedCall(t, text("user", "hi"), text("assistant", "hello"), text("user", "bye"))
	r := newTestReplayServer([]replayCall{first, second})

	w, resp := postReplay(t, r, second.request)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, resp["error"], "LLM request 1 of 2 diverged from the session: came out of order: it is the session's request 2, and request 1 was due")

	// once failed, the replay refuses even the request that was due
	w, resp = postReplay(t, r, first.request)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, resp["error"], "came out of order")
	served, _, failure := r.state()
	require.Zero(t, served)
	require.Error(t, failure)
}
