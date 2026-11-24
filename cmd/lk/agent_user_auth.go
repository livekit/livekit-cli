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
	"io"
	"io/fs"
	"net/http"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
)

// The cloud agent commands call cloud agents through server-sdk-go's
// cloudagents client. With an API key it signs each call with the key. Under
// --user-auth the same client goes through the token bridge (see
// session_auth.go), which asks the Public API for a token with the Agent.Admin
// grant. Each command takes one of the interfaces below, as the sip and number
// commands do (see telephony_user_auth.go): the SDK client implements it with
// an API key, and userAuthAgentAPI under --user-auth. userAuthAgentAPI sends
// every call through the bridge for now. A method moves to the Public API's
// AgentService by calling a.client in place of a.bridge.

// agentAPI is what the cloud agent commands call.
type agentAPI interface {
	ListAgents(ctx context.Context, req *livekit.ListAgentsRequest) (*livekit.ListAgentsResponse, error)
	ListAgentVersions(ctx context.Context, req *livekit.ListAgentVersionsRequest) (*livekit.ListAgentVersionsResponse, error)
	ListAgentSecrets(ctx context.Context, req *livekit.ListAgentSecretsRequest) (*livekit.ListAgentSecretsResponse, error)
	UpdateAgentSecrets(ctx context.Context, req *livekit.UpdateAgentSecretsRequest) (*livekit.UpdateAgentSecretsResponse, error)
	UpdateAgent(ctx context.Context, req *livekit.UpdateAgentRequest) (*livekit.UpdateAgentResponse, error)
	RestartAgent(ctx context.Context, req *livekit.RestartAgentRequest) (*livekit.RestartAgentResponse, error)
	RollbackAgent(ctx context.Context, req *livekit.RollbackAgentRequest) (*livekit.RollbackAgentResponse, error)
	PromoteAgent(ctx context.Context, agentID, srcDeployment, dstDeployment string) error
	DeleteAgent(ctx context.Context, req *livekit.DeleteAgentRequest) (*livekit.DeleteAgentResponse, error)
	GetClientSettings(ctx context.Context, req *livekit.ClientSettingsRequest) (*livekit.ClientSettingsResponse, error)
	// CreateAgent registers an agent, uploads source, and builds and deploys
	// it to production, writing the build log to buildLog.
	CreateAgent(ctx context.Context, source fs.FS, secrets []*livekit.AgentSecret, regions []string, attributes map[string]string, excludeFiles []string, buildLog io.Writer) (*livekit.CreateAgentResponse, error)
	// DeployAgentV2 uploads source, and builds and deploys it to deployment
	// (production when ""), writing the build log to buildLog.
	DeployAgentV2(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, buildLog io.Writer) error
	// RegisterAgent, GetPushTarget and NewRegistryTransport push a prebuilt
	// image (--image, --image-tar).
	RegisterAgent(ctx context.Context, secrets []*livekit.AgentSecret, regions []string) (string, error)
	GetPushTarget(ctx context.Context, agentID string) (*cloudagents.PushTarget, error)
	NewRegistryTransport(attributes map[string]string) http.RoundTripper
	// StreamLogs writes an agent's runtime ("deploy") or build ("build") log
	// to w until it ends or ctx is done.
	StreamLogs(ctx context.Context, logType, agentID, deployment string, w io.Writer, serverRegion string) error
}

// privateLinkAPI is what the agent private-link commands call.
type privateLinkAPI interface {
	CreatePrivateLink(ctx context.Context, req *livekit.CreatePrivateLinkRequest) (*livekit.CreatePrivateLinkResponse, error)
	ListPrivateLinks(ctx context.Context, req *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error)
	GetPrivateLinkStatus(ctx context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error)
	DestroyPrivateLink(ctx context.Context, req *livekit.DestroyPrivateLinkRequest) (*livekit.DestroyPrivateLinkResponse, error)
}

var (
	_ agentAPI       = (*cloudagents.Client)(nil)
	_ agentAPI       = (*userAuthAgentAPI)(nil)
	_ privateLinkAPI = (*cloudagents.Client)(nil)
	_ privateLinkAPI = (*userAuthAgentAPI)(nil)
)

// newAgentAPI returns the cloud agents API for pc: server-sdk-go's client
// with an API key, or userAuthAgentAPI under --user-auth.
func newAgentAPI(cmd *cli.Command, pc *config.ProjectConfig) (agentAPI, privateLinkAPI, error) {
	sdk, err := newAgentsClient(pc)
	if err != nil {
		return nil, nil, err
	}
	if !userAuthEnabled(cmd) {
		return sdk, sdk, nil
	}
	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return nil, nil, err
	}
	a := &userAuthAgentAPI{client: client, projectID: pc.ProjectId, bridge: sdk}
	return a, a, nil
}

// userAuthAgentAPI is agentAPI and privateLinkAPI under --user-auth, for one
// project. Each method calls bridge, the cloudagents client on the token
// bridge, until the Public API serves it.
type userAuthAgentAPI struct {
	client    *public.Client
	projectID string
	bridge    *cloudagents.Client
}

