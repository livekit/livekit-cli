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
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/livekit/protocol/auth"
)

// CreateToken mints a LiveKit access token for a project carrying grants,
// valid for ttl (rounded down to whole seconds; 0 means the server default).
// The token is signed server-side, so no API secret is needed locally.
func (c *Client) CreateToken(ctx context.Context, projectID string, grants *auth.ClaimGrants, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	// Grants are a google.protobuf.Struct on the wire, which the generated
	// client models as an untyped union; send the access-token JSON as-is.
	body, err := json.Marshal(struct {
		Grants     *auth.ClaimGrants `json:"grants"`
		TTLSeconds uint32            `json:"ttlSeconds,omitempty"`
	}{grants, uint32(ttl / time.Second)})
	if err != nil {
		return "", time.Time{}, err
	}
	resp, err := c.gen.ProjectServiceCreateTokenWithBodyWithResponse(ctx, projectID, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.JSON200 == nil {
		return "", time.Time{}, responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.Token == nil || *resp.JSON200.Token == "" {
		_, err := requirePayload[string](nil, "token")
		return "", time.Time{}, err
	}
	if resp.JSON200.ExpiresAt != nil {
		expiresAt = *resp.JSON200.ExpiresAt
	} else {
		expiresAt = time.Now().Add(ttl)
	}
	return *resp.JSON200.Token, expiresAt, nil
}
