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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/livekit/protocol/auth"
)

type projectRoutingResponse struct {
	ProjectId string `json:"project_id"`
	Type      string `json:"type"`
	AgentsURL string `json:"agents_url"`
}

func fetchProjectRouting(ctx context.Context) (*projectRoutingResponse, error) {
	if project == nil {
		return nil, fmt.Errorf("project is required to fetch routing")
	}

	token, err := auth.NewAccessToken(project.APIKey, project.APISecret).ToJWT()
	if err != nil {
		return nil, err
	}

	logger.Infow("requesting project routing", "server_url", serverURL, "project_id", project.ProjectId)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/cli/project-routing", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("project routing request failed: %s", resp.Status)
	}

	var payload projectRoutingResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	payload.AgentsURL = strings.TrimSpace(payload.AgentsURL)
	payload.Type = strings.ToLower(strings.TrimSpace(payload.Type))
	logger.Infow("project routing response", "project_id", payload.ProjectId, "type", payload.Type, "agents_url", payload.AgentsURL)
	return &payload, nil
}
