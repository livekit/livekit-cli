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

// ListProjectMembers returns the members of a project.
func (c *Client) ListProjectMembers(ctx context.Context, projectID string) ([]oapi.LivekitPublicapiProjectsV1ProjectMember, error) {
	resp, err := c.gen.ProjectServiceListMembersWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetProjectMember returns a single project member by user id.
func (c *Client) GetProjectMember(ctx context.Context, projectID, userID string) (*oapi.LivekitPublicapiProjectsV1ProjectMember, error) {
	resp, err := c.gen.ProjectServiceGetMemberWithResponse(ctx, projectID, userID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.Member, "member")
}

// UpdateProjectMember changes a project member's role.
func (c *Client) UpdateProjectMember(ctx context.Context, projectID, userID string, role Role) (*oapi.LivekitPublicapiProjectsV1ProjectMember, error) {
	resp, err := c.gen.ProjectServiceUpdateMemberWithResponse(ctx, projectID, userID, oapi.ProjectServiceUpdateMemberJSONRequestBody{
		Role: ptr(int32(role)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.Member, "member")
}

// RemoveProjectMember removes a member from a project.
func (c *Client) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	resp, err := c.gen.ProjectServiceRemoveMemberWithResponse(ctx, projectID, userID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// AddWorkspaceMembersToProject grants a set of existing workspace members access
// to a project at the given role, returning the resulting project memberships.
func (c *Client) AddWorkspaceMembersToProject(ctx context.Context, projectID string, userIDs []string, role Role) ([]oapi.LivekitPublicapiProjectsV1ProjectMember, error) {
	resp, err := c.gen.ProjectServiceAddWorkspaceMembersToProjectWithResponse(ctx, projectID, oapi.ProjectServiceAddWorkspaceMembersToProjectJSONRequestBody{
		UserIds: &userIDs,
		Role:    ptr(int32(role)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}
