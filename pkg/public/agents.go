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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// Agent is a cloud agent as AgentService returns it. Its Info is LiveKit's
// own livekit.AgentInfo, which the API sends as protojson, so it decodes as
// TelephonyService's messages do (see telephony.go): lk's agent commands
// already print that type, and the round trip keeps every field.
type Agent struct {
	// Info is the agent, with a deployment for each region where it runs.
	// The API leaves out a version, agent name or deploy time that LiveKit
	// doesn't know, and the secrets.
	Info *livekit.AgentInfo
	// Status is the deployment status of the agent.
	Status oapi.LivekitPublicapiAgentsV1DeploymentStatus
	// AgentBuilderID is the ID of the Agent Builder agent that runs as this
	// agent, or "" for an agent deployed from code.
	AgentBuilderID string
}

// agentJSON is Agent as the API sends it.
type agentJSON struct {
	Info           json.RawMessage                               `json:"info"`
	Status         oapi.LivekitPublicapiAgentsV1DeploymentStatus `json:"status"`
	AgentBuilderID string                                        `json:"agentBuilderId"`
}

func (a agentJSON) decode() (*Agent, error) {
	if a.Info == nil {
		return nil, errors.New("unexpected response from server: missing info")
	}
	info := new(livekit.AgentInfo)
	if err := telephonyUnmarshal.Unmarshal(a.Info, info); err != nil {
		return nil, err
	}
	return &Agent{Info: info, Status: a.Status, AgentBuilderID: a.AgentBuilderID}, nil
}

// ListAgents returns the project's cloud agents, in ID order. The list is
// always one page.
func (c *Client) ListAgents(ctx context.Context, projectID string) ([]*Agent, error) {
	resp, err := c.gen.AgentServiceListAgentsWithResponse(ctx, projectID, &oapi.AgentServiceListAgentsParams{})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var env struct {
		Items    []agentJSON `json:"items"`
		PageInfo struct {
			HasMore bool `json:"hasMore"`
		} `json:"pageInfo"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	if env.PageInfo.HasMore {
		return nil, errors.New("unexpected response from server: the list has more than one page")
	}
	out := make([]*Agent, 0, len(env.Items))
	for _, item := range env.Items {
		agent, err := item.decode()
		if err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, nil
}

// GetAgent returns the project's cloud agent agentID. An agent the project
// doesn't have is NotFound "agent not found" (see IsAgentNotFound).
func (c *Client) GetAgent(ctx context.Context, projectID, agentID string) (*Agent, error) {
	resp, err := c.gen.AgentServiceGetAgentWithResponse(ctx, projectID, agentID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var env struct {
		Agent *agentJSON `json:"agent"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	if env.Agent == nil {
		return nil, errors.New("unexpected response from server: missing agent")
	}
	return env.Agent.decode()
}

// agentNotFound is the message of AgentService's NotFound for an agent the
// project doesn't have.
const agentNotFound = "agent not found"

// IsAgentNotFound reports whether err is AgentService's NotFound for an
// agent, not for the project.
func IsAgentNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Message == agentNotFound
}
