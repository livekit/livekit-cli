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
	"regexp"
	"strconv"
	"strings"
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
// itself, for the reads and writes it serves: the agents, their versions,
// secret names and build logs, the project's client settings, and restart,
// rollback, promote, delete and secret updates. It sends every other call,
// such as create, deploy, a runtime log and private links, through the
// bridge for now. A method moves to AgentService by calling a.client in
// place of a.bridge.

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

// ListAgentVersions lists the agent's versions through AgentService, which
// a READ member may call. The Public API leaves out the deploy time of a
// version that was never deployed, where cloud agents lists the zero time,
// so ListAgentVersions puts that back and the commands print the same in
// both modes.
func (a *userAuthAgentAPI) ListAgentVersions(ctx context.Context, req *livekit.ListAgentVersionsRequest) (*livekit.ListAgentVersionsResponse, error) {
	versions, err := a.client.ListAgentVersions(ctx, a.projectID, req.GetAgentId())
	if err != nil {
		return nil, cloudAPIError(err)
	}
	for _, v := range versions {
		if v.DeployedAt == nil {
			v.DeployedAt = timestamppb.New(time.Time{})
		}
	}
	return &livekit.ListAgentVersionsResponse{Versions: versions}, nil
}

// ListAgentSecrets lists the names of the agent's secrets through
// AgentService, which, as the dashboard, lets only a project admin list
// them.
func (a *userAuthAgentAPI) ListAgentSecrets(ctx context.Context, req *livekit.ListAgentSecretsRequest) (*livekit.ListAgentSecretsResponse, error) {
	secrets, err := a.client.ListAgentSecrets(ctx, a.projectID, req.GetAgentId())
	if err != nil {
		return nil, agentServiceError(err, projectAdmin)
	}
	return &livekit.ListAgentSecretsResponse{Secrets: secrets}, nil
}

// UpdateAgentSecrets sets req.Secrets and removes req.Remove through
// AgentService, which lets only a project admin change secrets. AgentService
// only merges, so an update with req.Overwrite, which replaces every secret,
// goes through the bridge, which also needs a project admin.
func (a *userAuthAgentAPI) UpdateAgentSecrets(ctx context.Context, req *livekit.UpdateAgentSecretsRequest) (*livekit.UpdateAgentSecretsResponse, error) {
	if req.GetOverwrite() {
		res, err := a.bridge.UpdateAgentSecrets(ctx, req)
		return res, bridgeError(err)
	}
	if err := a.updateSecrets(ctx, req.GetAgentId(), req.GetSecrets(), req.GetRemove()); err != nil {
		return nil, err
	}
	return &livekit.UpdateAgentSecretsResponse{Success: true}, nil
}

// UpdateAgent sets req.Secrets through AgentService, which, as cloud agents'
// UpdateAgent, merges them and restarts the agent. With no secrets, cloud
// agents' UpdateAgent only restarts the agent, and so does UpdateAgent. lk
// sends no regions, which cloud agents ignores.
func (a *userAuthAgentAPI) UpdateAgent(ctx context.Context, req *livekit.UpdateAgentRequest) (*livekit.UpdateAgentResponse, error) {
	if len(req.GetSecrets()) == 0 {
		if _, err := a.RestartAgent(ctx, &livekit.RestartAgentRequest{AgentId: req.GetAgentId()}); err != nil {
			return nil, err
		}
		return &livekit.UpdateAgentResponse{Success: true}, nil
	}
	if err := a.updateSecrets(ctx, req.GetAgentId(), req.GetSecrets(), nil); err != nil {
		return nil, err
	}
	return &livekit.UpdateAgentResponse{Success: true}, nil
}

func (a *userAuthAgentAPI) updateSecrets(ctx context.Context, agentID string, set []*livekit.AgentSecret, remove []string) error {
	err := a.client.UpdateAgentSecrets(ctx, a.projectID, agentID, set, remove)
	if violations := public.FieldViolations(err); len(violations) > 0 {
		return secretsError(err, violations, set, remove)
	}
	return agentServiceError(err, projectAdmin)
}

