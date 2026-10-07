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

// ListKeys returns the API keys of a project. Agent keys (created for cloud
// agents) are hidden unless includeAgentKeys is set. Secrets are never returned.
func (c *Client) ListKeys(ctx context.Context, projectID string, includeAgentKeys bool) ([]oapi.LivekitPublicapiProjectsV1AccessKey, error) {
	resp, err := c.gen.ProjectServiceListKeysWithResponse(ctx, projectID, &oapi.ProjectServiceListKeysParams{
		IncludeAgentKeys: ptr(includeAgentKeys),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Keys), nil
}

// ListScopedKeys returns a project's API keys split into those owned by the
// authenticated user (personal) and the rest (other).
func (c *Client) ListScopedKeys(ctx context.Context, projectID string) (personal, other []oapi.LivekitPublicapiProjectsV1AccessKey, err error) {
	resp, err := c.gen.ProjectServiceListScopedKeysWithResponse(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.PersonalKeys), items(resp.JSON200.OtherKeys), nil
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
