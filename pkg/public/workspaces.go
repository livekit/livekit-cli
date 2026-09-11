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

// ListWorkspaces returns the workspaces the authenticated user can access.
func (c *Client) ListWorkspaces(ctx context.Context) ([]oapi.LivekitPublicapiWorkspacesV1Workspace, error) {
	resp, err := c.gen.WorkspaceServiceListWorkspacesWithResponse(ctx, &oapi.WorkspaceServiceListWorkspacesParams{
		PagePageSize: ptr(int32(100)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetWorkspace returns a single workspace by id.
func (c *Client) GetWorkspace(ctx context.Context, workspaceID string) (*oapi.LivekitPublicapiWorkspacesV1Workspace, error) {
	resp, err := c.gen.WorkspaceServiceGetWorkspaceWithResponse(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Workspace, nil
}

// CreateWorkspace creates a workspace. organizationID is optional.
func (c *Client) CreateWorkspace(ctx context.Context, name, organizationID string) (*oapi.LivekitPublicapiWorkspacesV1Workspace, error) {
	body := oapi.WorkspaceServiceCreateWorkspaceJSONRequestBody{Name: ptr(name)}
	if organizationID != "" {
		body.OrganizationId = ptr(organizationID)
	}
	resp, err := c.gen.WorkspaceServiceCreateWorkspaceWithResponse(ctx, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Workspace, nil
}

// UpdateWorkspace renames a workspace.
func (c *Client) UpdateWorkspace(ctx context.Context, workspaceID, name string) (*oapi.LivekitPublicapiWorkspacesV1Workspace, error) {
	resp, err := c.gen.WorkspaceServiceUpdateWorkspaceWithResponse(ctx, workspaceID, oapi.WorkspaceServiceUpdateWorkspaceJSONRequestBody{
		Name: ptr(name),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Workspace, nil
}

// DeleteWorkspace deletes a workspace by id.
func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	resp, err := c.gen.WorkspaceServiceDeleteWorkspaceWithResponse(ctx, workspaceID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListWorkspaceProjects returns the projects within a workspace.
func (c *Client) ListWorkspaceProjects(ctx context.Context, workspaceID string) ([]oapi.LivekitPublicapiProjectsV1Project, error) {
	resp, err := c.gen.WorkspaceServiceListProjectsWithResponse(ctx, workspaceID, &oapi.WorkspaceServiceListProjectsParams{
		PagePageSize: ptr(int32(100)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetWorkspaceProject returns a project within a workspace.
func (c *Client) GetWorkspaceProject(ctx context.Context, workspaceID, projectID string) (*oapi.LivekitPublicapiProjectsV1Project, error) {
	resp, err := c.gen.WorkspaceServiceGetProjectWithResponse(ctx, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Project, nil
}

// CreateWorkspaceProject creates a project within a workspace.
func (c *Client) CreateWorkspaceProject(ctx context.Context, workspaceID, name string) (*oapi.LivekitPublicapiProjectsV1Project, error) {
	resp, err := c.gen.WorkspaceServiceCreateProjectWithResponse(ctx, workspaceID, oapi.WorkspaceServiceCreateProjectJSONRequestBody{
		Name: ptr(name),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Project, nil
}

// UpdateWorkspaceProject renames a project within a workspace.
func (c *Client) UpdateWorkspaceProject(ctx context.Context, workspaceID, projectID, name string) (*oapi.LivekitPublicapiProjectsV1Project, error) {
	resp, err := c.gen.WorkspaceServiceUpdateProjectWithResponse(ctx, workspaceID, projectID, oapi.WorkspaceServiceUpdateProjectJSONRequestBody{
		Name: ptr(name),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Project, nil
}

// DeleteWorkspaceProject deletes a project within a workspace.
func (c *Client) DeleteWorkspaceProject(ctx context.Context, workspaceID, projectID string) error {
	resp, err := c.gen.WorkspaceServiceDeleteProjectWithResponse(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListWorkspaceMembers returns the members of a workspace.
func (c *Client) ListWorkspaceMembers(ctx context.Context, workspaceID string) ([]oapi.LivekitPublicapiWorkspacesV1WorkspaceMember, error) {
	resp, err := c.gen.WorkspaceServiceListMembersWithResponse(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetWorkspaceMember returns a single workspace member by user id.
func (c *Client) GetWorkspaceMember(ctx context.Context, workspaceID, userID string) (*oapi.LivekitPublicapiWorkspacesV1WorkspaceMember, error) {
	resp, err := c.gen.WorkspaceServiceGetMemberWithResponse(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Member, nil
}

// UpdateWorkspaceMember changes a workspace member's role.
func (c *Client) UpdateWorkspaceMember(ctx context.Context, workspaceID, userID string, role Role) (*oapi.LivekitPublicapiWorkspacesV1WorkspaceMember, error) {
	resp, err := c.gen.WorkspaceServiceUpdateMemberWithResponse(ctx, workspaceID, userID, oapi.WorkspaceServiceUpdateMemberJSONRequestBody{
		Role: ptr(int32(role)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Member, nil
}

// DeleteWorkspaceMember removes a member from a workspace.
func (c *Client) DeleteWorkspaceMember(ctx context.Context, workspaceID, userID string) error {
	resp, err := c.gen.WorkspaceServiceDeleteMemberWithResponse(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListWorkspaceInvites returns the pending invites for a workspace.
func (c *Client) ListWorkspaceInvites(ctx context.Context, workspaceID string) ([]oapi.LivekitPublicapiWorkspacesV1WorkspaceInvite, error) {
	resp, err := c.gen.WorkspaceServiceListInvitesWithResponse(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetWorkspaceInvite returns a single workspace invite by its token.
func (c *Client) GetWorkspaceInvite(ctx context.Context, inviteToken string) (*oapi.LivekitPublicapiWorkspacesV1WorkspaceInvite, error) {
	resp, err := c.gen.WorkspaceServiceGetInviteWithResponse(ctx, inviteToken)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Invite, nil
}

// CreateWorkspaceInvite invites an email address to a workspace at the given
// role. The response carries the generated invite token.
func (c *Client) CreateWorkspaceInvite(ctx context.Context, workspaceID, email string, role Role) (*oapi.LivekitPublicapiWorkspacesV1CreateInviteResponse, error) {
	resp, err := c.gen.WorkspaceServiceCreateInviteWithResponse(ctx, workspaceID, oapi.WorkspaceServiceCreateInviteJSONRequestBody{
		Email: ptr(email),
		Role:  ptr(int32(role)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}

// DeleteWorkspaceInvite revokes a pending workspace invite (keyed by email).
func (c *Client) DeleteWorkspaceInvite(ctx context.Context, workspaceID, email string) error {
	resp, err := c.gen.WorkspaceServiceDeleteInviteWithResponse(ctx, workspaceID, email)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// AnswerWorkspaceInvite accepts or declines a workspace invite by token. On
// accept the response carries the resulting membership (nil otherwise).
func (c *Client) AnswerWorkspaceInvite(ctx context.Context, inviteToken string, accept bool) (*oapi.LivekitPublicapiWorkspacesV1WorkspaceMember, error) {
	resp, err := c.gen.WorkspaceServiceAnswerInviteWithResponse(ctx, inviteToken, oapi.WorkspaceServiceAnswerInviteJSONRequestBody{
		Accept: ptr(accept),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Member, nil
}
