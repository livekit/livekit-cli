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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/livekit/protocol/livekit"
)

const privateLinkJSON = `{"privateLinkId":"PL_1","name":"db","region":"us-east","port":5432,` +
	`"endpoint":"com.amazonaws.vpce.us-east-1.vpce-svc-1","connectionEndpoint":"db.pl.livekit.cloud","cloudRegion":"us-east-1"}`

func wantPrivateLink() *livekit.PrivateLink {
	return &livekit.PrivateLink{
		PrivateLinkId: "PL_1", Name: "db", Region: "us-east", Port: 5432,
		Endpoint: "com.amazonaws.vpce.us-east-1.vpce-svc-1", ConnectionEndpoint: "db.pl.livekit.cloud", CloudRegion: "us-east-1",
	}
}

// CreatePrivateLink sends the fields lk sets, the cloud region only when
// set, and returns the link.
func TestCreatePrivateLink(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"privateLink":`+privateLinkJSON+`}`)
	link, err := c.CreatePrivateLink(context.Background(), "p_1", &livekit.CreatePrivateLinkRequest{
		Name: "db", Region: "us-east", Port: 5432, Endpoint: "com.amazonaws.vpce.us-east-1.vpce-svc-1",
	})
	require.NoError(t, err)
	assert.True(t, proto.Equal(wantPrivateLink(), link), "got %v", link)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/private-links", got.path)
	assert.JSONEq(t, `{"name":"db","region":"us-east","port":5432,"endpoint":"com.amazonaws.vpce.us-east-1.vpce-svc-1"}`, got.body)

	c, got = telephonyServer(t, http.StatusOK, `{"privateLink":`+privateLinkJSON+`}`)
	_, err = c.CreatePrivateLink(context.Background(), "p_1", &livekit.CreatePrivateLinkRequest{
		Name: "db", Region: "us-east", Port: 5432, Endpoint: "/subscriptions/s/pls", CloudRegion: proto.String("eastus"),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"db","region":"us-east","port":5432,"endpoint":"/subscriptions/s/pls","cloudRegion":"eastus"}`, got.body)
}

// Each link comes with its health, or without it when LiveKit couldn't
// read it.
func TestListPrivateLinks(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"items":[`+
		`{"link":`+privateLinkJSON+`,"status":{"status":"PRIVATE_LINK_STATUS_HEALTHY","updatedAt":"2026-11-23T10:00:00Z","reason":"ok"}},`+
		`{"link":{"privateLinkId":"PL_2","name":"cache"}}],"pageInfo":{}}`)
	links, err := c.ListPrivateLinks(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/private-links", got.path)
	require.Len(t, links, 2)
	assert.True(t, proto.Equal(wantPrivateLink(), links[0].Link))
	assert.True(t, proto.Equal(&livekit.PrivateLinkStatus{
		Status:    livekit.PrivateLinkStatus_PRIVATE_LINK_STATUS_HEALTHY,
		UpdatedAt: timestamppb.New(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)),
		Reason:    "ok",
	}, links[0].Status))
	assert.Equal(t, "PL_2", links[1].Link.PrivateLinkId)
	assert.Nil(t, links[1].Status)

	c, _ = telephonyServer(t, http.StatusOK, `{"items":[],"pageInfo":{"hasMore":true,"nextCursor":"c"}}`)
	_, err = c.ListPrivateLinks(context.Background(), "p_1")
	require.EqualError(t, err, "unexpected response from server: the list has more than one page")

	c, _ = telephonyServer(t, http.StatusOK, `{"items":[{"status":{}}]}`)
	_, err = c.ListPrivateLinks(context.Background(), "p_1")
	require.EqualError(t, err, "unexpected response from server: missing link")
}

func TestDeletePrivateLink(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{}`)
	require.NoError(t, c.DeletePrivateLink(context.Background(), "p_1", "PL_1"))
	assert.Equal(t, http.MethodDelete, got.method)
	assert.Equal(t, "/v0/projects/p_1/private-links/PL_1", got.path)

	c, _ = telephonyServer(t, http.StatusNotFound, `{"code":5,"message":"private link not found"}`)
	require.EqualError(t, c.DeletePrivateLink(context.Background(), "p_1", "PL_9"), "private link not found")
}

func TestGetPrivateLinkStatus(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"status":{"status":"PRIVATE_LINK_STATUS_UNHEALTHY","reason":"no route"}}`)
	status, err := c.GetPrivateLinkStatus(context.Background(), "p_1", "PL_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v0/projects/p_1/private-links/PL_1/status", got.path)
	assert.True(t, proto.Equal(&livekit.PrivateLinkStatus{Status: livekit.PrivateLinkStatus_PRIVATE_LINK_STATUS_UNHEALTHY, Reason: "no route"}, status))
}
