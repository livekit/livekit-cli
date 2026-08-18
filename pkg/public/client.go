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

// Package public is the CLI's client for the user-authenticated LiveKit Public
// API. It wraps the Connect clients generated from the publicapi protobufs
// (pkg/gen, via the `generate` mage target) behind the small domain surface the
// CLI needs, so callers don't depend on the generated types directly.
//
// Transport: the generated clients speak the Connect protocol by default (works
// over HTTP/1.1 and HTTP/2). The server also accepts gRPC and gRPC-Web on the
// same endpoints; to force gRPC wire framing, pass connect.WithGRPC() through
// New's opts.
package public

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	commonv1 "github.com/livekit/livekit-cli/v2/pkg/gen/livekit/publicapi/common/v1"
	projectsv1 "github.com/livekit/livekit-cli/v2/pkg/gen/livekit/publicapi/projects/v1"
	"github.com/livekit/livekit-cli/v2/pkg/gen/livekit/publicapi/projects/v1/projectsv1connect"
)

// DefaultBaseURL is the production base URL of the LiveKit Public API. Override
// it (e.g. to http://localhost:8000) for local development. Connect appends the
// RPC path (e.g. /livekit.publicapi.projects.v1.ProjectService/ListProjects), so
// this is the host root, not a REST prefix.
const DefaultBaseURL = "https://api.livekit.cloud"

// Client is the CLI's user-authenticated Public API client.
type Client struct {
	projects projectsv1connect.ProjectServiceClient
}

// Project is a project the authenticated user can access.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Subdomain string `json:"subdomain,omitempty"`
}

// projectFrom maps a generated Project message to the domain type.
func projectFrom(p *projectsv1.Project) Project {
	return Project{ID: p.GetId(), Name: p.GetName(), Subdomain: p.GetSubdomain()}
}

// New builds a Client for the Public API at baseURL, authenticating every
// request with the given user session token. If baseURL is empty, DefaultBaseURL
// is used. Extra Connect client options (e.g. connect.WithGRPC()) may be passed.
func New(baseURL, token string, opts ...connect.ClientOption) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// Prepend the bearer-auth interceptor so callers' opts can still override
	// transport behavior.
	opts = append([]connect.ClientOption{connect.WithInterceptors(bearerAuth(token))}, opts...)
	return &Client{
		projects: projectsv1connect.NewProjectServiceClient(http.DefaultClient, baseURL, opts...),
	}, nil
}

// bearerAuth returns a Connect interceptor that authorizes each request with the
// user session token.
func bearerAuth(token string) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, req)
		}
	})
}

// ListProjects returns the projects the authenticated user can access.
//
// NOTE: this returns only the first page; wire up PageInfo/cursor paging when a
// command needs the full set.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	resp, err := c.projects.ListProjects(ctx, connect.NewRequest(&projectsv1.ListProjectsRequest{
		Page: &commonv1.PageRequest{
			PageSize: 100,
		},
	}))
	if err != nil {
		return nil, err
	}
	items := resp.Msg.GetItems()
	projects := make([]Project, len(items))
	for i, p := range items {
		projects[i] = projectFrom(p)
	}
	return projects, nil
}

// GetProject returns a single project by id.
func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	resp, err := c.projects.GetProject(ctx, connect.NewRequest(&projectsv1.GetProjectRequest{ProjectId: projectID}))
	if err != nil {
		return nil, err
	}
	p := projectFrom(resp.Msg.GetProject())
	return &p, nil
}

// CreateProject creates a new project with the given name and returns it.
func (c *Client) CreateProject(ctx context.Context, name string) (*Project, error) {
	resp, err := c.projects.CreateProject(ctx, connect.NewRequest(&projectsv1.CreateProjectRequest{Name: name}))
	if err != nil {
		return nil, err
	}
	p := projectFrom(resp.Msg.GetProject())
	return &p, nil
}

// UpdateProject updates a project's name and returns the updated project.
func (c *Client) UpdateProject(ctx context.Context, projectID, name string) (*Project, error) {
	resp, err := c.projects.UpdateProject(ctx, connect.NewRequest(&projectsv1.UpdateProjectRequest{
		ProjectId: projectID,
		Name:      &name,
	}))
	if err != nil {
		return nil, err
	}
	p := projectFrom(resp.Msg.GetProject())
	return &p, nil
}

// DeleteProject deletes a project by id.
func (c *Client) DeleteProject(ctx context.Context, projectID string) error {
	_, err := c.projects.DeleteProject(ctx, connect.NewRequest(&projectsv1.DeleteProjectRequest{ProjectId: projectID}))
	return err
}

// IsUnauthenticated reports whether err is a Connect error signalling a missing
// or invalid session (Unauthenticated), which the CLI surfaces as a prompt to
// re-run `lk cloud auth`.
func IsUnauthenticated(err error) bool {
	return connect.CodeOf(err) == connect.CodeUnauthenticated
}

// IsPermissionDenied reports whether err is a Connect error signalling that the
// authenticated account/session lacks access to the target project or action
// (PermissionDenied) — distinct from being unauthenticated.
func IsPermissionDenied(err error) bool {
	return connect.CodeOf(err) == connect.CodePermissionDenied
}
