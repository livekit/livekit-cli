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

// ListWebhooks returns the webhook destinations of a project.
func (c *Client) ListWebhooks(ctx context.Context, projectID string) ([]oapi.LivekitPublicapiProjectsV1Webhook, error) {
	resp, err := c.gen.ProjectServiceListWebhooksWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Webhooks), nil
}

// CreateWebhook adds a webhook destination to a project. An unset filter
// delivers every event; an unset signing key makes the server pick (and store)
// the project's oldest API key.
func (c *Client) CreateWebhook(ctx context.Context, projectID string, body oapi.ProjectServiceCreateWebhookJSONRequestBody) (*oapi.LivekitPublicapiProjectsV1Webhook, error) {
	resp, err := c.gen.ProjectServiceCreateWebhookWithResponse(ctx, projectID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Webhook == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Webhook, nil
}

// UpdateWebhook changes the fields set in body and leaves the rest unchanged. A
// non-nil FilterParams replaces the whole filter; an empty one delivers every
// event.
func (c *Client) UpdateWebhook(ctx context.Context, projectID, webhookID string, body oapi.ProjectServiceUpdateWebhookJSONRequestBody) (*oapi.LivekitPublicapiProjectsV1Webhook, error) {
	resp, err := c.gen.ProjectServiceUpdateWebhookWithResponse(ctx, projectID, webhookID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Webhook == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Webhook, nil
}

// DeleteWebhook removes a webhook destination from a project.
func (c *Client) DeleteWebhook(ctx context.Context, projectID, webhookID string) error {
	resp, err := c.gen.ProjectServiceDeleteWebhookWithResponse(ctx, projectID, webhookID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// SendTestWebhook makes one signed delivery of event, with sample data, to a
// webhook. A failed delivery is reported in the result, not as an error.
func (c *Client) SendTestWebhook(ctx context.Context, projectID, webhookID, event string) (*oapi.LivekitPublicapiProjectsV1SendTestWebhookResponse, error) {
	resp, err := c.gen.ProjectServiceSendTestWebhookWithResponse(ctx, projectID, webhookID, oapi.ProjectServiceSendTestWebhookJSONRequestBody{
		Event: &event,
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}

// ListWebhookEvents returns the event names a project's webhooks can filter on
// and send test deliveries of.
func (c *Client) ListWebhookEvents(ctx context.Context, projectID string) ([]string, error) {
	resp, err := c.gen.ProjectServiceListWebhookEventsWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Events), nil
}
