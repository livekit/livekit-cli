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

// ListProjectSessions returns one page of a project's analytics sessions. limit
// caps the page size (0 lets the server choose its default) and cursor requests a
// specific page (empty starts from the beginning). The operation is
// cursor-paginated; the returned nextCursor is non-empty when more pages remain
// (pass it back as cursor to fetch the next page).
func (c *Client) ListProjectSessions(ctx context.Context, projectID string, limit int32, cursor string) (sessions []oapi.LivekitPublicapiAnalyticsV1Session, nextCursor string, err error) {
	params := &oapi.AnalyticsServiceListProjectSessionsParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	resp, err := c.gen.AnalyticsServiceListProjectSessionsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	if pi := resp.JSON200.PageInfo; pi != nil && pi.NextCursor != nil {
		nextCursor = *pi.NextCursor
	}
	return items(resp.JSON200.Items), nextCursor, nil
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
	return requirePayload(resp.JSON200.Session, "session")
}
