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
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// HiddenHeaderValue is what the Public API sends in place of each header value
// of an Agent Builder agent configuration to a caller who may read the agent but not
// change it. The API refuses it as a header value on a write.
const HiddenHeaderValue = "********"

// AgentBuilderAgent is an Agent Builder agent as the Public API sent it, with its
// configuration verbatim. The generated configuration type drops properties
// this client doesn't know, so a configuration read through it and written
// back would lose them.
type AgentBuilderAgent struct {
	oapi.LivekitPublicapiAgentbuilderV1Agent
	Config json.RawMessage `json:"config,omitempty"`
}

// AgentBuilderAgentVersion is an Agent Builder agent version as the Public API sent it,
// with its configuration verbatim, as in AgentBuilderAgent.
type AgentBuilderAgentVersion struct {
	oapi.LivekitPublicapiAgentbuilderV1AgentVersion
	Config json.RawMessage `json:"config,omitempty"`
}

// agentBuilderAgentWrite is the body of an Agent Builder agent create or update. The
// configuration goes as the caller wrote it, for the reason in AgentBuilderAgent.
type agentBuilderAgentWrite struct {
	Name   *string         `json:"name,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// ListAgentBuilderAgents returns one page of a project's Agent Builder agents, without
// their configurations, and the cursor of the next page ("" on the last).
// A limit of 0 lets the server choose the page size.
func (c *Client) ListAgentBuilderAgents(ctx context.Context, projectID string, limit int32, cursor string) ([]oapi.LivekitPublicapiAgentbuilderV1Agent, string, error) {
	params := &oapi.AgentBuilderServiceListAgentsParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	resp, err := c.gen.AgentBuilderServiceListAgentsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}

// GetAgentBuilderAgent returns an Agent Builder agent with its configuration.
func (c *Client) GetAgentBuilderAgent(ctx context.Context, projectID, agentBuilderAgentID string) (*AgentBuilderAgent, error) {
	resp, err := c.gen.AgentBuilderServiceGetAgentWithResponse(ctx, projectID, agentBuilderAgentID)
	if err != nil {
		return nil, err
	}
	return decodeAgentBuilderAgent(resp.StatusCode(), resp.Body)
}

// CreateAgentBuilderAgent creates an Agent Builder agent named name from config, a JSON
// object. A nil config takes the default configuration.
func (c *Client) CreateAgentBuilderAgent(ctx context.Context, projectID, name string, config json.RawMessage) (*AgentBuilderAgent, error) {
	body, err := json.Marshal(agentBuilderAgentWrite{Name: &name, Config: config})
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.AgentBuilderServiceCreateAgentWithBodyWithResponse(ctx, projectID, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return decodeAgentBuilderAgent(resp.StatusCode(), resp.Body)
}

// UpdateAgentBuilderAgent replaces an Agent Builder agent's configuration with config, a
// JSON object, renames it to name, or both. A nil name or config leaves that
// unchanged.
func (c *Client) UpdateAgentBuilderAgent(ctx context.Context, projectID, agentBuilderAgentID string, name *string, config json.RawMessage) (*AgentBuilderAgent, error) {
	body, err := json.Marshal(agentBuilderAgentWrite{Name: name, Config: config})
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.AgentBuilderServiceUpdateAgentWithBodyWithResponse(ctx, projectID, agentBuilderAgentID, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return decodeAgentBuilderAgent(resp.StatusCode(), resp.Body)
}

// AgentBuilderAgentDeploy is the answer to an Agent Builder agent deploy: the version the
// deployment builds and its status, which the API sends, and the server's
// time of the answer.
type AgentBuilderAgentDeploy struct {
	oapi.LivekitPublicapiAgentbuilderV1AgentsDeployResponse
	// Date is the time in the response's Date header, to the second, or the
	// zero time without one. It lets a caller tell a deployment that started
	// after the deploy from an earlier one by server clocks alone.
	Date time.Time `json:"-"`
}

// DeployAgentBuilderAgent starts a deployment of an Agent Builder agent's version, or of
// its current configuration when versionID is "". It returns once the build
// starts.
func (c *Client) DeployAgentBuilderAgent(ctx context.Context, projectID, agentBuilderAgentID, versionID string) (*AgentBuilderAgentDeploy, error) {
	body := oapi.AgentBuilderServiceDeployAgentJSONRequestBody{}
	if versionID != "" {
		body.VersionId = &versionID
	}
	resp, err := c.gen.AgentBuilderServiceDeployAgentWithResponse(ctx, projectID, agentBuilderAgentID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	date, _ := http.ParseTime(resp.HTTPResponse.Header.Get("Date"))
	return &AgentBuilderAgentDeploy{LivekitPublicapiAgentbuilderV1AgentsDeployResponse: *resp.JSON200, Date: date}, nil
}

// DeleteAgentBuilderAgent deletes an Agent Builder agent, its versions and its deployment.
func (c *Client) DeleteAgentBuilderAgent(ctx context.Context, projectID, agentBuilderAgentID string) error {
	resp, err := c.gen.AgentBuilderServiceDeleteAgentWithResponse(ctx, projectID, agentBuilderAgentID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListAgentBuilderAgentVersions returns one page of an Agent Builder agent's versions,
// newest first, and the cursor of the next page ("" on the last). A limit of
// 0 lets the server choose the page size.
func (c *Client) ListAgentBuilderAgentVersions(ctx context.Context, projectID, agentBuilderAgentID string, limit int32, cursor string) ([]AgentBuilderAgentVersion, string, error) {
	params := &oapi.AgentBuilderServiceListAgentVersionsParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	resp, err := c.gen.AgentBuilderServiceListAgentVersionsWithResponse(ctx, projectID, agentBuilderAgentID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	var page struct {
		Items    []AgentBuilderAgentVersion             `json:"items"`
		PageInfo *oapi.LivekitPublicapiCommonV1PageInfo `json:"pageInfo"`
	}
	if err := json.Unmarshal(resp.Body, &page); err != nil {
		return nil, "", err
	}
	return page.Items, pageCursor(page.PageInfo), nil
}

// GetAgentBuilderAgentCodeURL returns a signed URL that downloads the agent code of
// an Agent Builder agent version as a zip, valid for expirySeconds (0 for the
// server's default). An empty versionID asks for the current version.
func (c *Client) GetAgentBuilderAgentCodeURL(ctx context.Context, projectID, agentBuilderAgentID, versionID string, expirySeconds int) (*oapi.LivekitPublicapiAgentbuilderV1AgentsGetCodeURLResponse, error) {
	params := &oapi.AgentBuilderServiceGetAgentCodeURLParams{}
	if versionID != "" {
		params.VersionId = &versionID
	}
	if expirySeconds > 0 {
		params.ExpirySeconds = &expirySeconds
	}
	resp, err := c.gen.AgentBuilderServiceGetAgentCodeURLWithResponse(ctx, projectID, agentBuilderAgentID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.Url == nil || *resp.JSON200.Url == "" {
		return nil, errors.New("unexpected response from server: missing url")
	}
	return resp.JSON200, nil
}

// maxAgentBuilderAgentCode is the largest agent code zip DownloadAgentBuilderAgentCode
// reads. The zips hold a few source files and the project docs.
const maxAgentBuilderAgentCode = 100 << 20

// DownloadAgentBuilderAgentCode downloads the agent code zip from a signed URL
// that GetAgentBuilderAgentCodeURL returned. As in DownloadRecording, the request
// sends no credentials, and no error holds the URL's signature. filename is
// the name the server suggests for the zip, or "" when it suggests none.
func DownloadAgentBuilderAgentCode(ctx context.Context, signedURL string) (zip []byte, filename string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download agent code: %w", redactURLError(err))
	}
	resp, err := signedURLClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download agent code: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAgentBuilderAgentCode+1))
	if err != nil {
		return nil, "", fmt.Errorf("download agent code: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		err := responseError(resp.StatusCode, body)
		if apiErr, ok := err.(*APIError); ok {
			apiErr.Message = redactSignature(apiErr.Message, signedURL)
		}
		return nil, "", err
	}
	if len(body) > maxAgentBuilderAgentCode {
		return nil, "", fmt.Errorf("download agent code: the zip is larger than %d MiB", maxAgentBuilderAgentCode>>20)
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if name := filepath.Base(params["filename"]); name != "." && name != ".." && name != string(filepath.Separator) {
			filename = name
		}
	}
	return body, filename, nil
}

// decodeAgentBuilderAgent reads the Agent Builder agent of a get, create or update
// response, keeping its configuration verbatim.
func decodeAgentBuilderAgent(status int, body []byte) (*AgentBuilderAgent, error) {
	if status != http.StatusOK {
		return nil, responseError(status, body)
	}
	var env struct {
		AgentBuilderAgent *AgentBuilderAgent `json:"agent"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	return requirePayload(env.AgentBuilderAgent, "Agent Builder agent")
}

// HasHiddenHeaderValues reports whether config, an Agent Builder agent
// configuration, holds HiddenHeaderValue in place of a header value: an item
// of a "headers" list whose "value" is HiddenHeaderValue.
func HasHiddenHeaderValues(config json.RawMessage) bool {
	var v any
	if json.Unmarshal(config, &v) != nil {
		return false
	}
	return hasHiddenHeaderValue(v, false)
}

func hasHiddenHeaderValue(v any, inHeaders bool) bool {
	switch v := v.(type) {
	case map[string]any:
		if inHeaders && v["value"] == HiddenHeaderValue {
			return true
		}
		for k, child := range v {
			if hasHiddenHeaderValue(child, k == "headers") {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasHiddenHeaderValue(child, inHeaders) {
				return true
			}
		}
	}
	return false
}
