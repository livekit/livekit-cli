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

// KeyFilter narrows ListKeys. The zero value lists every key except agent keys.
type KeyFilter struct {
	// IncludeAgentKeys also lists the keys created for cloud agents.
	IncludeAgentKeys bool
	// OwnerID, when set, lists only the keys this user owns.
	OwnerID string
}

// ListKeys returns the API keys of a project that match filter, oldest first.
// Secrets are never returned.
func (c *Client) ListKeys(ctx context.Context, projectID string, filter KeyFilter) ([]oapi.LivekitPublicapiProjectsV1AccessKey, error) {
	params := &oapi.ProjectServiceListKeysParams{IncludeAgentKeys: ptr(filter.IncludeAgentKeys)}
	if filter.OwnerID != "" {
		params.OwnerId = ptr(filter.OwnerID)
	}
	resp, err := c.gen.ProjectServiceListKeysWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GenerateKey creates a new API key on a project. A service-account key has no
// owner, so it survives the generating user leaving the project. The returned
// secret is only ever available from this call.
func (c *Client) GenerateKey(ctx context.Context, projectID, description string, serviceAccount bool) (*oapi.LivekitPublicapiProjectsV1GenerateKeyResponse, error) {
	body := oapi.ProjectServiceGenerateKeyJSONRequestBody{IsServiceAccount: ptr(serviceAccount)}
	if description != "" {
		body.Description = &description
	}
	resp, err := c.gen.ProjectServiceGenerateKeyWithResponse(ctx, projectID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}

// DeleteKey revokes an API key on a project.
func (c *Client) DeleteKey(ctx context.Context, projectID, apiKey string) error {
	resp, err := c.gen.ProjectServiceDeleteKeyWithResponse(ctx, projectID, apiKey)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}
