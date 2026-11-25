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
	"fmt"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// CloudAgentService's data shapes are LiveKit's own messages (livekit.AgentInfo,
// livekit.AgentVersion, livekit.AgentSecret, livekit.PrivateLink, ...), which
// the API imports and sends as protojson. Like the telephony methods, these
// take and return the livekit types that lk's agent commands already build and
// print, and the generated client still makes each request, so its paths and
// query parameters come from the spec.

// CloudAgent is a cloud agent as the API returns it: LiveKit's own AgentInfo,
// the deployment status, and the ID of the Agent Builder agent that owns it.
// The API empties the values that LiveKit does not know (the agent name, the
// version and the deploy time), so Info has no placeholders.
type CloudAgent struct {
	Info *livekit.AgentInfo
	// Status is the CLOUD_AGENT_STATUS_ enum name, such as
	// CLOUD_AGENT_STATUS_RUNNING.
	Status         string
	AgentBuilderID string
}

// CloudAgentPrivateLink is a private link with its health. Status is nil when
// the API could not read the health.
type CloudAgentPrivateLink struct {
	Link   *livekit.PrivateLink
	Status *livekit.PrivateLinkStatus
}

// CloudAgentDeployment is the start of a deployment of new source: the tag of
// the version that the build makes, and the presigned POST to upload the
// source to.
type CloudAgentDeployment struct {
	Tag          string
	UploadURL    string
	UploadFields map[string]string
}

// CloudAgentBuildLog is the text of the build log of a version.
type CloudAgentBuildLog struct {
	Text      string
	Truncated bool
}