func (a *userAuthAgentAPI) ListAgents(ctx context.Context, req *livekit.ListAgentsRequest) (*livekit.ListAgentsResponse, error) {
	return a.bridge.ListAgents(ctx, req)
}

func (a *userAuthAgentAPI) ListAgentVersions(ctx context.Context, req *livekit.ListAgentVersionsRequest) (*livekit.ListAgentVersionsResponse, error) {
	return a.bridge.ListAgentVersions(ctx, req)
}

func (a *userAuthAgentAPI) ListAgentSecrets(ctx context.Context, req *livekit.ListAgentSecretsRequest) (*livekit.ListAgentSecretsResponse, error) {
	return a.bridge.ListAgentSecrets(ctx, req)
}

func (a *userAuthAgentAPI) UpdateAgentSecrets(ctx context.Context, req *livekit.UpdateAgentSecretsRequest) (*livekit.UpdateAgentSecretsResponse, error) {
	return a.bridge.UpdateAgentSecrets(ctx, req)
}

func (a *userAuthAgentAPI) UpdateAgent(ctx context.Context, req *livekit.UpdateAgentRequest) (*livekit.UpdateAgentResponse, error) {
	return a.bridge.UpdateAgent(ctx, req)
}

func (a *userAuthAgentAPI) RestartAgent(ctx context.Context, req *livekit.RestartAgentRequest) (*livekit.RestartAgentResponse, error) {
	return a.bridge.RestartAgent(ctx, req)
}

func (a *userAuthAgentAPI) RollbackAgent(ctx context.Context, req *livekit.RollbackAgentRequest) (*livekit.RollbackAgentResponse, error) {
	return a.bridge.RollbackAgent(ctx, req)
}

func (a *userAuthAgentAPI) PromoteAgent(ctx context.Context, agentID, srcDeployment, dstDeployment string) error {
	return a.bridge.PromoteAgent(ctx, agentID, srcDeployment, dstDeployment)
}

func (a *userAuthAgentAPI) DeleteAgent(ctx context.Context, req *livekit.DeleteAgentRequest) (*livekit.DeleteAgentResponse, error) {
	return a.bridge.DeleteAgent(ctx, req)
}

func (a *userAuthAgentAPI) GetClientSettings(ctx context.Context, req *livekit.ClientSettingsRequest) (*livekit.ClientSettingsResponse, error) {
	return a.bridge.GetClientSettings(ctx, req)
}

func (a *userAuthAgentAPI) CreateAgent(ctx context.Context, source fs.FS, secrets []*livekit.AgentSecret, regions []string, attributes map[string]string, excludeFiles []string, buildLog io.Writer) (*livekit.CreateAgentResponse, error) {
	return a.bridge.CreateAgent(ctx, source, secrets, regions, attributes, excludeFiles, buildLog)
}

func (a *userAuthAgentAPI) DeployAgentV2(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, buildLog io.Writer) error {
	return a.bridge.DeployAgentV2(ctx, agentID, source, secrets, attributes, deployment, excludeFiles, buildLog)
}

// RegisterAgent, GetPushTarget and NewRegistryTransport stay on the bridge:
// the Public API does not front cloud agents' image registry.
func (a *userAuthAgentAPI) RegisterAgent(ctx context.Context, secrets []*livekit.AgentSecret, regions []string) (string, error) {
	return a.bridge.RegisterAgent(ctx, secrets, regions)
}

func (a *userAuthAgentAPI) GetPushTarget(ctx context.Context, agentID string) (*cloudagents.PushTarget, error) {
	return a.bridge.GetPushTarget(ctx, agentID)
}

func (a *userAuthAgentAPI) NewRegistryTransport(attributes map[string]string) http.RoundTripper {
	return a.bridge.NewRegistryTransport(attributes)
}

func (a *userAuthAgentAPI) StreamLogs(ctx context.Context, logType, agentID, deployment string, w io.Writer, serverRegion string) error {
	return a.bridge.StreamLogs(ctx, logType, agentID, deployment, w, serverRegion)
}

func (a *userAuthAgentAPI) CreatePrivateLink(ctx context.Context, req *livekit.CreatePrivateLinkRequest) (*livekit.CreatePrivateLinkResponse, error) {
	return a.bridge.CreatePrivateLink(ctx, req)
}

func (a *userAuthAgentAPI) ListPrivateLinks(ctx context.Context, req *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error) {
	return a.bridge.ListPrivateLinks(ctx, req)
}

func (a *userAuthAgentAPI) GetPrivateLinkStatus(ctx context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error) {
	return a.bridge.GetPrivateLinkStatus(ctx, req)
}

func (a *userAuthAgentAPI) DestroyPrivateLink(ctx context.Context, req *livekit.DestroyPrivateLinkRequest) (*livekit.DestroyPrivateLinkResponse, error) {
	return a.bridge.DestroyPrivateLink(ctx, req)
}
