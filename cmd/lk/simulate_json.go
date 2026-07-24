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
	"fmt"
	"io"
	"strings"

	"github.com/livekit/protocol/livekit"
	agent "github.com/livekit/protocol/livekit/agent"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const simulationRunJSONVersion = 1

type simulationRunJSON struct {
	Version          int                 `json:"version"`
	RunID            string              `json:"run_id"`
	ProjectID        string              `json:"project_id,omitempty"`
	Status           string              `json:"status"`
	Error            string              `json:"error,omitempty"`
	AgentName        string              `json:"agent_name,omitempty"`
	AgentDescription string              `json:"agent_description,omitempty"`
	Mode             string              `json:"mode,omitempty"`
	Jobs             []simulationJobJSON `json:"jobs"`
}

type simulationJobJSON struct {
	ID                string           `json:"id"`
	Label             string           `json:"label,omitempty"`
	Status            string           `json:"status"`
	Error             string           `json:"error,omitempty"`
	Instructions      string           `json:"instructions,omitempty"`
	AgentExpectations string           `json:"agent_expectations,omitempty"`
	Tags              []string         `json:"tags,omitempty"`
	RoomName          string           `json:"room_name,omitempty"`
	RoomID            string           `json:"room_id,omitempty"`
	ChatContext       *chatContextJSON `json:"chat_context,omitempty"`
}

// chatContextJSON deliberately matches livekit.agents.llm.ChatContext.to_dict().
// That lets a replay harness call ChatContext.from_dict(job["chat_context"])
// without translating or re-authoring any conversation turn.
type chatContextJSON struct {
	Items []map[string]any `json:"items"`
}

func writeSimulationRunJSON(w io.Writer, run *livekit.SimulationRun) error {
	if run == nil {
		return fmt.Errorf("cannot export a nil simulation run")
	}

	summary := decodeRunSummary(run)
	var histories map[string]*agent.ChatContext
	if summary != nil {
		histories = summary.ChatHistory
	}

	export := simulationRunJSON{
		Version:          simulationRunJSONVersion,
		RunID:            run.Id,
		ProjectID:        run.ProjectId,
		Status:           run.Status.String(),
		Error:            run.Error,
		AgentName:        run.AgentName,
		AgentDescription: run.AgentDescription,
		Mode:             run.Mode.String(),
		Jobs:             make([]simulationJobJSON, 0, len(run.Jobs)),
	}
	for _, job := range run.Jobs {
		if job == nil {
			continue
		}
		exportedJob := simulationJobJSON{
			ID:                job.Id,
			Label:             job.Label,
			Status:            job.Status.String(),
			Error:             job.Error,
			Instructions:      job.Instructions,
			AgentExpectations: job.AgentExpectations,
			Tags:              job.Tags,
			RoomName:          job.RoomName,
			RoomID:            job.RoomId,
		}
		if history := histories[job.Id]; history != nil {
			exportedJob.ChatContext = exportChatContext(history)
		}
		export.Jobs = append(export.Jobs, exportedJob)
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(export); err != nil {
		return fmt.Errorf("encode simulation run JSON: %w", err)
	}
	return nil
}

func exportChatContext(ctx *agent.ChatContext) *chatContextJSON {
	items := make([]map[string]any, 0, len(ctx.Items))
	for _, item := range ctx.Items {
		if item == nil {
			continue
		}
		switch value := item.Item.(type) {
		case *agent.ChatContext_ChatItem_Message:
			message := value.Message
			if message == nil {
				continue
			}
			content := make([]string, 0, len(message.Content))
			for _, part := range message.Content {
				if part != nil {
					content = append(content, part.GetText())
				}
			}
			exported := map[string]any{
				"type":        "message",
				"role":        strings.ToLower(message.Role.String()),
				"content":     content,
				"interrupted": message.Interrupted,
			}
			putIfNotEmpty(exported, "id", message.Id)
			if message.TranscriptConfidence != nil {
				exported["transcript_confidence"] = message.GetTranscriptConfidence()
			}
			if len(message.Extra) > 0 {
				exported["extra"] = message.Extra
			}
			putCreatedAt(exported, message.CreatedAt)
			items = append(items, exported)

		case *agent.ChatContext_ChatItem_FunctionCall:
			call := value.FunctionCall
			if call == nil {
				continue
			}
			exported := map[string]any{
				"type":      "function_call",
				"call_id":   call.CallId,
				"name":      call.Name,
				"arguments": call.Arguments,
			}
			putIfNotEmpty(exported, "id", call.Id)
			putCreatedAt(exported, call.CreatedAt)
			items = append(items, exported)

		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			output := value.FunctionCallOutput
			if output == nil {
				continue
			}
			exported := map[string]any{
				"type":     "function_call_output",
				"call_id":  output.CallId,
				"name":     output.Name,
				"output":   output.Output,
				"is_error": output.IsError,
			}
			putIfNotEmpty(exported, "id", output.Id)
			putCreatedAt(exported, output.CreatedAt)
			items = append(items, exported)

		case *agent.ChatContext_ChatItem_AgentHandoff:
			handoff := value.AgentHandoff
			if handoff == nil {
				continue
			}
			exported := map[string]any{
				"type":         "agent_handoff",
				"new_agent_id": handoff.NewAgentId,
			}
			putIfNotEmpty(exported, "id", handoff.Id)
			if handoff.OldAgentId != nil {
				exported["old_agent_id"] = handoff.GetOldAgentId()
			}
			putCreatedAt(exported, handoff.CreatedAt)
			items = append(items, exported)

		case *agent.ChatContext_ChatItem_AgentConfigUpdate:
			update := value.AgentConfigUpdate
			if update == nil {
				continue
			}
			exported := map[string]any{
				"type":          "agent_config_update",
				"tools_added":   update.ToolsAdded,
				"tools_removed": update.ToolsRemoved,
			}
			putIfNotEmpty(exported, "id", update.Id)
			if update.Instructions != nil {
				exported["instructions"] = update.GetInstructions()
			}
			putCreatedAt(exported, update.CreatedAt)
			items = append(items, exported)
		}
	}
	return &chatContextJSON{Items: items}
}

func putIfNotEmpty(item map[string]any, key, value string) {
	if value != "" {
		item[key] = value
	}
}

func putCreatedAt(item map[string]any, value *timestamppb.Timestamp) {
	if value == nil {
		return
	}
	item["created_at"] = float64(value.Seconds) + float64(value.Nanos)/1e9
}