// agentBody returns the JSON object whose values are fields. A livekit message
// or a slice of them is written as protojson, and any other value as JSON.
func agentBody(fields map[string]any) (*bytes.Reader, error) {
	obj := make(map[string]json.RawMessage, len(fields))
	for name, v := range fields {
		raw, err := agentJSON(v)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
		obj[name] = raw
	}
	body, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

func agentJSON(v any) (json.RawMessage, error) {
	switch v := v.(type) {
	case proto.Message:
		return protojson.Marshal(v)
	case []*livekit.AgentSecret:
		items := make([]json.RawMessage, len(v))
		for i, m := range v {
			raw, err := protojson.Marshal(m)
			if err != nil {
				return nil, err
			}
			items[i] = raw
		}
		return json.Marshal(items)
	}
	return json.Marshal(v)
}

// decodeJSON decodes the body of a 200 response into v, or returns the error
// of any other status.
func decodeJSON(status int, body []byte, v any) error {
	if status != http.StatusOK {
		return responseError(status, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("unexpected response from server: %w", err)
	}
	return nil
}

// decodeMessages reads each raw item into a new message.
func decodeMessages[T proto.Message](raw []json.RawMessage, newMessage func() T) ([]T, error) {
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		m := newMessage()
		if err := telephonyUnmarshal.Unmarshal(r, m); err != nil {
			return nil, fmt.Errorf("unexpected response from server: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

// ListCloudAgents returns the project's cloud agents in ID order, or only
// those in agentIDs when it isn't empty.
func (c *Client) ListCloudAgents(ctx context.Context, projectID string, agentIDs []string) ([]*CloudAgent, error) {
	params := &oapi.CloudAgentServiceListCloudAgentsParams{}
	if len(agentIDs) > 0 {
		params.AgentIds = &agentIDs
	}
	resp, err := c.gen.CloudAgentServiceListCloudAgentsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []struct {
			Info           json.RawMessage `json:"info"`
			Status         string          `json:"status"`
			AgentBuilderID string          `json:"agentBuilderId"`
		} `json:"items"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &body); err != nil {
		return nil, err
	}
	out := make([]*CloudAgent, len(body.Items))
	for i, item := range body.Items {
		info := &livekit.AgentInfo{}
		if err := telephonyUnmarshal.Unmarshal(item.Info, info); err != nil {
			return nil, fmt.Errorf("unexpected response from server: %w", err)
		}
		out[i] = &CloudAgent{Info: info, Status: item.Status, AgentBuilderID: item.AgentBuilderID}
	}
	return out, nil
}

// ListCloudAgentVersions returns the versions of a cloud agent, newest first.
func (c *Client) ListCloudAgentVersions(ctx context.Context, projectID, agentID string) ([]*livekit.AgentVersion, error) {
	resp, err := c.gen.CloudAgentServiceListCloudAgentVersionsWithResponse(ctx, projectID, agentID, &oapi.CloudAgentServiceListCloudAgentVersionsParams{})
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &body); err != nil {
		return nil, err
	}
	return decodeMessages(body.Items, func() *livekit.AgentVersion { return &livekit.AgentVersion{} })
}

// ListCloudAgentSecrets returns the secrets of a cloud agent, without values.
func (c *Client) ListCloudAgentSecrets(ctx context.Context, projectID, agentID string) ([]*livekit.AgentSecret, error) {
	resp, err := c.gen.CloudAgentServiceListCloudAgentSecretsWithResponse(ctx, projectID, agentID, &oapi.CloudAgentServiceListCloudAgentSecretsParams{})
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &body); err != nil {
		return nil, err
	}
	return decodeMessages(body.Items, func() *livekit.AgentSecret { return &livekit.AgentSecret{} })
}

// GetCloudAgentSettings returns the settings that a client reads before it
// creates or deploys an agent, as the name and value pairs that lk reads.
func (c *Client) GetCloudAgentSettings(ctx context.Context, projectID string) ([]*livekit.SettingsParam, error) {
	resp, err := c.gen.CloudAgentServiceGetCloudAgentSettingsWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var body struct {
		AvailableRegions        []string          `json:"availableRegions"`
		ProjectDataRegion       string            `json:"projectDataRegion"`
		ResidencyWarningRegions []string          `json:"residencyWarningRegions"`
		Settings                map[string]string `json:"settings"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &body); err != nil {
		return nil, err
	}
	params := []*livekit.SettingsParam{
		{Name: "available_regions", Value: join(body.AvailableRegions)},
		{Name: "project_data_region", Value: body.ProjectDataRegion},
		{Name: "residency_warning_regions", Value: join(body.ResidencyWarningRegions)},
	}
	for name, value := range body.Settings {
		params = append(params, &livekit.SettingsParam{Name: name, Value: value})
	}
	return params, nil
}

func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item
	}
	return out
}

// CreateCloudAgent registers a cloud agent with nothing deployed in region,
// and returns its ID.
func (c *Client) CreateCloudAgent(ctx context.Context, projectID, region string, secrets []*livekit.AgentSecret) (string, error) {
	body, err := agentBody(map[string]any{"region": region, "secrets": secrets})
	if err != nil {
		return "", err
	}
	resp, err := c.gen.CloudAgentServiceCreateCloudAgentWithBodyWithResponse(ctx, projectID, "application/json", body)
	if err != nil {
		return "", err
	}
	var out struct {
		AgentID string `json:"agentId"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &out); err != nil {
		return "", err
	}
	return out.AgentID, nil
}

// DeleteCloudAgent deletes a cloud agent, or only its named deployment.
func (c *Client) DeleteCloudAgent(ctx context.Context, projectID, agentID, deployment string) error {
	params := &oapi.CloudAgentServiceDeleteCloudAgentParams{}
	if deployment != "" {
		params.Deployment = &deployment
	}
	resp, err := c.gen.CloudAgentServiceDeleteCloudAgentWithResponse(ctx, projectID, agentID, params)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// DeployCloudAgent starts a deployment of new source to the cloud agent's
// deployment (production when empty).
func (c *Client) DeployCloudAgent(ctx context.Context, projectID, agentID, deployment string, secrets []*livekit.AgentSecret) (*CloudAgentDeployment, error) {
	body, err := agentBody(map[string]any{"deployment": deployment, "secrets": secrets})
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.CloudAgentServiceDeployCloudAgentWithBodyWithResponse(ctx, projectID, agentID, "application/json", body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Tag    string `json:"tag"`
		Upload struct {
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"upload"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &out); err != nil {
		return nil, err
	}
	return &CloudAgentDeployment{Tag: out.Tag, UploadURL: out.Upload.URL, UploadFields: out.Upload.Fields}, nil
}

// BuildCloudAgent starts the build of the source uploaded for tag, and returns
// once the build is queued.
func (c *Client) BuildCloudAgent(ctx context.Context, projectID, agentID, tag, deployment string, attributes map[string]string) error {
	body, err := agentBody(map[string]any{"tag": tag, "deployment": deployment, "attributes": attributes})
	if err != nil {
		return err
	}
	resp, err := c.gen.CloudAgentServiceBuildCloudAgentWithBodyWithResponse(ctx, projectID, agentID, "application/json", body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// PromoteCloudAgent makes the version that deployment runs the production
// version.
func (c *Client) PromoteCloudAgent(ctx context.Context, projectID, agentID, deployment string) error {
	body, err := agentBody(map[string]any{"deployment": deployment})
	if err != nil {
		return err
	}
	resp, err := c.gen.CloudAgentServicePromoteCloudAgentWithBodyWithResponse(ctx, projectID, agentID, "application/json", body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// RestartCloudAgent restarts the workers of a cloud agent.
func (c *Client) RestartCloudAgent(ctx context.Context, projectID, agentID string) error {
	resp, err := c.gen.CloudAgentServiceRestartCloudAgentWithResponse(ctx, projectID, agentID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// RollbackCloudAgent makes version the production version.
func (c *Client) RollbackCloudAgent(ctx context.Context, projectID, agentID, version string) error {
	body, err := agentBody(map[string]any{"version": version})
	if err != nil {
		return err
	}
	resp, err := c.gen.CloudAgentServiceRollbackCloudAgentWithBodyWithResponse(ctx, projectID, agentID, "application/json", body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// UpdateCloudAgentSecrets sets the secrets in set and deletes the secrets named
// in remove. A secret that it does not name keeps its value.
func (c *Client) UpdateCloudAgentSecrets(ctx context.Context, projectID, agentID string, set []*livekit.AgentSecret, remove []string) error {
	body, err := agentBody(map[string]any{"set": set, "remove": remove})
	if err != nil {
		return err
	}
	resp, err := c.gen.CloudAgentServiceUpdateCloudAgentSecretsWithBodyWithResponse(ctx, projectID, agentID, "application/json", body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// GetCloudAgentBuildLog returns the build log of a version of a cloud agent
// (the deployed version when version is empty).
func (c *Client) GetCloudAgentBuildLog(ctx context.Context, projectID, agentID, deployment, version string) (*CloudAgentBuildLog, error) {
	params := &oapi.CloudAgentServiceGetCloudAgentBuildLogsParams{}
	if deployment != "" {
		params.Deployment = &deployment
	}
	if version != "" {
		params.Version = &version
	}
	resp, err := c.gen.CloudAgentServiceGetCloudAgentBuildLogsWithResponse(ctx, projectID, agentID, params)
	if err != nil {
		return nil, err
	}
	var out struct {
		Log       string `json:"log"`
		Truncated bool   `json:"truncated"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &out); err != nil {
		return nil, err
	}
	return &CloudAgentBuildLog{Text: out.Log, Truncated: out.Truncated}, nil
}

// ListCloudAgentPrivateLinks returns the project's private links, each with
// its health.
func (c *Client) ListCloudAgentPrivateLinks(ctx context.Context, projectID string) ([]*CloudAgentPrivateLink, error) {
	resp, err := c.gen.CloudAgentServiceListCloudAgentPrivateLinksWithResponse(ctx, projectID, &oapi.CloudAgentServiceListCloudAgentPrivateLinksParams{})
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []struct {
			Link   json.RawMessage `json:"link"`
			Status json.RawMessage `json:"status"`
		} `json:"items"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &body); err != nil {
		return nil, err
	}
	out := make([]*CloudAgentPrivateLink, len(body.Items))
	for i, item := range body.Items {
		link := &livekit.PrivateLink{}
		if err := telephonyUnmarshal.Unmarshal(item.Link, link); err != nil {
			return nil, fmt.Errorf("unexpected response from server: %w", err)
		}
		out[i] = &CloudAgentPrivateLink{Link: link}
		if len(item.Status) > 0 && string(item.Status) != "null" {
			status := &livekit.PrivateLinkStatus{}
			if err := telephonyUnmarshal.Unmarshal(item.Status, status); err != nil {
				return nil, fmt.Errorf("unexpected response from server: %w", err)
			}
			out[i].Status = status
		}
	}
	return out, nil
}

// CreateCloudAgentPrivateLink makes a private link.
func (c *Client) CreateCloudAgentPrivateLink(ctx context.Context, projectID string, req *livekit.CreatePrivateLinkRequest) (*livekit.PrivateLink, error) {
	fields := map[string]any{
		"name": req.GetName(), "region": req.GetRegion(), "port": req.GetPort(), "endpoint": req.GetEndpoint(),
	}
	if req.CloudRegion != nil {
		fields["cloudRegion"] = req.GetCloudRegion()
	}
	body, err := agentBody(fields)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.CloudAgentServiceCreateCloudAgentPrivateLinkWithBodyWithResponse(ctx, projectID, "application/json", body)
	if err != nil {
		return nil, err
	}
	var out struct {
		PrivateLink json.RawMessage `json:"privateLink"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &out); err != nil {
		return nil, err
	}
	link := &livekit.PrivateLink{}
	if err := telephonyUnmarshal.Unmarshal(out.PrivateLink, link); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	return link, nil
}

// DeleteCloudAgentPrivateLink deletes a private link.
func (c *Client) DeleteCloudAgentPrivateLink(ctx context.Context, projectID, privateLinkID string) error {
	resp, err := c.gen.CloudAgentServiceDeleteCloudAgentPrivateLinkWithResponse(ctx, projectID, privateLinkID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// GetCloudAgentPrivateLinkStatus returns the health of a private link.
func (c *Client) GetCloudAgentPrivateLinkStatus(ctx context.Context, projectID, privateLinkID string) (*livekit.PrivateLinkStatus, error) {
	resp, err := c.gen.CloudAgentServiceGetCloudAgentPrivateLinkStatusWithResponse(ctx, projectID, privateLinkID)
	if err != nil {
		return nil, err
	}
	var out struct {
		Status json.RawMessage `json:"status"`
	}
	if err := decodeJSON(resp.StatusCode(), resp.Body, &out); err != nil {
		return nil, err
	}
	status := &livekit.PrivateLinkStatus{}
	if len(out.Status) == 0 {
		return status, nil
	}
	if err := telephonyUnmarshal.Unmarshal(out.Status, status); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	return status, nil
}
