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

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/tokenauth"
	"github.com/livekit/protocol/auth"
)

// Session auth for server SDK calls.
//
// Commands that talk to LiveKit only through server-sdk-go (room, sip, egress,
// ingress, dispatch, phone-number, replay, cloud agents), join rooms (room
// join, perf load tests, the egress layout demo) or print tokens (token create)
// load their project with loadProjectForSDK or requireProjectForSDK. With an
// API key these behave like loadProjectDetails/requireProject. Under
// --experimental-auth they load a session project instead: the user's project
// with placeholder credentials. Its SDK clients (via withDefaultClientOpts, or
// tokenauth's Transport for cloudagents) swap each request's token for one
// fetched from the Public API (ProjectService.CreateToken) with the same grants,
// and tokens the CLI signs itself go through the tokenauth helpers with
// sessionTokenSource.
//
// Apps and agents need a real key of their own; see app_credentials.go. The
// rest (agent simulate's local runs) keep loadProjectDetails/requireProject
// and stay behind experimentalAuthGate.

// sessionTokenSources holds the token source for each session project, by
// project ID, for withDefaultClientOpts to attach.
var sessionTokenSources = map[string]*tokenauth.CachingTokenSource{}

// loadProjectForSDK is loadProjectDetails for commands whose credentials go
// only to SDK clients built with withDefaultClientOpts or to the tokenauth
// helpers, so a session project will do.
func loadProjectForSDK(ctx context.Context, cmd *cli.Command, opts ...loadOption) (*config.ProjectConfig, error) {
	if experimentalAuthEnabled(cmd) {
		return loadSessionProject(ctx, cmd)
	}
	return loadProjectDetails(cmd, opts...)
}

// requireProjectForSDK is requireProjectWithOpts for commands whose
// credentials go only to SDK clients built with withDefaultClientOpts or to the
// tokenauth helpers, so a session project will do.
func requireProjectForSDK(ctx context.Context, cmd *cli.Command, opts ...loadOption) (context.Context, error) {
	if !experimentalAuthEnabled(cmd) {
		return requireProjectWithOpts(ctx, cmd, opts...)
	}
	if project == nil {
		pc, err := loadSessionProject(ctx, cmd)
		if err != nil {
			return ctx, err
		}
		project = pc
	}
	return ctx, nil
}

// loadSessionProject resolves the signed-in user's project (--project, default
// project, or picker) and returns a ProjectConfig with placeholder credentials,
// whose SDK clients are authorized by tokens minted for the session.
func loadSessionProject(ctx context.Context, cmd *cli.Command) (*config.ProjectConfig, error) {
	client, conf, user, err := newCloudAPIClient(cmd)
	if err != nil {
		return nil, err
	}
	id, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return nil, err
	}
	up := user.FindProject(id)
	if up != nil && up.URL == "" {
		// Cached before the API returned project URLs; refresh once.
		if _, err := refreshUserProjects(ctx, conf, user); err != nil {
			return nil, cloudAPIError(err)
		}
		up = user.FindProject(id)
	}
	if up == nil {
		return nil, fmt.Errorf("project %q not found for %s", id, userLabel(user))
	}
	if up.URL == "" {
		return nil, fmt.Errorf("the API did not return a server URL for project %s", id)
	}
	sessionTokenSources[id] = tokenauth.NewCachingTokenSource(&publicAPITokenSource{client: client, projectID: id})
	return &config.ProjectConfig{
		Name:      up.Name,
		ProjectId: id,
		URL:       up.URL,
		APIKey:    tokenauth.PlaceholderAPIKey,
		APISecret: tokenauth.PlaceholderAPISecret,
	}, nil
}

// publicAPITokenSource fetches tokens from the Public API as the signed-in user.
type publicAPITokenSource struct {
	client    *public.Client
	projectID string
}

func (s *publicAPITokenSource) Fetch(ctx context.Context, grants *auth.ClaimGrants, ttl time.Duration) (string, time.Time, error) {
	token, expiresAt, err := s.client.CreateToken(ctx, s.projectID, grants, ttl)
	if err != nil {
		return "", time.Time{}, cloudAPIError(err)
	}
	// The API drops grants it doesn't know rather than rejecting them.
	if err := tokenauth.CheckGrants(token, grants); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// fetchSessionToken fetches a one-off token (e.g. a participant token) for a
// session project.
func fetchSessionToken(ctx context.Context, pc *config.ProjectConfig, grants *auth.ClaimGrants, ttl time.Duration) (string, error) {
	src := sessionTokenSource(pc)
	if src == nil {
		return "", fmt.Errorf("project %s was not loaded with session auth", pc.ProjectId)
	}
	token, _, err := src.Fetch(ctx, grants, ttl)
	return token, err
}

// sessionTokenSource returns the token source for a session project, or nil
// when pc has real credentials. The tokenauth helpers (Sign, JoinRoom,
// ConnectToRoom) take it directly: nil means sign locally with pc's key.
func sessionTokenSource(pc *config.ProjectConfig) *tokenauth.CachingTokenSource {
	if !isSessionProject(pc) {
		return nil
	}
	return sessionTokenSources[pc.ProjectId]
}

// isSessionProject reports whether pc came from loadSessionProject, so its
// credentials are placeholders that can't sign anything locally.
func isSessionProject(pc *config.ProjectConfig) bool {
	return pc != nil && tokenauth.IsPlaceholder(pc.APIKey)
}