// secretsError is err, AgentService's refusal of the secrets in set and
// remove, with a line for each of its violations that names the secret by
// name, not by its index in the request.
func secretsError(err error, violations []public.FieldViolation, set []*livekit.AgentSecret, remove []string) error {
	var b strings.Builder
	b.WriteString("invalid secrets:")
	for _, v := range violations {
		b.WriteString("\n  ")
		b.WriteString(secretViolation(v, set, remove))
	}
	return &invalidSecretsError{msg: b.String(), err: err}
}

// invalidSecretsError reads as its lines, and unwraps to the Public API's
// refusal.
type invalidSecretsError struct {
	msg string
	err error
}

func (e *invalidSecretsError) Error() string { return e.msg }
func (e *invalidSecretsError) Unwrap() error { return e.err }

// secretFieldPattern matches a violation's field: set[i] or remove[i], then
// the field of the secret in set, if any.
var secretFieldPattern = regexp.MustCompile(`^(set|remove)\[(\d+)\](?:\.(\w+))?$`)

// secretViolation words v as "secret NAME: FIELD DESCRIPTION", such as
// "secret LIVEKIT_X: name is reserved: ...", or, for a field it doesn't
// know, as the API names it.
func secretViolation(v public.FieldViolation, set []*livekit.AgentSecret, remove []string) string {
	asSent := v.Field + ": " + v.Description
	m := secretFieldPattern.FindStringSubmatch(v.Field)
	if m == nil {
		return asSent
	}
	i, err := strconv.Atoi(m[2])
	if err != nil {
		return asSent
	}
	switch {
	case m[1] == "set" && m[3] != "" && i < len(set):
		return fmt.Sprintf("secret %s: %s %s", set[i].GetName(), m[3], v.Description)
	case m[1] == "remove" && m[3] == "" && i < len(remove):
		return fmt.Sprintf("secret %s to remove: name %s", remove[i], v.Description)
	}
	return asSent
}

// RestartAgent restarts the agent through AgentService, which a WRITE member
// may call.
func (a *userAuthAgentAPI) RestartAgent(ctx context.Context, req *livekit.RestartAgentRequest) (*livekit.RestartAgentResponse, error) {
	if err := a.client.RestartAgent(ctx, a.projectID, req.GetAgentId()); err != nil {
		return nil, agentServiceError(err, projectWriter)
	}
	return &livekit.RestartAgentResponse{Success: true}, nil
}

// RollbackAgent rolls the agent's production deployment back to req.Version
// through AgentService, which a WRITE member may call.
func (a *userAuthAgentAPI) RollbackAgent(ctx context.Context, req *livekit.RollbackAgentRequest) (*livekit.RollbackAgentResponse, error) {
	if err := a.client.RollbackAgent(ctx, a.projectID, req.GetAgentId(), req.GetVersion()); err != nil {
		return nil, agentWriteError(err, req.GetAgentId(), projectWriter)
	}
	return &livekit.RollbackAgentResponse{Success: true}, nil
}

// PromoteAgent promotes the version that srcDeployment runs to production
// through AgentService, which a WRITE member may call. AgentService promotes
// only to production, so a promote to another deployment, which lk doesn't
// make, goes through the bridge.
func (a *userAuthAgentAPI) PromoteAgent(ctx context.Context, agentID, srcDeployment, dstDeployment string) error {
	if deploymentName(dstDeployment) != productionDeployment {
		return bridgeError(a.bridge.PromoteAgent(ctx, agentID, srcDeployment, dstDeployment))
	}
	if _, err := a.client.PromoteAgent(ctx, a.projectID, agentID, srcDeployment); err != nil {
		return agentWriteError(err, agentID, projectWriter)
	}
	return nil
}

// DeleteAgent deletes the agent, or its deployment req.Deployment, through
// AgentService, which lets only a project admin delete one.
func (a *userAuthAgentAPI) DeleteAgent(ctx context.Context, req *livekit.DeleteAgentRequest) (*livekit.DeleteAgentResponse, error) {
	if err := a.client.DeleteAgent(ctx, a.projectID, req.GetAgentId(), req.GetDeployment()); err != nil {
		return nil, agentWriteError(err, req.GetAgentId(), projectAdmin)
	}
	return &livekit.DeleteAgentResponse{Success: true}, nil
}

