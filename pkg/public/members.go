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

// RemoveProjectMember removes a member from a project (from its whole
// workspace when the project isn't private), deletes the API keys they own
// there, and returns how many it deleted. confirmedNumKeys must be that count:
// call with 0, and if the member owns keys the error carries the count (see
// MemberRemovalNotConfirmed).
func (c *Client) RemoveProjectMember(ctx context.Context, projectID, userID string, confirmedNumKeys uint32) (uint32, error) {
	resp, err := c.gen.ProjectServiceRemoveMemberWithResponse(ctx, projectID, userID, &oapi.ProjectServiceRemoveMemberParams{
		ConfirmedNumKeys: confirmedNumKeysParam(confirmedNumKeys),
	})
	if err != nil {
		return 0, err
	}
	if resp.JSON200 == nil {
		return 0, responseError(resp.StatusCode(), resp.Body)
	}
	return numKeys(resp.JSON200.NumKeys), nil
}

// MemberRemoval is what removing a member would do to the API keys they own.
type MemberRemoval struct {
	// NumKeys is how many of the member's API keys the removal deletes. Pass
	// it as confirmedNumKeys to go ahead.
	NumKeys uint32 `json:"numKeys"`
	// LastKeyProjectNames are the projects the removal would leave without an
	// API key. Private ones the caller may not see are only counted, in
	// NumPrivateLastKeyProjects.
	LastKeyProjectNames       []string `json:"lastKeyProjectNames"`
	NumPrivateLastKeyProjects uint32   `json:"numPrivateLastKeyProjects"`
}

// LeavesProjectWithoutKey reports whether the removal would take a project's
// last API key. The Public API refuses such a removal even when the count is
// confirmed.
func (r *MemberRemoval) LeavesProjectWithoutKey() bool {
	return len(r.LastKeyProjectNames) > 0 || r.NumPrivateLastKeyProjects > 0
}

// MemberRemovalNotConfirmed reports whether err is the Public API's refusal of
// a member removal that didn't go ahead: the member owns API keys and the
// caller didn't confirm their number, or the removal would leave a project
// without an API key.
func MemberRemovalNotConfirmed(err error) (*MemberRemoval, bool) {
	var removal MemberRemoval
	if !errorDetail(err, "livekit.publicapi.common.v1.MemberRemovalNotConfirmed", &removal) {
		return nil, false
	}
	return &removal, true
}

// confirmedNumKeysParam leaves a count of 0 out of the request, which the API
// reads as 0. A server from before confirmedNumKeys answers the parameter with
// an unknown-field error, so leaving it out keeps removing a member who owns
// no keys working there.
func confirmedNumKeysParam(n uint32) *int {
	if n == 0 {
		return nil
	}
	return ptr(int(n))
}

// numKeys reads a member removal's numKeys, which the API leaves out when no
// key was deleted.
func numKeys(n *int) uint32 {
	if n == nil {
		return 0
	}
	return uint32(*n)
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
