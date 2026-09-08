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
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// simulationCreateDeploymentField is SimulationRun.Create.Request.deployment.
// Empty/unset = production, matching CreateAgentDispatchRequest.deployment.
const simulationCreateDeploymentField protowire.Number = 14

// setSimulationCreateDeployment writes deployment onto the create request.
// Prefer the generated field when this CLI's protocol module has it; otherwise
// emit protobuf field 14 as unknown bytes so Cloud can pin AgentDispatch before
// a protocol module bump lands here.
func setSimulationCreateDeployment(req *livekit.SimulationRun_Create_Request, deployment string) {
	if req == nil || deployment == "" {
		return
	}
	msg := req.ProtoReflect()
	if fd := msg.Descriptor().Fields().ByName("deployment"); fd != nil {
		msg.Set(fd, protoreflect.ValueOfString(deployment))
		return
	}
	var b []byte
	b = protowire.AppendTag(b, simulationCreateDeploymentField, protowire.BytesType)
	b = protowire.AppendString(b, deployment)
	msg.SetUnknown(append(msg.GetUnknown(), b...))
}
