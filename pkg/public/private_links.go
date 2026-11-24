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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// PrivateLink is a private link of a project, with its health. The API
// sends both as LiveKit's own messages, as protojson, so they decode as
// Agent's info does.
type PrivateLink struct {
	Link *livekit.PrivateLink
	// Status is the health of the link, or nil when LiveKit couldn't read
	// it: GetPrivateLinkStatus reads it again.
	Status *livekit.PrivateLinkStatus
}

// CreatePrivateLink creates a private link in the project and returns it.
// The API takes req's name, region, port, endpoint and cloud region.
func (c *Client) CreatePrivateLink(ctx context.Context, projectID string, req *livekit.CreatePrivateLinkRequest) (*livekit.PrivateLink, error) {
	port := int(req.GetPort())
	body := oapi.AgentServiceCreatePrivateLinkJSONRequestBody{
		Name:        &req.Name,
		Region:      &req.Region,
		Port:        &port,
		Endpoint:    &req.Endpoint,
		CloudRegion: req.CloudRegion,
	}
	resp, err := c.gen.AgentServiceCreatePrivateLinkWithResponse(ctx, projectID, body)
	if err != nil {
		return nil, err
	}
	link := new(livekit.PrivateLink)
	if err := decodeField(resp.StatusCode(), resp.Body, "privateLink", link); err != nil {
		return nil, err
	}
	return link, nil
}

// ListPrivateLinks returns the project's private links, in name order, each
// with its health. The list is always one page.
func (c *Client) ListPrivateLinks(ctx context.Context, projectID string) ([]PrivateLink, error) {
	resp, err := c.gen.AgentServiceListPrivateLinksWithResponse(ctx, projectID, &oapi.AgentServiceListPrivateLinksParams{})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var env struct {
		Items []struct {
			Link   json.RawMessage `json:"link"`
			Status json.RawMessage `json:"status"`
		} `json:"items"`
		PageInfo struct {
			HasMore bool `json:"hasMore"`
		} `json:"pageInfo"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	if env.PageInfo.HasMore {
		return nil, errors.New("unexpected response from server: the list has more than one page")
	}
	out := make([]PrivateLink, 0, len(env.Items))
	for _, item := range env.Items {
		if item.Link == nil {
			return nil, errors.New("unexpected response from server: missing link")
		}
		link := PrivateLink{Link: new(livekit.PrivateLink)}
		if err := telephonyUnmarshal.Unmarshal(item.Link, link.Link); err != nil {
			return nil, err
		}
		if item.Status != nil {
			link.Status = new(livekit.PrivateLinkStatus)
			if err := telephonyUnmarshal.Unmarshal(item.Status, link.Status); err != nil {
				return nil, err
			}
		}
		out = append(out, link)
	}
	return out, nil
}

// DeletePrivateLink deletes the project's private link id.
func (c *Client) DeletePrivateLink(ctx context.Context, projectID, id string) error {
	resp, err := c.gen.AgentServiceDeletePrivateLinkWithResponse(ctx, projectID, id)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// GetPrivateLinkStatus returns the health of the project's private link id.
func (c *Client) GetPrivateLinkStatus(ctx context.Context, projectID, id string) (*livekit.PrivateLinkStatus, error) {
	resp, err := c.gen.AgentServiceGetPrivateLinkStatusWithResponse(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	status := new(livekit.PrivateLinkStatus)
	if err := decodeField(resp.StatusCode(), resp.Body, "status", status); err != nil {
		return nil, err
	}
	return status, nil
}
