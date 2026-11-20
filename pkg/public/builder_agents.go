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
// of a builder agent configuration to a caller who may read the agent but not
// change it. The API refuses it as a header value on a write.
const HiddenHeaderValue = "********"

// BuilderAgent is a builder agent as the Public API sent it, with its
// configuration verbatim. The generated configuration type drops properties
// this client doesn't know, so a configuration read through it and written
// back would lose them.
type BuilderAgent struct {
	oapi.LivekitPublicapiAgentsV1BuilderAgent
	Config json.RawMessage `json:"config,omitempty"`
}

// BuilderAgentVersion is a builder agent version as the Public API sent it,
// with its configuration verbatim, as in BuilderAgent.
type BuilderAgentVersion struct {
	oapi.LivekitPublicapiAgentsV1BuilderAgentVersion
	Config json.RawMessage `json:"config,omitempty"`
}

// builderAgentWrite is the body of a builder agent create or update. The
// configuration goes as the caller wrote it, for the reason in BuilderAgent.
type builderAgentWrite struct {
	Name   *string         `json:"name,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// ListBuilderAgents returns one page of a project's builder agents, without
// their configurations, and the cursor of the next page ("" on the last).
// A limit of 0 lets the server choose the page size.
func (c *Client) ListBuilderAgents(ctx context.Context, projectID string, limit int32, cursor string) ([]oapi.LivekitPublicapiAgentsV1BuilderAgent, string, error) {
	params := &oapi.BuilderAgentServiceListBuilderAgentsParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	resp, err := c.gen.BuilderAgentServiceListBuilderAgentsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}

// GetBuilderAgent returns a builder agent with its configuration.
func (c *Client) GetBuilderAgent(ctx context.Context, projectID, builderAgentID string) (*BuilderAgent, error) {
	resp, err := c.gen.BuilderAgentServiceGetBuilderAgentWithResponse(ctx, projectID, builderAgentID)
	if err != nil {
		return nil, err
	}
	return decodeBuilderAgent(resp.StatusCode(), resp.Body)
}

// CreateBuilderAgent creates a builder agent named name from config, a JSON
// object. A nil config takes the default configuration.
func (c *Client) CreateBuilderAgent(ctx context.Context, projectID, name string, config json.RawMessage) (*BuilderAgent, error) {
	body, err := json.Marshal(builderAgentWrite{Name: &name, Config: config})
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.BuilderAgentServiceCreateBuilderAgentWithBodyWithResponse(ctx, projectID, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return decodeBuilderAgent(resp.StatusCode(), resp.Body)
}

// UpdateBuilderAgent replaces a builder agent's configuration with config, a
// JSON object, renames it to name, or both. A nil name or config leaves that
// unchanged.
func (c *Client) UpdateBuilderAgent(ctx context.Context, projectID, builderAgentID string, name *string, config json.RawMessage) (*BuilderAgent, error) {
	body, err := json.Marshal(builderAgentWrite{Name: name, Config: config})
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.BuilderAgentServiceUpdateBuilderAgentWithBodyWithResponse(ctx, projectID, builderAgentID, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return decodeBuilderAgent(resp.StatusCode(), resp.Body)
}

// BuilderAgentDeploy is the answer to a builder agent deploy: the version the
// deployment builds and its status, which the API sends, and the server's
// time of the answer.
type BuilderAgentDeploy struct {
	oapi.LivekitPublicapiAgentsV1BuilderAgentsDeployResponse
	// Date is the time in the response's Date header, to the second, or the
	// zero time without one. It lets a caller tell a deployment that started
	// after the deploy from an earlier one by server clocks alone.
	Date time.Time `json:"-"`
}

// DeployBuilderAgent starts a deployment of a builder agent's version, or of
// its current configuration when versionID is "". It returns once the build
// starts.
func (c *Client) DeployBuilderAgent(ctx context.Context, projectID, builderAgentID, versionID string) (*BuilderAgentDeploy, error) {
	body := oapi.BuilderAgentServiceDeployBuilderAgentJSONRequestBody{}
	if versionID != "" {
		body.VersionId = &versionID
	}
	resp, err := c.gen.BuilderAgentServiceDeployBuilderAgentWithResponse(ctx, projectID, builderAgentID, body)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	date, _ := http.ParseTime(resp.HTTPResponse.Header.Get("Date"))
	return &BuilderAgentDeploy{LivekitPublicapiAgentsV1BuilderAgentsDeployResponse: *resp.JSON200, Date: date}, nil
}

// DeleteBuilderAgent deletes a builder agent, its versions and its deployment.
func (c *Client) DeleteBuilderAgent(ctx context.Context, projectID, builderAgentID string) error {
	resp, err := c.gen.BuilderAgentServiceDeleteBuilderAgentWithResponse(ctx, projectID, builderAgentID)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListBuilderAgentVersions returns one page of a builder agent's versions,
// newest first, and the cursor of the next page ("" on the last). A limit of
// 0 lets the server choose the page size.
func (c *Client) ListBuilderAgentVersions(ctx context.Context, projectID, builderAgentID string, limit int32, cursor string) ([]BuilderAgentVersion, string, error) {
	params := &oapi.BuilderAgentServiceListBuilderAgentVersionsParams{}
	if limit > 0 {
		params.PagePageSize = ptr(limit)
	}
	if cursor != "" {
		params.PageCursor = ptr(cursor)
	}
	resp, err := c.gen.BuilderAgentServiceListBuilderAgentVersionsWithResponse(ctx, projectID, builderAgentID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	var page struct {
		Items    []BuilderAgentVersion                  `json:"items"`
		PageInfo *oapi.LivekitPublicapiCommonV1PageInfo `json:"pageInfo"`
	}
	if err := json.Unmarshal(resp.Body, &page); err != nil {
		return nil, "", err
	}
	return page.Items, pageCursor(page.PageInfo), nil
}

// GetBuilderAgentCodeURL returns a signed URL that downloads the agent code of
// a builder agent version as a zip, valid for expirySeconds (0 for the
// server's default). An empty versionID asks for the current version.
func (c *Client) GetBuilderAgentCodeURL(ctx context.Context, projectID, builderAgentID, versionID string, expirySeconds int) (*oapi.LivekitPublicapiAgentsV1BuilderAgentsGetCodeURLResponse, error) {
	params := &oapi.BuilderAgentServiceGetBuilderAgentCodeURLParams{}
	if versionID != "" {
		params.VersionId = &versionID
	}
	if expirySeconds > 0 {
		params.ExpirySeconds = &expirySeconds
	}
	resp, err := c.gen.BuilderAgentServiceGetBuilderAgentCodeURLWithResponse(ctx, projectID, builderAgentID, params)
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

// maxBuilderAgentCode is the largest agent code zip DownloadBuilderAgentCode
// reads. The zips hold a few source files and the project docs.
const maxBuilderAgentCode = 100 << 20

// DownloadBuilderAgentCode downloads the agent code zip from a signed URL
// that GetBuilderAgentCodeURL returned. As in DownloadRecording, the request
// sends no credentials, and no error holds the URL's signature. filename is
// the name the server suggests for the zip, or "" when it suggests none.
func DownloadBuilderAgentCode(ctx context.Context, signedURL string) (zip []byte, filename string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download agent code: %w", redactURLError(err))
	}
	resp, err := signedURLClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download agent code: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBuilderAgentCode+1))
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
	if len(body) > maxBuilderAgentCode {
		return nil, "", fmt.Errorf("download agent code: the zip is larger than %d MiB", maxBuilderAgentCode>>20)
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if name := filepath.Base(params["filename"]); name != "." && name != ".." && name != string(filepath.Separator) {
			filename = name
		}
	}
	return body, filename, nil
}

// decodeBuilderAgent reads the builder agent of a get, create or update
// response, keeping its configuration verbatim.
func decodeBuilderAgent(status int, body []byte) (*BuilderAgent, error) {
	if status != http.StatusOK {
		return nil, responseError(status, body)
	}
	var env struct {
		BuilderAgent *BuilderAgent `json:"builderAgent"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	return requirePayload(env.BuilderAgent, "builder agent")
}

// HasHiddenHeaderValues reports whether config, a builder agent
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
