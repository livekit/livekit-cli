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

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// GetCurrentUser returns the signed-in user.
func (c *Client) GetCurrentUser(ctx context.Context) (*oapi.LivekitPublicapiUsersV1User, error) {
	resp, err := c.gen.UserServiceGetCurrentUserWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.User, "user")
}

// GetUser returns a single user by id.
func (c *Client) GetUser(ctx context.Context, userID string) (*oapi.LivekitPublicapiUsersV1User, error) {
	resp, err := c.gen.UserServiceGetUserWithResponse(ctx, userID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.User, "user")
}

// ListUsers returns one page of the users in a project or workspace. At least
// one of projectID or workspaceID must be set. limit caps the page size (0 =
// server default); cursor requests a specific page. The returned nextCursor is
// non-empty when more pages remain.
func (c *Client) ListUsers(ctx context.Context, projectID, workspaceID string, limit int32, cursor string) (users []oapi.LivekitPublicapiUsersV1User, nextCursor string, err error) {
	if projectID == "" && workspaceID == "" {
		return nil, "", errors.New("a project or workspace is required to list users")
	}
	params := &oapi.UserServiceListUsersParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	if projectID != "" {
		params.ProjectId = ptr(projectID)
	}
	if workspaceID != "" {
		params.WorkspaceId = ptr(workspaceID)
	}
	resp, err := c.gen.UserServiceListUsersWithResponse(ctx, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}
