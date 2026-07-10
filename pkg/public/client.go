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
)

// DefaultBaseURL is the production base URL of the LiveKit Public API. Override
// it (e.g. to http://localhost:8000/v1) for local development.
const DefaultBaseURL = "https://api.livekit.cloud/v1"

// Client is the CLI's client for the user-authenticated LiveKit Public API. It
// wraps the oapi-codegen-generated client (package oapi) and exposes the small
// set of domain types and operations the CLI needs, insulating callers from the
// generated surface (which is regenerated from the OpenAPI spec).
type Client struct {
	gen *oapi.ClientWithResponses
}

// Project is a project the authenticated user can access.
//
// NOTE: the published spec does not yet describe the project endpoints (they
// are 501-only, with no success schema), so ListProjects/GetProject decode
// their responses by hand below rather than through generated types. This
// mirror carries only the id the server currently returns; extend it (and the
// decoding) as the endpoints — ideally the spec itself — grow.
type Project struct {
	ID string
}

// New builds a Client for the Public API at baseURL, authenticating every
// request with the given user session token. If baseURL is empty, DefaultBaseURL
// is used.
func New(baseURL, token string, opts ...oapi.ClientOption) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// Prepend the bearer-auth editor so callers' opts can still override it.
	opts = append([]oapi.ClientOption{oapi.WithRequestEditorFn(bearerAuth(token))}, opts...)
	gen, err := oapi.NewClientWithResponses(baseURL, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{gen: gen}, nil
}

// bearerAuth returns a request editor that authorizes each request with the
// user session token.
func bearerAuth(token string) oapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// ListProjects returns the projects the authenticated user can access.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	resp, err := c.gen.ListProjectsWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	// The spec has no schema for this endpoint yet, so decode the body directly.
	var body []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, fmt.Errorf("decode projects: %w", err)
	}
	projects := make([]Project, len(body))
	for i, p := range body {
		projects[i] = Project{ID: p.ID}
	}
	return projects, nil
}

// GetProject returns a single project by id.
func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	resp, err := c.gen.GetProjectWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, fmt.Errorf("decode project: %w", err)
	}
	return &Project{ID: body.ID}, nil
}

// APIError is a structured error from the Public API. It carries the HTTP status
// and, when the body decoded as the spec's Error schema, its code and message.
// Callers can errors.As for it — notably via IsUnauthenticated.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// IsUnauthenticated reports whether err is an APIError signalling a missing or
// invalid session (HTTP 401), which the CLI surfaces as a prompt to re-run
// `lk cloud auth`.
func IsUnauthenticated(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Code == "unauthenticated")
}

// responseError builds an APIError from a non-2xx response. It prefers the
// spec's structured Error body ({error:{code,message}}) and falls back to the
// raw body when the server returned an unexpected shape or content type.
func responseError(status int, body []byte) error {
	var e oapi.Error
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Code != "" {
		return &APIError{Status: status, Code: e.Error.Code, Message: e.Error.Message}
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &APIError{Status: status, Message: fmt.Sprintf("unexpected response (HTTP %d): %s", status, msg)}
}
