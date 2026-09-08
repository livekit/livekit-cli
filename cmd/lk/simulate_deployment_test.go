// Copyright 2025 LiveKit, Inc.
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
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestSetSimulationCreateDeploymentEmitsField14(t *testing.T) {
	req := &livekit.SimulationRun_Create_Request{AgentName: "myudda-agent"}
	setSimulationCreateDeployment(req, "staging")

	raw, err := proto.Marshal(req)
	require.NoError(t, err)

	var got string
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		require.False(t, n < 0)
		raw = raw[n:]
		if typ != protowire.BytesType {
			_, n = protowire.ConsumeFieldValue(num, typ, raw)
			require.False(t, n < 0)
			raw = raw[n:]
			continue
		}
		val, n := protowire.ConsumeBytes(raw)
		require.False(t, n < 0)
		raw = raw[n:]
		if num == simulationCreateDeploymentField {
			got = string(val)
		}
	}
	require.Equal(t, "staging", got)
}

func TestSetSimulationCreateDeploymentEmptyIsOmitted(t *testing.T) {
	req := &livekit.SimulationRun_Create_Request{AgentName: "myudda-agent"}
	setSimulationCreateDeployment(req, "")
	require.Empty(t, req.ProtoReflect().GetUnknown())
}
