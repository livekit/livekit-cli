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
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentInsights = `{"summary":{"sessions":"2","turns":"3","e2eLatency":{"p50Ms":2465.6,"count":"1"}},` +
	`"agents":[{"agentId":"CA_1","agentName":"support","sessions":"2","turns":"3"}],` +
	`"models":[{"component":"MODEL_COMPONENT_LLM","provider":"openai","model":"gpt-4.1","requests":"4"}]}`

func TestGetAgentInsights(t *testing.T) {
	tests := []struct {
		name string
		opts AgentInsightsOptions
		want url.Values
	}{
		{name: "zero options send nothing", want: url.Values{}},
		{
			name: "range and agents",
			opts: AgentInsightsOptions{
				Range:      TimeRange{Start: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)},
				AgentIDs:   []string{" CA_1 ", "CA_2"},
				AgentNames: []string{"self-hosted"},
			},
			want: url.Values{
				"range.startTime": {"2026-11-01T00:00:00Z"},
				"range.endTime":   {"2026-11-08T00:00:00Z"},
				"agentIds":        {"CA_1", "CA_2"},
				"agentNames":      {"self-hosted"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, gotAuth, gotPath, gotQuery := queryServer(t, http.StatusOK, agentInsights)

			resp, err := c.GetAgentInsights(context.Background(), "p1", tt.opts)
			require.NoError(t, err)

			assert.Equal(t, "Bearer sekret", *gotAuth)
			assert.Equal(t, "/v0/projects/p1/agent-insights", *gotPath)
			assert.Equal(t, tt.want, *gotQuery)

			assert.Equal(t, "2", *resp.Summary.Sessions)
			assert.Equal(t, 2465.6, *resp.Summary.E2eLatency.P50Ms)
			require.Len(t, *resp.Agents, 1)
			assert.Equal(t, "CA_1", *(*resp.Agents)[0].AgentId)
			require.Len(t, *resp.Models, 1)
			assert.Equal(t, "gpt-4.1", *(*resp.Models)[0].Model)
		})
	}
}

// TestGetAgentInsightsRejectsBlankAgents confirms a blank agent ID or name
// fails Validate, and fails GetAgentInsights before any request is sent.
func TestGetAgentInsightsRejectsBlankAgents(t *testing.T) {
	for _, opts := range []AgentInsightsOptions{
		{AgentIDs: []string{"CA_1", " "}},
		{AgentNames: []string{""}},
	} {
		require.ErrorContains(t, opts.Validate(), "must not be empty")

		c, called := noRequestServer(t)
		_, err := c.GetAgentInsights(context.Background(), "p1", opts)
		require.ErrorContains(t, err, "must not be empty")
		assert.False(t, *called, "no request should be sent")
	}
}

// TestGetAgentInsightsErrors checks the server's refusal of a range passes
// through with its message.
func TestGetAgentInsightsErrors(t *testing.T) {
	c, _, _, _ := queryServer(t, http.StatusBadRequest, `{"code":3,"message":"the time range can be at most 30 days"}`)
	_, err := c.GetAgentInsights(context.Background(), "p1", AgentInsightsOptions{})
	assert.EqualError(t, err, "the time range can be at most 30 days")
}
