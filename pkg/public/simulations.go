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
	"fmt"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// CreateSimulationOptions configures a new simulation run. When ScenarioGroup
// is not provided the server generates NumSimulations scenarios from the
// uploaded agent source.
type CreateSimulationOptions struct {
	AgentName      string
	NumSimulations int32
	Concurrency    int32
	Mode           string // "text" (default) or "audio"
	Region         string
}

// parseSimulationMode maps a friendly mode name to the wire enum.
func parseSimulationMode(s string) (oapi.LivekitSimulationMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return oapi.SIMULATIONMODETEXT, nil
	case "audio":
		return oapi.SIMULATIONMODEAUDIO, nil
	default:
		return "", fmt.Errorf("invalid simulation mode %q (expected \"text\" or \"audio\")", s)
	}
}

// parseSimulationStatus maps a friendly status name to the wire enum.
func parseSimulationStatus(s string) (oapi.LivekitSimulationRunStatus, error) {
	switch "STATUS_" + strings.ToUpper(strings.TrimSpace(s)) {
	case "STATUS_PENDING_UPLOAD":
		return oapi.LivekitSimulationRunStatusSTATUSPENDINGUPLOAD, nil
	case "STATUS_GENERATING":
		return oapi.LivekitSimulationRunStatusSTATUSGENERATING, nil
	case "STATUS_RUNNING":
		return oapi.LivekitSimulationRunStatusSTATUSRUNNING, nil
	case "STATUS_SUMMARIZING":
		return oapi.LivekitSimulationRunStatusSTATUSSUMMARIZING, nil
	case "STATUS_COMPLETED":
		return oapi.LivekitSimulationRunStatusSTATUSCOMPLETED, nil
	case "STATUS_FAILED":
		return oapi.LivekitSimulationRunStatusSTATUSFAILED, nil
	case "STATUS_CANCELLED":
		return oapi.LivekitSimulationRunStatusSTATUSCANCELLED, nil
	default:
		return "", fmt.Errorf("invalid simulation status %q", s)
	}
}

// ListSimulationRuns returns one page of the simulation runs for a project,
// optionally filtered by status name (e.g. "running", "completed"). This
// operation is token-paginated: pageToken requests a specific page and the
// returned nextToken is non-empty when more pages remain.
func (c *Client) ListSimulationRuns(ctx context.Context, projectID, status, pageToken string) (runs []oapi.LivekitSimulationRun, nextToken string, err error) {
	params := &oapi.SimulationServiceListSimulationRunsParams{}
	if status != "" {
		s, perr := parseSimulationStatus(status)
		if perr != nil {
			return nil, "", perr
		}
		params.Status = &s
	}
	if pageToken != "" {
		params.PageTokenToken = ptr(pageToken)
	}
	resp, err := c.gen.SimulationServiceListSimulationRunsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	if pt := resp.JSON200.NextPageToken; pt != nil && pt.Token != nil {
		nextToken = *pt.Token
	}
	return items(resp.JSON200.Runs), nextToken, nil
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

// CreateSimulationRun starts a new simulation run. The response carries the run
// id plus a presigned POST target the caller must upload the agent bundle to.
func (c *Client) CreateSimulationRun(ctx context.Context, projectID string, opts CreateSimulationOptions) (*oapi.LivekitSimulationRunCreateResponse, error) {
	mode, err := parseSimulationMode(opts.Mode)
	if err != nil {
		return nil, err
	}
	body := oapi.SimulationServiceCreateSimulationRunJSONRequestBody{Mode: &mode}
	if opts.AgentName != "" {
		body.AgentName = ptr(opts.AgentName)
	}
	if opts.NumSimulations > 0 {
		body.NumSimulations = ptr(opts.NumSimulations)
	}
	if opts.Concurrency > 0 {
		body.Concurrency = ptr(opts.Concurrency)
	}
	if opts.Region != "" {
		body.Region = ptr(opts.Region)
	}
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
