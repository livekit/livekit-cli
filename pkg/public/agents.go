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
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// Agent is a cloud agent as AgentService returns it. Its Info is LiveKit's
// own livekit.AgentInfo, which the API sends as protojson, so it decodes as
// TelephonyService's messages do (see telephony.go): lk's agent commands
// already print that type, and the round trip keeps every field.
type Agent struct {
	// Info is the agent, with a deployment for each region where it runs.
	// The API leaves out a version, agent name or deploy time that LiveKit
	// doesn't know, and the secrets.
	Info *livekit.AgentInfo
	// Status is the deployment status of the agent.
	Status oapi.LivekitPublicapiAgentsV1DeploymentStatus
	// AgentBuilderID is the ID of the Agent Builder agent that runs as this
	// agent, or "" for an agent deployed from code.
	AgentBuilderID string
}

// agentJSON is Agent as the API sends it.
type agentJSON struct {
	Info           json.RawMessage                               `json:"info"`
	Status         oapi.LivekitPublicapiAgentsV1DeploymentStatus `json:"status"`
	AgentBuilderID string                                        `json:"agentBuilderId"`
}

func (a agentJSON) decode() (*Agent, error) {
	if a.Info == nil {
		return nil, errors.New("unexpected response from server: missing info")
	}
	info := new(livekit.AgentInfo)
	if err := telephonyUnmarshal.Unmarshal(a.Info, info); err != nil {
		return nil, err
	}
	return &Agent{Info: info, Status: a.Status, AgentBuilderID: a.AgentBuilderID}, nil
}

