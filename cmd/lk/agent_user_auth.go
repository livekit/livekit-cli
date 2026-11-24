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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

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
// an API key, and userAuthAgentAPI under --user-auth. userAuthAgentAPI calls
// the Public API's AgentService, which checks the user's role on the project
// itself, for ListAgents, and sends every other call through the bridge for
// now. A method moves to AgentService by calling a.client in place of
// a.bridge.

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
// project. A method that the Public API serves calls client and passes the
// error through cloudAPIError. Each other method calls bridge, the
// cloudagents client on the token bridge, and passes the error through
// bridgeError.
type userAuthAgentAPI struct {
	client    *public.Client
	projectID string
	bridge    *cloudagents.Client
}

// ListAgents lists the project's agents, or gets the one req.AgentId names,
// through AgentService, which a READ member may call. An agent the project
// doesn't have is an empty list, as cloud agents answers it. Each agent is
// as cloud agents lists it (see cloudAgentsInfo), so the commands print the
// same in both modes.
func (a *userAuthAgentAPI) ListAgents(ctx context.Context, req *livekit.ListAgentsRequest) (*livekit.ListAgentsResponse, error) {
	var agents []*public.Agent
	if id := req.GetAgentId(); id != "" {
		agent, err := a.client.GetAgent(ctx, a.projectID, id)
		if public.IsAgentNotFound(err) {
			return &livekit.ListAgentsResponse{}, nil
		}
		if err != nil {
			return nil, cloudAPIError(err)
		}
		agents = []*public.Agent{agent}
	} else {
		var err error
		if agents, err = a.client.ListAgents(ctx, a.projectID); err != nil {
			return nil, cloudAPIError(err)
		}
	}
	res := &livekit.ListAgentsResponse{Agents: make([]*livekit.AgentInfo, 0, len(agents))}
	for _, agent := range agents {
		res.Agents = append(res.Agents, cloudAgentsInfo(agent.Info))
	}
	return res, nil
}

// noVersion is the version cloud agents lists for an agent or a deployment
// whose version it doesn't know.
const noVersion = "---"

// cloudAgentsInfo returns info, an agent as the Public API returns it, as
// cloud agents' ListAgents lists it. The Public API leaves out the version
// and the deploy time that cloud agents doesn't know, where cloud agents
// lists "---" and the zero time, so cloudAgentsInfo puts those back. It
// can't put back the agent name "<Pending>" or "<Unavailable>", which the
// Public API also leaves out: that name stays empty.
func cloudAgentsInfo(info *livekit.AgentInfo) *livekit.AgentInfo {
	if info.Version == "" {
		info.Version = noVersion
	}
	if info.DeployedAt == nil {
		info.DeployedAt = timestamppb.New(time.Time{})
	}
	for _, d := range info.AgentDeployments {
		if d.Version == "" {
			d.Version = noVersion
		}
	}
	return info
}

func (a *userAuthAgentAPI) ListAgentVersions(ctx context.Context, req *livekit.ListAgentVersionsRequest) (*livekit.ListAgentVersionsResponse, error) {
	res, err := a.bridge.ListAgentVersions(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) ListAgentSecrets(ctx context.Context, req *livekit.ListAgentSecretsRequest) (*livekit.ListAgentSecretsResponse, error) {
	res, err := a.bridge.ListAgentSecrets(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) UpdateAgentSecrets(ctx context.Context, req *livekit.UpdateAgentSecretsRequest) (*livekit.UpdateAgentSecretsResponse, error) {
	res, err := a.bridge.UpdateAgentSecrets(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) UpdateAgent(ctx context.Context, req *livekit.UpdateAgentRequest) (*livekit.UpdateAgentResponse, error) {
	res, err := a.bridge.UpdateAgent(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) RestartAgent(ctx context.Context, req *livekit.RestartAgentRequest) (*livekit.RestartAgentResponse, error) {
	res, err := a.bridge.RestartAgent(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) RollbackAgent(ctx context.Context, req *livekit.RollbackAgentRequest) (*livekit.RollbackAgentResponse, error) {
	res, err := a.bridge.RollbackAgent(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) PromoteAgent(ctx context.Context, agentID, srcDeployment, dstDeployment string) error {
	return bridgeError(a.bridge.PromoteAgent(ctx, agentID, srcDeployment, dstDeployment))
}

func (a *userAuthAgentAPI) DeleteAgent(ctx context.Context, req *livekit.DeleteAgentRequest) (*livekit.DeleteAgentResponse, error) {
	res, err := a.bridge.DeleteAgent(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) GetClientSettings(ctx context.Context, req *livekit.ClientSettingsRequest) (*livekit.ClientSettingsResponse, error) {
	res, err := a.bridge.GetClientSettings(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) CreateAgent(ctx context.Context, source fs.FS, secrets []*livekit.AgentSecret, regions []string, attributes map[string]string, excludeFiles []string, buildLog io.Writer) (*livekit.CreateAgentResponse, error) {
	res, err := a.bridge.CreateAgent(ctx, source, secrets, regions, attributes, excludeFiles, buildLog)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) DeployAgentV2(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, buildLog io.Writer) error {
	return bridgeError(a.bridge.DeployAgentV2(ctx, agentID, source, secrets, attributes, deployment, excludeFiles, buildLog))
}

// RegisterAgent, GetPushTarget and NewRegistryTransport stay on the bridge:
// the Public API does not front cloud agents' image registry.
func (a *userAuthAgentAPI) RegisterAgent(ctx context.Context, secrets []*livekit.AgentSecret, regions []string) (string, error) {
	res, err := a.bridge.RegisterAgent(ctx, secrets, regions)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) GetPushTarget(ctx context.Context, agentID string) (*cloudagents.PushTarget, error) {
	res, err := a.bridge.GetPushTarget(ctx, agentID)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) NewRegistryTransport(attributes map[string]string) http.RoundTripper {
	return a.bridge.NewRegistryTransport(attributes)
}

func (a *userAuthAgentAPI) StreamLogs(ctx context.Context, logType, agentID, deployment string, w io.Writer, serverRegion string) error {
	return bridgeError(a.bridge.StreamLogs(ctx, logType, agentID, deployment, w, serverRegion))
}

func (a *userAuthAgentAPI) CreatePrivateLink(ctx context.Context, req *livekit.CreatePrivateLinkRequest) (*livekit.CreatePrivateLinkResponse, error) {
	res, err := a.bridge.CreatePrivateLink(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) ListPrivateLinks(ctx context.Context, req *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error) {
	res, err := a.bridge.ListPrivateLinks(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) GetPrivateLinkStatus(ctx context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error) {
	res, err := a.bridge.GetPrivateLinkStatus(ctx, req)
	return res, bridgeError(err)
}

func (a *userAuthAgentAPI) DestroyPrivateLink(ctx context.Context, req *livekit.DestroyPrivateLinkRequest) (*livekit.DestroyPrivateLinkResponse, error) {
	res, err := a.bridge.DestroyPrivateLink(ctx, req)
	return res, bridgeError(err)
}

// bridgeError is err from a call through the token bridge, or, when the
// Public API refused the bridge's token, an error that says why. cloud agents
// takes no grant narrower than Agent.Admin, and the Public API gives that one
// only to a project admin, so the refusal says the command needs one, not
// that the user lacks access to the project.
func bridgeError(err error) error {
	var refused *public.APIError
	if !errors.As(err, &refused) || !public.IsPermissionDenied(refused) {
		return err
	}
	return fmt.Errorf("signed in with --user-auth, this command needs a project admin (%w). "+
		"A project API key works meanwhile: re-run without --user-auth", refused)
}
