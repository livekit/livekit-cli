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
	"errors"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// AgentInsightsOptions selects the agents and the range of GetAgentInsights.
// Zero values read every agent of the project over the last 7 days.
type AgentInsightsOptions struct {
	// Range bounds the data. The read excludes End, and the server refuses a
	// range longer than 30 days.
	Range TimeRange
	// AgentIDs (CA_...) and AgentNames select agents: the read returns the
	// data of the agents in either list. A self-hosted agent has no ID, so it
	// is selected by name. Both empty read every agent. The server refuses
	// more than 25 together.
	AgentIDs   []string
	AgentNames []string
}

// agentInsightsParams translates opts into the generated query params,
// rejecting a blank agent ID or name before any request is sent.
func agentInsightsParams(opts AgentInsightsOptions) (*oapi.AnalyticsServiceGetAgentInsightsParams, error) {
	params := &oapi.AnalyticsServiceGetAgentInsightsParams{}
	params.RangeStartTime, params.RangeEndTime = opts.Range.params()
	ids, err := nonBlank(opts.AgentIDs, "agent ID")
	if err != nil {
		return nil, err
	}
	names, err := nonBlank(opts.AgentNames, "agent name")
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		params.AgentIds = &ids
	}
	if len(names) > 0 {
		params.AgentNames = &names
	}
	return params, nil
}

// nonBlank returns values trimmed, failing on one that is blank; what names
// a value in the error.
func nonBlank(values []string, what string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, errors.New(what + " must not be empty")
		}
		out = append(out, v)
	}
	return out, nil
}

// Validate reports a blank agent ID or name. GetAgentInsights makes the same
// check before sending a request; callers can run it earlier.
func (o AgentInsightsOptions) Validate() error {
	_, err := agentInsightsParams(o)
	return err
}

// GetAgentInsights returns the agent insights of a project over a range: the
// totals and latencies of its agents, one item for each agent and deployment,
// and one item for each model the agents used. It is the data that the agent
// dashboards of LiveKit Cloud show, from the turns, model requests and tool
// calls the agents recorded; simulated sessions are left out. Every project
// member can read it. A range that is longer than 30 days or starts after now
// is a 400.
func (c *Client) GetAgentInsights(ctx context.Context, projectID string, opts AgentInsightsOptions) (*oapi.LivekitPublicapiAnalyticsV1AgentInsightsGetResponse, error) {
	params, err := agentInsightsParams(opts)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.AnalyticsServiceGetAgentInsightsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}