// ListAgents returns the project's cloud agents, in ID order. The list is
// always one page.
func (c *Client) ListAgents(ctx context.Context, projectID string) ([]*Agent, error) {
	resp, err := c.gen.AgentServiceListAgentsWithResponse(ctx, projectID, &oapi.AgentServiceListAgentsParams{})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var env struct {
		Items    []agentJSON `json:"items"`
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
	out := make([]*Agent, 0, len(env.Items))
	for _, item := range env.Items {
		agent, err := item.decode()
		if err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, nil
}

// GetAgent returns the project's cloud agent agentID. An agent the project
// doesn't have is NotFound "agent not found" (see IsAgentNotFound).
func (c *Client) GetAgent(ctx context.Context, projectID, agentID string) (*Agent, error) {
	resp, err := c.gen.AgentServiceGetAgentWithResponse(ctx, projectID, agentID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var env struct {
		Agent *agentJSON `json:"agent"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	if env.Agent == nil {
		return nil, errors.New("unexpected response from server: missing agent")
	}
	return env.Agent.decode()
}

// agentNotFound is the message of AgentService's NotFound for an agent the
// project doesn't have.
const agentNotFound = "agent not found"

// IsAgentNotFound reports whether err is AgentService's NotFound for an
// agent, not for the project.
func IsAgentNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Message == agentNotFound
}

// ListAgentVersions returns the versions of the project's cloud agent
// agentID, newest first. The list is always one page.
func (c *Client) ListAgentVersions(ctx context.Context, projectID, agentID string) ([]*livekit.AgentVersion, error) {
	resp, err := c.gen.AgentServiceListAgentVersionsWithResponse(ctx, projectID, agentID, &oapi.AgentServiceListAgentVersionsParams{})
	if err != nil {
		return nil, err
	}
	return decodeOnePage[livekit.AgentVersion](resp.StatusCode(), resp.Body)
}

// ListAgentSecrets returns the secrets of the project's cloud agent agentID,
// in name order, without their values. The list is always one page.
func (c *Client) ListAgentSecrets(ctx context.Context, projectID, agentID string) ([]*livekit.AgentSecret, error) {
	resp, err := c.gen.AgentServiceListAgentSecretsWithResponse(ctx, projectID, agentID, &oapi.AgentServiceListAgentSecretsParams{})
	if err != nil {
		return nil, err
	}
	return decodeOnePage[livekit.AgentSecret](resp.StatusCode(), resp.Body)
}

// BuildLog is the build log of one version of a cloud agent.
type BuildLog struct {
	// Text is the log as plain text, or its end, from the start of a line,
	// when Truncated.
	Text string
	// Truncated is true when the log is longer than the API returns.
	Truncated bool
}

// GetAgentBuildLog returns the build log of version of the project's cloud
// agent agentID. The log exists once the build ends: before that, and for a
// version without one, it is NotFound "build log not found".
func (c *Client) GetAgentBuildLog(ctx context.Context, projectID, agentID, version string) (*BuildLog, error) {
	resp, err := c.gen.AgentServiceGetAgentBuildLogWithResponse(ctx, projectID, agentID, version)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200 == nil {
		return nil, errors.New("unexpected response from server: not JSON")
	}
	log := &BuildLog{}
	if resp.JSON200.Text != nil {
		log.Text = *resp.JSON200.Text
	}
	if resp.JSON200.Truncated != nil {
		log.Truncated = *resp.JSON200.Truncated
	}
	return log, nil
}

// GetAgentClientSettings returns the settings that lk reads before it
// creates or deploys a cloud agent in the project, such as the regions where
// the project can deploy one.
func (c *Client) GetAgentClientSettings(ctx context.Context, projectID string) (*livekit.ClientSettingsResponse, error) {
	resp, err := c.gen.AgentServiceGetAgentClientSettingsWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	// The response is {"params": [livekit.SettingsParam]}, the shape of
	// livekit.ClientSettingsResponse.
	settings := new(livekit.ClientSettingsResponse)
	if err := telephonyUnmarshal.Unmarshal(resp.Body, settings); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	return settings, nil
}

// RestartAgent restarts the project's cloud agent agentID. It keeps its
// version and its secrets.
func (c *Client) RestartAgent(ctx context.Context, projectID, agentID string) error {
	resp, err := c.gen.AgentServiceRestartAgentWithResponse(ctx, projectID, agentID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// RollbackAgent points the production deployment of the project's cloud
// agent agentID at version, or at the version before the current one when
// version is "". A version the agent doesn't have is NotFound, and one whose
// build didn't succeed is FailedPrecondition.
func (c *Client) RollbackAgent(ctx context.Context, projectID, agentID, version string) error {
	body := oapi.AgentServiceRollbackAgentJSONRequestBody{}
	if version != "" {
		body.Version = &version
	}
	resp, err := c.gen.AgentServiceRollbackAgentWithResponse(ctx, projectID, agentID, body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// PromoteAgent points the production deployment of the project's cloud
// agent agentID at the version that deployment runs, and returns that
// version.
func (c *Client) PromoteAgent(ctx context.Context, projectID, agentID, deployment string) (string, error) {
	resp, err := c.gen.AgentServicePromoteAgentWithResponse(ctx, projectID, agentID,
		oapi.AgentServicePromoteAgentJSONRequestBody{Deployment: &deployment})
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return "", responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200 == nil || resp.JSON200.Version == nil {
		return "", errors.New("unexpected response from server: missing version")
	}
	return *resp.JSON200.Version, nil
}

// DeleteAgent deletes the project's cloud agent agentID, or only its
// deployment when deployment is set.
func (c *Client) DeleteAgent(ctx context.Context, projectID, agentID, deployment string) error {
	params := &oapi.AgentServiceDeleteAgentParams{}
	if deployment != "" {
		params.Deployment = &deployment
	}
	resp, err := c.gen.AgentServiceDeleteAgentWithResponse(ctx, projectID, agentID, params)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// UpdateAgentSecrets sets the name, value and kind of each secret in set,
// and removes the secrets named in remove, on the project's cloud agent
// agentID. The agent's other secrets keep their values. A request the API
// finds invalid is InvalidArgument, with a FieldViolation for each problem.
func (c *Client) UpdateAgentSecrets(ctx context.Context, projectID, agentID string, set []*livekit.AgentSecret, remove []string) error {
	body := oapi.AgentServiceUpdateAgentSecretsJSONRequestBody{Set: secretValues(set)}
	if len(remove) > 0 {
		body.Remove = &remove
	}
	resp, err := c.gen.AgentServiceUpdateAgentSecretsWithResponse(ctx, projectID, agentID, body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// secretValues returns secrets as the API takes them, or nil for none.
func secretValues(secrets []*livekit.AgentSecret) *[]oapi.LivekitPublicapiAgentsV1AgentSecretValue {
	if len(secrets) == 0 {
		return nil
	}
	values := make([]oapi.LivekitPublicapiAgentsV1AgentSecretValue, len(secrets))
	for i, secret := range secrets {
		kind := oapi.LivekitAgentSecretKind(secret.GetKind().String())
		values[i] = oapi.LivekitPublicapiAgentsV1AgentSecretValue{Name: &secret.Name, Value: &secret.Value, Kind: &kind}
	}
	return &values
}

// CreateAgent creates a cloud agent in the project, in region, with secrets
// and nothing deployed, and returns its ID. region must be one of the
// regions that GetAgentClientSettings lists in available_regions. To deploy
// the agent's source, call DeployAgent.
func (c *Client) CreateAgent(ctx context.Context, projectID, region string, secrets []*livekit.AgentSecret) (string, error) {
	resp, err := c.gen.AgentServiceCreateAgentWithResponse(ctx, projectID,
		oapi.AgentServiceCreateAgentJSONRequestBody{Region: &region, Secrets: secretValues(secrets)})
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return "", responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200 == nil || resp.JSON200.AgentId == nil || *resp.JSON200.AgentId == "" {
		return "", errors.New("unexpected response from server: missing agent ID")
	}
	return *resp.JSON200.AgentId, nil
}

// SourceDeploy is a deploy that DeployAgent started: where to upload the
// source, and the tag of the version that its build makes.
type SourceDeploy struct {
	// Tag is the tag of the version that the build makes, which
	// StartAgentBuild takes.
	Tag string
	// UploadURL and UploadFields are a presigned POST: send the source as a
	// gzipped tarball in a multipart form, first each of UploadFields, then
	// the tarball in a field named "file".
	UploadURL    string
	UploadFields map[string]string
}

// DeployAgent starts a deploy of new source to deployment of the project's
// cloud agent agentID, or to production when deployment is "", and sets
// secrets on the agent. Upload the source as the returned SourceDeploy
// says, then call StartAgentBuild with its Tag.
func (c *Client) DeployAgent(ctx context.Context, projectID, agentID, deployment string, secrets []*livekit.AgentSecret) (*SourceDeploy, error) {
	body := oapi.AgentServiceDeployAgentJSONRequestBody{Secrets: secretValues(secrets)}
	if deployment != "" {
		body.Deployment = &deployment
	}
	resp, err := c.gen.AgentServiceDeployAgentWithResponse(ctx, projectID, agentID, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	r := resp.JSON200
	if r == nil || r.Tag == nil || *r.Tag == "" || r.Upload == nil || r.Upload.Url == nil || *r.Upload.Url == "" {
		return nil, errors.New("unexpected response from server: missing tag or upload")
	}
	d := &SourceDeploy{Tag: *r.Tag, UploadURL: *r.Upload.Url, UploadFields: map[string]string{}}
	if r.Upload.Fields != nil {
		d.UploadFields = *r.Upload.Fields
	}
	return d, nil
}

// StartAgentBuild builds the source uploaded for the deploy that
// DeployAgent started, as the version tag, and deploys the version to
// deployment when the build succeeds. It returns once the build is queued;
// the build continues. Too many builds in a short time are
// ResourceExhausted, and a build that doesn't start is FailedPrecondition.
func (c *Client) StartAgentBuild(ctx context.Context, projectID, agentID, deployment, tag string, attributes map[string]string) error {
	body := oapi.AgentServiceStartAgentBuildJSONRequestBody{Tag: &tag}
	if deployment != "" {
		body.Deployment = &deployment
	}
	if len(attributes) > 0 {
		body.Attributes = &attributes
	}
	resp, err := c.gen.AgentServiceStartAgentBuildWithResponse(ctx, projectID, agentID, body)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// FieldViolation is one problem with a field of a request that the API
// refused as invalid, such as {"set[0].name", "is required"}.
type FieldViolation struct {
	Field       string `json:"field"`
	Description string `json:"description"`
}

// FieldViolations returns the field violations of err, the API's
// InvalidArgument with a google.rpc.BadRequest detail, or nil.
func FieldViolations(err error) []FieldViolation {
	var detail struct {
		FieldViolations []FieldViolation `json:"fieldViolations"`
	}
	if !errorDetail(err, "google.rpc.BadRequest", &detail) {
		return nil
	}
	return detail.FieldViolations
}

// agentBuilderAgent starts the message of AgentService's FailedPrecondition
// for a write that an Agent Builder agent refuses.
const agentBuilderAgent = "agent is an Agent Builder agent"

// IsAgentBuilderAgent reports whether err is AgentService's refusal of a
// rollback, promote or delete of an Agent Builder agent, which Agent Builder
// deploys and deletes itself.
func IsAgentBuilderAgent(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && strings.HasPrefix(apiErr.Message, agentBuilderAgent)
}
