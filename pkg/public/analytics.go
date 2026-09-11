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

// ListProjectSessions returns the analytics sessions for a project.
//
// NOTE: returns only the first page; wire up PageInfo/cursor paging when a
// command needs the full set.
func (c *Client) ListProjectSessions(ctx context.Context, projectID string) ([]oapi.LivekitPublicapiAnalyticsV1Session, error) {
	resp, err := c.gen.AnalyticsServiceListProjectSessionsWithResponse(ctx, projectID, &oapi.AnalyticsServiceListProjectSessionsParams{
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

// GetSession returns a single analytics session by id.
func (c *Client) GetSession(ctx context.Context, projectID, sessionID string) (*oapi.LivekitPublicapiAnalyticsV1Session, error) {
	resp, err := c.gen.AnalyticsServiceGetSessionWithResponse(ctx, projectID, sessionID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Session, nil
}
