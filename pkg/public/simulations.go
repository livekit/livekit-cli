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

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// ListSimulationRuns returns one page of the simulation runs for a project.
func (c *Client) ListSimulationRuns(ctx context.Context, projectID string, params *oapi.SimulationServiceListSimulationRunsParams) (runs []oapi.LivekitSimulationRun, nextCursor string, err error) {
	resp, err := c.gen.SimulationServiceListSimulationRunsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}

// GetSimulationRun returns a single simulation run by id.
func (c *Client) GetSimulationRun(ctx context.Context, projectID, runID string) (*oapi.LivekitSimulationRun, error) {
	resp, err := c.gen.SimulationServiceGetSimulationRunWithResponse(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.Run, "simulation run")
}

// CreateSimulationRun starts a new simulation run. When ScenarioGroup is unset,
// the response carries a presigned POST target for the agent source archive.
func (c *Client) CreateSimulationRun(ctx context.Context, projectID string, body oapi.SimulationServiceCreateSimulationRunJSONRequestBody) (*oapi.LivekitPublicapiSimulationsV1SimulationRunsCreateResponse, error) {
	resp, err := c.gen.SimulationServiceCreateSimulationRunWithResponse(ctx, projectID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}

// CancelSimulationRun cancels an in-progress simulation run.
func (c *Client) CancelSimulationRun(ctx context.Context, projectID, runID string) error {
	resp, err := c.gen.SimulationServiceCancelSimulationRunWithResponse(ctx, projectID, runID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}
