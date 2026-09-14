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

// ListProjectInvites returns the pending invites for a project.
func (c *Client) ListProjectInvites(ctx context.Context, projectID string) ([]oapi.LivekitPublicapiProjectsV1ProjectInvite, error) {
	resp, err := c.gen.ProjectServiceListInvitesWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), nil
}

// GetProjectInvite returns a single invite by its token.
func (c *Client) GetProjectInvite(ctx context.Context, inviteToken string) (*oapi.LivekitPublicapiProjectsV1ProjectInvite, error) {
	resp, err := c.gen.ProjectServiceGetInviteWithResponse(ctx, inviteToken)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.Invite, "invite")
}

// InviteProjectMember invites an email address to a project at the given role.
// The response carries the generated invite token (the server does not echo the
// email/role back).
func (c *Client) InviteProjectMember(ctx context.Context, projectID, email string, role Role) (*oapi.LivekitPublicapiProjectsV1InviteMemberResponse, error) {
	resp, err := c.gen.ProjectServiceInviteMemberWithResponse(ctx, projectID, oapi.ProjectServiceInviteMemberJSONRequestBody{
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

// UpdateProjectInvite changes the role of a pending invite (keyed by email).
func (c *Client) UpdateProjectInvite(ctx context.Context, projectID, email string, role Role) (*oapi.LivekitPublicapiProjectsV1ProjectInvite, error) {
	resp, err := c.gen.ProjectServiceUpdateInviteWithResponse(ctx, projectID, email, oapi.ProjectServiceUpdateInviteJSONRequestBody{
		Role: ptr(int32(role)),
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return requirePayload(resp.JSON200.Invite, "invite")
}

// DeleteProjectInvite revokes a pending invite (keyed by email).
func (c *Client) DeleteProjectInvite(ctx context.Context, projectID, email string) error {
	resp, err := c.gen.ProjectServiceDeleteInviteWithResponse(ctx, projectID, email)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// AnswerProjectInvitation accepts or declines a project invite by token. On
// accept the response carries the resulting membership (nil otherwise).
func (c *Client) AnswerProjectInvitation(ctx context.Context, inviteToken string, accept bool) (*oapi.LivekitPublicapiProjectsV1ProjectMember, error) {
	resp, err := c.gen.ProjectServiceAnswerInvitationWithResponse(ctx, inviteToken, oapi.ProjectServiceAnswerInvitationJSONRequestBody{
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
