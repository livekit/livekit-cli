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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// replayRequest is an LLM request as an llm_request span records it (the GenAI
// semantic conventions), which is also what the agent SDK posts in replay mode.
// Values are decoded JSON, so two requests compare with reflect.DeepEqual.
type replayRequest struct {
	Model              string `json:"model"`
	Provider           string `json:"provider"`
	SystemInstructions []any  `json:"system_instructions"`
	InputMessages      []any  `json:"input_messages"`
	ToolDefinitions    []any  `json:"tool_definitions"`
}

// replayCall is one LLM request the session made and the output it got.
type replayCall struct {
	spanID     string
	request    replayRequest
	output     []any // gen_ai.output.messages
	responseID string
}

// replayCallsFromSpans rebuilds the session's LLM requests, in the order they
// were made, from its llm_request spans. A span recorded with input deltas
// (lk.input.base_span_id) continues its base span's input: the base's last
// lk.input.dropped_from_base messages are replaced by the span's own, and its
// instructions apply when the span records none.
func replayCallsFromSpans(spans []oapi.LivekitPublicapiObservabilityV1Span) ([]replayCall, error) {
	var calls []replayCall
	byID := map[string]*replayCall{}
	for _, span := range spans {
		if deref(span.Name) != "llm_request" {
			continue
		}
		attrs, err := spanAttributes(span)
		if err != nil {
			return nil, err
		}
		if _, ok := attrs["gen_ai.output.messages"]; !ok {
			if _, ok := attrs["gen_ai.input.messages"]; !ok {
				return nil, errors.New("the session's LLM spans carry no message content, so there is nothing to replay " +
					"(the agent ran with OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT off, or the content was redacted)")
			}
		}
		call := replayCall{
			spanID: deref(span.SpanId),
			request: replayRequest{
				Model:              stringAttr(attrs, "gen_ai.request.model"),
				Provider:           stringAttr(attrs, "gen_ai.provider.name"),
				SystemInstructions: listAttr(attrs, "gen_ai.system_instructions"),
				InputMessages:      listAttr(attrs, "gen_ai.input.messages"),
				ToolDefinitions:    listAttr(attrs, "gen_ai.tool.definitions"),
			},
			output:     listAttr(attrs, "gen_ai.output.messages"),
			responseID: stringAttr(attrs, "gen_ai.response.id"),
		}
		if baseID := stringAttr(attrs, "lk.input.base_span_id"); baseID != "" {
			base, ok := byID[baseID]
			if !ok {
				return nil, fmt.Errorf("LLM span %s continues span %s, which the session's traces don't include", call.spanID, baseID)
			}
			dropped, _ := attrs["lk.input.dropped_from_base"].(float64)
			keep := len(base.request.InputMessages) - int(dropped)
			if keep < 0 {
				return nil, fmt.Errorf("LLM span %s drops more messages than span %s recorded", call.spanID, baseID)
			}
			call.request.InputMessages = append(append([]any{}, base.request.InputMessages[:keep]...), call.request.InputMessages...)
			if len(call.request.SystemInstructions) == 0 {
				call.request.SystemInstructions = base.request.SystemInstructions
			}
		}
		calls = append(calls, call)
		byID[call.spanID] = &calls[len(calls)-1]
	}
	if len(calls) == 0 {
		return nil, errors.New("the session made no LLM requests, so there is nothing to replay")
	}
	return calls, nil
}

func spanAttributes(span oapi.LivekitPublicapiObservabilityV1Span) (map[string]any, error) {
	attrs := map[string]any{}
	if span.Attributes == nil {
		return attrs, nil
	}
	raw, err := json.Marshal(span.Attributes)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return nil, err
	}
	return attrs, nil
}

func stringAttr(attrs map[string]any, key string) string {
	s, _ := attrs[key].(string)
	return s
}

// listAttr reads a JSON-array attribute; one still encoded as a string is decoded.
func listAttr(attrs map[string]any, key string) []any {
	switch v := attrs[key].(type) {
	case []any:
		return v
	case string:
		var list []any
		if json.Unmarshal([]byte(v), &list) == nil {
			return list
		}
	}
	return nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// diff describes how got differs from the recorded request, or "" when it is
// the same request.
func (c replayCall) diff(got replayRequest) string {
	want := c.request
	switch {
	case want.Model != got.Model:
		return fmt.Sprintf("model: recorded %q, replayed %q", want.Model, got.Model)
	case want.Provider != got.Provider:
		return fmt.Sprintf("provider: recorded %q, replayed %q", want.Provider, got.Provider)
	case !jsonEqual(want.SystemInstructions, got.SystemInstructions):
		return "instructions differ\n" + sideBySide(want.SystemInstructions, got.SystemInstructions)
	case !jsonEqual(want.ToolDefinitions, got.ToolDefinitions):
		return "tools differ\n" + sideBySide(want.ToolDefinitions, got.ToolDefinitions)
	}
	for i := 0; i < max(len(want.InputMessages), len(got.InputMessages)); i++ {
		var w, g any
		if i < len(want.InputMessages) {
			w = want.InputMessages[i]
		}
		if i < len(got.InputMessages) {
			g = got.InputMessages[i]
		}
		if !jsonEqual(w, g) {
			return fmt.Sprintf("input message %d of %d differs\n%s", i+1, len(want.InputMessages), sideBySide(w, g))
		}
	}
	return ""
}

// jsonEqual compares decoded JSON, treating a missing list as an empty one:
// an llm_request span omits an attribute that has nothing in it.
func jsonEqual(a, b any) bool {
	if la, ok := a.([]any); ok && len(la) == 0 {
		a = nil
	}
	if lb, ok := b.([]any); ok && len(lb) == 0 {
		b = nil
	}
	return reflect.DeepEqual(a, b)
}

func sideBySide(want, got any) string {
	return "  recorded: " + compactJSON(want) + "\n  replayed: " + compactJSON(got)
}

func compactJSON(v any) string {
	if v == nil {
		return "(none)"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// replayUserTurns returns, for each call, the user messages the session's user
// said before it: the ones its input adds to what the user had already said.
// An input may carry only the latest stretch of the conversation, so what the
// user had said is matched at its end.
func replayUserTurns(calls []replayCall) [][]string {
	var said []string
	turns := make([][]string, len(calls))
	for i, call := range calls {
		users := userTexts(call.request.InputMessages)
		known := 0
		for n := min(len(users), len(said)); n > 0; n-- {
			if reflect.DeepEqual(users[:n], said[len(said)-n:]) {
				known = n
				break
			}
		}
		turns[i] = users[known:]
		said = append(said, turns[i]...)
	}
	return turns
}

func userTexts(messages []any) []string {
	var texts []string
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		var text []string
		parts, _ := msg["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if part["type"] == "text" {
				s, _ := part["content"].(string)
				text = append(text, s)
			}
		}
		texts = append(texts, strings.Join(text, "\n"))
	}
	return texts
}
