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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestListWebhooks(t *testing.T) {
	var path string
	srv := jsonServer(t, http.StatusOK, `{"webhooks":[{"id":"wh_1","url":"https://example.com/hook","filterParams":{"includeEvents":["room_started"]}}]}`, nil, &path)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	webhooks, err := c.ListWebhooks(context.Background(), "p_1")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/webhooks", path)
	require.Len(t, webhooks, 1)
	assert.Equal(t, "wh_1", *webhooks[0].Id)
	assert.Equal(t, []string{"room_started"}, *webhooks[0].FilterParams.IncludeEvents)
}

// TestUpdateWebhookBody checks that unset fields are omitted (left unchanged)
// and that an empty filter is still sent, since it clears the filter.
func TestUpdateWebhookBody(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webhook":{"id":"wh_1","name":"renamed"}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	name := "renamed"
	wh, err := c.UpdateWebhook(context.Background(), "p_1", "wh_1", oapi.ProjectServiceUpdateWebhookJSONRequestBody{
		Name:         &name,
		FilterParams: &oapi.LivekitFilterParams{},
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, method)
	assert.Equal(t, "/v0/projects/p_1/webhooks/wh_1", path)
	assert.JSONEq(t, `{"name":"renamed","filterParams":{}}`, body)
	assert.Equal(t, "renamed", *wh.Name)
}

func TestSendTestWebhookFailedDelivery(t *testing.T) {
	var path string
	srv := jsonServer(t, http.StatusOK, `{"error":"connection refused"}`, nil, &path)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	res, err := c.SendTestWebhook(context.Background(), "p_1", "wh_1", "room_started")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/webhooks/wh_1:test", path)
	assert.Nil(t, res.StatusCode)
	assert.Equal(t, "connection refused", *res.Error)
}

func TestDeleteWebhookError(t *testing.T) {
	srv := jsonServer(t, http.StatusForbidden, `{"message":"writer only"}`, nil, nil)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)

	err = c.DeleteWebhook(context.Background(), "p_1", "wh_1")
	assert.True(t, IsPermissionDenied(err))
}
