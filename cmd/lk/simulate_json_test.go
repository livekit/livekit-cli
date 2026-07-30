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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/livekit/protocol/livekit"
	agent "github.com/livekit/protocol/livekit/agent"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestWriteSimulationRunJSONExportsReplayableChatContext(t *testing.T) {
	instructions := "You are the booking agent."
	summary := &livekit.SimulationRunSummary{
		ChatHistory: map[string]*agent.ChatContext{
			"job-1": {
				Items: []*agent.ChatContext_ChatItem{
					{
						Item: &agent.ChatContext_ChatItem_AgentConfigUpdate{
							AgentConfigUpdate: &agent.AgentConfigUpdate{
								Id:           "config-1",
								Instructions: &instructions,
								ToolsAdded:   []string{"lookup_invoice"},
							},
						},
					},
					{
						Item: &agent.ChatContext_ChatItem_Message{
							Message: &agent.ChatMessage{
								Id:      "message-1",
								Role:    agent.ChatRole_USER,
								Content: []*agent.ChatMessage_ChatContent{{Payload: &agent.ChatMessage_ChatContent_Text{Text: "What is this charge?"}}},
							},
						},
					},
					{
						Item: &agent.ChatContext_ChatItem_FunctionCall{
							FunctionCall: &agent.FunctionCall{
								Id:        "call-item-1",
								CallId:    "call-1",
								Name:      "lookup_invoice",
								Arguments: `{"code":"HTL-1"}`,
							},
						},
					},
					{
						Item: &agent.ChatContext_ChatItem_FunctionCallOutput{
							FunctionCallOutput: &agent.FunctionCallOutput{
								Id:      "output-item-1",
								CallId:  "call-1",
								Name:    "lookup_invoice",
								Output:  `"Room (2 nights)" at 560 dollars`,
								IsError: false,
							},
						},
					},
				},
			},
		},
	}
	rawSummary, err := proto.Marshal(summary)
	require.NoError(t, err)
	encoder, err := zstd.NewWriter(nil)
	require.NoError(t, err)

	run := &livekit.SimulationRun{
		Id:          "run-1",
		ProjectId:   "project-1",
		Status:      livekit.SimulationRun_STATUS_COMPLETED,
		SummaryZstd: encoder.EncodeAll(rawSummary, nil),
		Jobs: []*livekit.SimulationRun_Job{
			{
				Id:                "job-1",
				Label:             "Invoice dispute",
				Status:            livekit.SimulationRun_Job_STATUS_COMPLETED,
				Instructions:      "Dispute the unknown charge.",
				AgentExpectations: "Read the invoice first.",
			},
		},
	}

	var output bytes.Buffer
	require.NoError(t, writeSimulationRunJSON(&output, run))

	var exported simulationRunJSON
	require.NoError(t, json.Unmarshal(output.Bytes(), &exported))
	require.Equal(t, simulationRunJSONVersion, exported.Version)

	var exportedRun livekit.SimulationRun
	require.NoError(t, protojson.Unmarshal(exported.Run, &exportedRun))
	require.Empty(t, exportedRun.SummaryZstd)
	require.Empty(t, cmp.Diff(run, &exportedRun, protocmp.Transform(),
		protocmp.IgnoreFields(&livekit.SimulationRun{}, "summary_zstd")))

	var exportedSummary livekit.SimulationRunSummary
	require.NoError(t, protojson.Unmarshal(exported.Summary, &exportedSummary))
	require.Empty(t, cmp.Diff(summary, &exportedSummary, protocmp.Transform()))
}

func TestWriteSimulationRunJSONOmitsSummaryWhenAbsent(t *testing.T) {
	run := &livekit.SimulationRun{
		Id:     "run-1",
		Status: livekit.SimulationRun_STATUS_FAILED,
		Jobs: []*livekit.SimulationRun_Job{
			{Id: "job-1", Status: livekit.SimulationRun_Job_STATUS_FAILED, Error: "timeout"},
		},
	}

	var output bytes.Buffer
	require.NoError(t, writeSimulationRunJSON(&output, run))

	var exported simulationRunJSON
	require.NoError(t, json.Unmarshal(output.Bytes(), &exported))
	require.Nil(t, exported.Summary)

	var exportedRun livekit.SimulationRun
	require.NoError(t, protojson.Unmarshal(exported.Run, &exportedRun))
	require.Empty(t, cmp.Diff(run, &exportedRun, protocmp.Transform()))
}