// agentWriteError is agentServiceError, except that AgentService's refusal
// to roll back, promote or delete an Agent Builder agent says where to do
// it instead.
func agentWriteError(err error, agentID, who string) error {
	if public.IsAgentBuilderAgent(err) {
		return fmt.Errorf("agent %s is an Agent Builder agent: roll it back, promote it or delete it in Agent Builder, "+
			"in the LiveKit Cloud dashboard (%w)", agentID, err)
	}
	return agentServiceError(err, who)
}

// GetClientSettings reads the project's settings for creating and deploying
// an agent through AgentService, which a READ member may call.
func (a *userAuthAgentAPI) GetClientSettings(ctx context.Context, _ *livekit.ClientSettingsRequest) (*livekit.ClientSettingsResponse, error) {
	res, err := a.client.GetAgentClientSettings(ctx, a.projectID)
	return res, cloudAPIError(err)
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

// StreamLogs writes the build log of the version that deployment runs
// through AgentService, which a READ member may call, and needs no
// serverRegion. A runtime log goes through the bridge: the Public API serves
// no runtime log stream.
func (a *userAuthAgentAPI) StreamLogs(ctx context.Context, logType, agentID, deployment string, w io.Writer, serverRegion string) error {
	if logType != buildLogType {
		return bridgeError(a.bridge.StreamLogs(ctx, logType, agentID, deployment, w, serverRegion))
	}
	agent, err := a.client.GetAgent(ctx, a.projectID, agentID)
	if err != nil {
		return cloudAPIError(err)
	}
	version := deployedVersion(agent.Info, deployment)
	if version == "" {
		return fmt.Errorf("no version deployed to the %s deployment of agent %s", deploymentName(deployment), agentID)
	}
	log, err := a.client.GetAgentBuildLog(ctx, a.projectID, agentID, version)
	if err != nil {
		return cloudAPIError(err)
	}
	if _, err := io.WriteString(w, log.Text); err != nil {
		return err
	}
	if log.Text != "" && !strings.HasSuffix(log.Text, "\n") {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if log.Truncated {
		out.Warnf("The build log of version %s is too long to show in full: this is its end.", version)
	}
	return nil
}

// buildLogType is the log type of a build log, as --log-type names it.
const buildLogType = "build"

// deploymentName is deployment, or productionDeployment for "".
func deploymentName(deployment string) string {
	if deployment == "" {
		return productionDeployment
	}
	return deployment
}

// deployedVersion returns the version that deployment ("" for production)
// of the agent info runs, as cloud agents finds the build log it streams
// when lk names no version: the version of the deployment's first region
// that has one, else, for production, the agent's version. It returns ""
// when the deployment runs no version.
func deployedVersion(info *livekit.AgentInfo, deployment string) string {
	name := deploymentName(deployment)
	for _, d := range info.GetAgentDeployments() {
		if deploymentName(d.GetDeployment()) == name && d.GetVersion() != "" {
			return d.GetVersion()
		}
	}
	if name == productionDeployment {
		return info.GetVersion()
	}
	return ""
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
	return fmt.Errorf("signed in with --user-auth, this command needs %s (%w). "+
		"A project API key works meanwhile: re-run without --user-auth", projectAdmin, refused)
}

// projectAdmin and projectWriter are who may make a call that needs the
// ADMIN or the WRITE role on the project, as a refusal names them.
const (
	projectAdmin  = "a project admin"
	projectWriter = "write access to the project"
)

// agentServiceError is err from a call to AgentService, worded as
// cloudAPIError words it, except that the refusal of a member whose role on
// the project is too low says that the command needs who, such as
// projectAdmin, not that the user lacks access to the project. The Public
// API words that refusal "this needs <role or permission> on the project".
// Any other refusal, such as of a suspended project, keeps cloudAPIError's
// words.
func agentServiceError(err error, who string) error {
	var refused *public.APIError
	if errors.As(err, &refused) && public.IsPermissionDenied(refused) && strings.HasPrefix(refused.Message, lacksRolePrefix) {
		return fmt.Errorf("signed in with --user-auth, this command needs %s (%w). "+
			"A project API key also works: re-run without --user-auth", who, refused)
	}
	return cloudAPIError(err)
}

// lacksRolePrefix starts the Public API's refusal of a member whose role on
// the project is too low.
const lacksRolePrefix = "this needs "
