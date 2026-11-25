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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/server-sdk-go/v2/pkg/cloudagents"
)

// The agent commands call cloud agents through server-sdk-go's cloudagents
// client. With an API key it signs each call with the key. Under --user-auth
// the same client goes through the token bridge (see session_auth.go), which
// asks the Public API for a token with the Agent.Admin grant, so only a project
// admin can use it. For the calls that the Public API's CloudAgentService
// serves, --user-auth calls the service instead, which checks the user's role
// on the project itself, as the sip and number commands do (see
// telephony_user_auth.go). Each command takes agentAPI, so its flags, request
// files and output are the same in both modes: the SDK client implements it
// with an API key, and userAuthAgentAPI under --user-auth.
//
// userAuthAgentAPI keeps what only the bridge serves: the live runtime log, and
// the push of a prebuilt image (--image, --image-tar).

// agentAPI is what the agent commands call.
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

	CreatePrivateLink(ctx context.Context, req *livekit.CreatePrivateLinkRequest) (*livekit.CreatePrivateLinkResponse, error)
	ListPrivateLinks(ctx context.Context, req *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error)
	GetPrivateLinkStatus(ctx context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error)
	DestroyPrivateLink(ctx context.Context, req *livekit.DestroyPrivateLinkRequest) (*livekit.DestroyPrivateLinkResponse, error)
}

var (
	_ agentAPI = (*cloudagents.Client)(nil)
	_ agentAPI = (*userAuthAgentAPI)(nil)
)

// agentAPIError is cloudAPIError, and it adds what the user needs to act on a
// refusal: the role that the command needs, and the fields that the API says a
// request got wrong, one to a line.
func agentAPIError(err error) error {
	if err == nil {
		return nil
	}
	msg := cloudAPIError(err).Error()
	if public.IsPermissionDenied(err) {
		msg += agentRoleHint(err.Error())
	}
	violations := public.FieldViolations(err)
	if len(violations) == 0 && msg == err.Error() {
		return cloudAPIError(err)
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, v := range violations {
		fmt.Fprintf(&b, "\n  %s: %s", v.Field, v.Description)
	}
	return errors.New(b.String())
}

// agentRoleHint names the role that a refused command needs, from the
// permission that the API names in its refusal, and says what to leave out of a
// command to run it with a lower role.
func agentRoleHint(refusal string) string {
	switch {
	case strings.Contains(refusal, "agent_secret:manage"):
		return secretsHint
	case strings.Contains(refusal, "agent:delete"), strings.Contains(refusal, "needs ADMIN"):
		return "\n  This command needs a project admin."
	case strings.Contains(refusal, "agent:manage"), strings.Contains(refusal, "agent:deploy"), strings.Contains(refusal, "needs WRITE"):
		return "\n  This command needs write access to the project."
	}
	return ""
}

// secretsHint is what a user learns when a command that sets secrets is refused.
const secretsHint = "\n  Listing or setting secrets needs a project admin. To go on without secrets, leave them out of the command and out of the .env file."

// withSecretsHint adds secretsHint to a refusal of a command that set secrets,
// whatever words the refusal uses for the role: a server that decides by role
// names an ADMIN, not a permission.
func withSecretsHint(err error, secrets []*livekit.AgentSecret) error {
	if err == nil || len(secrets) == 0 || !strings.Contains(err.Error(), "this needs ") {
		return err
	}
	if strings.Contains(err.Error(), secretsHint) {
		return err
	}
	return errors.New(err.Error() + secretsHint)
}

// bridgeError adds to a refusal of the token bridge, which needs a project
// admin, the role that the user lacks.
func bridgeError(err error) error {
	if err == nil {
		return nil
	}
	if public.IsPermissionDenied(err) || strings.Contains(err.Error(), "this needs ") {
		return fmt.Errorf("%w\n  This command needs a project admin when you sign in with --user-auth", err)
	}
	return err
}

// agentCreatedError is the error of a create that registered the agent and then
// failed, so that the command can save the ID of the agent: a retry would make
// another agent, which counts against the plan.
type agentCreatedError struct {
	agentID string
	err     error
}

func (e *agentCreatedError) Error() string {
	return fmt.Sprintf("created agent %s, but %v", e.agentID, e.err)
}
func (e *agentCreatedError) Unwrap() error { return e.err }

// newAgentAPI returns the agent API for pc: server-sdk-go's client with an API
// key, or userAuthAgentAPI under --user-auth.
func newAgentAPI(cmd *cli.Command, pc *config.ProjectConfig) (agentAPI, error) {
	sdk, err := newAgentsClient(pc)
	if err != nil {
		return nil, err
	}
	if !userAuthEnabled(cmd) {
		return sdk, nil
	}
	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return nil, err
	}
	return &userAuthAgentAPI{client: client, projectID: pc.ProjectId, bridge: sdk}, nil
}

// userAuthAgentAPI is agentAPI under --user-auth, for one project. A call that
// the Public API serves goes to client. The rest go through the token bridge,
// bridge.
type userAuthAgentAPI struct {
	client    *public.Client
	projectID string
	bridge    *cloudagents.Client

	// linkHealth holds the health that the last ListPrivateLinks read for each
	// link, so that the status call of the list command for the same link
	// makes no request. A status call takes the entry.
	linkMu     sync.Mutex
	linkHealth map[string]*livekit.PrivateLinkStatus
}

func (a *userAuthAgentAPI) ListAgents(ctx context.Context, req *livekit.ListAgentsRequest) (*livekit.ListAgentsResponse, error) {
	var ids []string
	if id := req.GetAgentId(); id != "" {
		ids = []string{id}
	}
	agents, err := a.client.ListCloudAgents(ctx, a.projectID, ids)
	if err != nil {
		return nil, agentAPIError(err)
	}
	infos := make([]*livekit.AgentInfo, len(agents))
	for i, agent := range agents {
		infos[i] = cloudAgentsInfo(agent.Info)
	}
	return &livekit.ListAgentsResponse{Agents: infos}, nil
}

// noVersion is what cloud agents lists for an agent, or a deployment, that no
// version was deployed to. The Public API leaves the version out, and the
// commands print and compare this value.
const noVersion = "---"

// cloudAgentsInfo puts back the values that cloud agents lists for a value it
// does not know and that the Public API leaves out: the version "---" and the
// zero deploy time, which the commands sort and print. The agent name cannot
// be put back, because cloud agents lists "<Pending>" for 5 minutes after the
// agent is created and "<Unavailable>" while its endpoint is down, and the
// Public API gives neither: under --user-auth the name of such an agent is
// empty.
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
	versions, err := a.client.ListCloudAgentVersions(ctx, a.projectID, req.GetAgentId())
	if err != nil {
		return nil, agentAPIError(err)
	}
	for _, v := range versions {
		// The commands print a version that was never deployed as an empty
		// value, from a zero time.
		if v.DeployedAt == nil {
			v.DeployedAt = timestamppb.New(time.Time{})
		}
	}
	return &livekit.ListAgentVersionsResponse{Versions: versions}, nil
}

func (a *userAuthAgentAPI) ListAgentSecrets(ctx context.Context, req *livekit.ListAgentSecretsRequest) (*livekit.ListAgentSecretsResponse, error) {
	secrets, err := a.client.ListCloudAgentSecrets(ctx, a.projectID, req.GetAgentId())
	if err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.ListAgentSecretsResponse{Secrets: secrets}, nil
}

// UpdateAgentSecrets sets and removes secrets through the Public API, which
// never deletes a secret that a request does not name. An overwrite, which
// deletes every other secret, goes through the bridge.
func (a *userAuthAgentAPI) UpdateAgentSecrets(ctx context.Context, req *livekit.UpdateAgentSecretsRequest) (*livekit.UpdateAgentSecretsResponse, error) {
	if req.GetOverwrite() {
		res, err := a.bridge.UpdateAgentSecrets(ctx, req)
		return res, bridgeError(err)
	}
	err := a.client.UpdateCloudAgentSecrets(ctx, a.projectID, req.GetAgentId(), req.GetSecrets(), req.GetRemove())
	if err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.UpdateAgentSecretsResponse{Success: true}, nil
}

// UpdateAgent sets the secrets of the request, or restarts the agent when it
// has none. Both roll the agent, as the update of cloud agents does. The Public
// API has no call to change the regions of an agent, and cloud agents ignores
// them in an update.
func (a *userAuthAgentAPI) UpdateAgent(ctx context.Context, req *livekit.UpdateAgentRequest) (*livekit.UpdateAgentResponse, error) {
	if len(req.GetSecrets()) > 0 {
		if _, err := a.UpdateAgentSecrets(ctx, &livekit.UpdateAgentSecretsRequest{AgentId: req.GetAgentId(), Secrets: req.GetSecrets()}); err != nil {
			return nil, err
		}
		return &livekit.UpdateAgentResponse{Success: true}, nil
	}
	if _, err := a.RestartAgent(ctx, &livekit.RestartAgentRequest{AgentId: req.GetAgentId()}); err != nil {
		return nil, err
	}
	return &livekit.UpdateAgentResponse{Success: true}, nil
}

func (a *userAuthAgentAPI) RestartAgent(ctx context.Context, req *livekit.RestartAgentRequest) (*livekit.RestartAgentResponse, error) {
	if err := a.client.RestartCloudAgent(ctx, a.projectID, req.GetAgentId()); err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.RestartAgentResponse{Success: true}, nil
}

func (a *userAuthAgentAPI) RollbackAgent(ctx context.Context, req *livekit.RollbackAgentRequest) (*livekit.RollbackAgentResponse, error) {
	if err := a.client.RollbackCloudAgent(ctx, a.projectID, req.GetAgentId(), req.GetVersion()); err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.RollbackAgentResponse{Success: true}, nil
}

// PromoteAgent promotes the version that srcDeployment runs to production.
// The Public API promotes only to production, which is what lk asks for.
func (a *userAuthAgentAPI) PromoteAgent(ctx context.Context, agentID, srcDeployment, dstDeployment string) error {
	if dstDeployment != "" {
		return errors.New("the Public API promotes only to production")
	}
	return agentAPIError(a.client.PromoteCloudAgent(ctx, a.projectID, agentID, srcDeployment))
}

func (a *userAuthAgentAPI) DeleteAgent(ctx context.Context, req *livekit.DeleteAgentRequest) (*livekit.DeleteAgentResponse, error) {
	if err := a.client.DeleteCloudAgent(ctx, a.projectID, req.GetAgentId(), req.GetDeployment()); err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.DeleteAgentResponse{Success: true}, nil
}

func (a *userAuthAgentAPI) GetClientSettings(ctx context.Context, _ *livekit.ClientSettingsRequest) (*livekit.ClientSettingsResponse, error) {
	params, err := a.client.GetCloudAgentSettings(ctx, a.projectID)
	if err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.ClientSettingsResponse{Params: params}, nil
}

// CreateAgent registers the agent in its region, then deploys source to
// production. The Public API creates an agent in one region, which is what the
// create command resolves.
func (a *userAuthAgentAPI) CreateAgent(ctx context.Context, source fs.FS, secrets []*livekit.AgentSecret, regions []string, attributes map[string]string, excludeFiles []string, buildLog io.Writer) (*livekit.CreateAgentResponse, error) {
	if len(regions) != 1 {
		return nil, errors.New("an agent is created in one region")
	}
	agentID, err := a.client.CreateCloudAgent(ctx, a.projectID, regions[0], secrets)
	if err != nil {
		return nil, withSecretsHint(agentAPIError(err), secrets)
	}
	// The secrets went with the registration.
	if err := a.deploy(ctx, agentID, source, nil, attributes, "", excludeFiles, buildLog); err != nil {
		return nil, &agentCreatedError{agentID: agentID, err: err}
	}
	return &livekit.CreateAgentResponse{AgentId: agentID, ServerRegions: a.serverRegions(ctx, agentID)}, nil
}

// serverRegions returns the servers that run the agent, which the log tail
// after a create reads. It returns none when the agent can't be read, and the
// command then leaves the tail out: the region that the user chose names a
// LiveKit region, not a server.
func (a *userAuthAgentAPI) serverRegions(ctx context.Context, agentID string) []string {
	agents, err := a.client.ListCloudAgents(ctx, a.projectID, []string{agentID})
	if err != nil || len(agents) == 0 {
		return nil
	}
	var out []string
	for _, d := range agents[0].Info.GetAgentDeployments() {
		if r := d.GetServerRegion(); r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func (a *userAuthAgentAPI) DeployAgentV2(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, buildLog io.Writer) error {
	return a.deploy(ctx, agentID, source, secrets, attributes, deployment, excludeFiles, buildLog)
}

// buildPollInterval is how often a deploy reads the status of its build.
var buildPollInterval = 5 * time.Second

// maxPollFailures is how many reads of the versions in a row may fail before a
// deploy stops following its build. The build goes on without lk.
const maxPollFailures = 5

// errInterruptedBeforeBuild is the error of a deploy that lk stopped after the
// upload and before it heard that the build started. It does not wrap context.Canceled, which
// the commands read as a build that goes on in the cloud: none does.
var errInterruptedBeforeBuild = errors.New("interrupted before the build was confirmed to start; run `lk agent status` to see whether it is building")

// deploy runs a deploy from source as the Public API splits it. The API never
// holds the source or a build open: it returns where to upload, lk uploads,
// the API starts the build and returns once it is queued, and lk reads the
// versions of the agent until the build ends. The source is a tarball with the
// excludes that an API-key deploy uses.
func (a *userAuthAgentAPI) deploy(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, w io.Writer) error {
	d, err := a.client.DeployCloudAgent(ctx, a.projectID, agentID, deployment, secrets)
	if err != nil {
		return withSecretsHint(agentAPIError(err), secrets)
	}
	var tarball bytes.Buffer
	if err := cloudagents.CreateSourceTarball(source, excludeFiles, &tarball); err != nil {
		return err
	}
	if err := cloudagents.MultipartUpload(d.UploadURL, d.UploadFields, &tarball); err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	if ctx.Err() != nil {
		return errInterruptedBeforeBuild
	}
	if err := a.client.BuildCloudAgent(ctx, a.projectID, agentID, d.Tag, deployment, attributes); err != nil {
		if ctx.Err() != nil {
			return errInterruptedBeforeBuild
		}
		return agentAPIError(err)
	}
	fmt.Fprintln(w, "Build queued; waiting for it to finish...")
	return a.followBuild(ctx, agentID, deployment, d.Tag, w)
}

// followBuild reads the versions of the agent until the version with tag ends
// its build, and prints each new status of the version. A read that fails is
// tried again, up to maxPollFailures in a row: then the build goes on in the
// cloud and the user reads its end with `lk agent status`.
func (a *userAuthAgentAPI) followBuild(ctx context.Context, agentID, deployment, tag string, w io.Writer) error {
	var (
		failures int
		last     string
		lastErr  error
	)
	for {
		versions, err := a.client.ListCloudAgentVersions(ctx, a.projectID, agentID)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			lastErr = err
			failures++
			if failures >= maxPollFailures {
				return fmt.Errorf("can't follow the build (%w). The build goes on; run `lk agent status` to read its end", agentAPIError(lastErr))
			}
		default:
			failures = 0
			for _, v := range versions {
				if v.GetVersion() != tag {
					continue
				}
				status := v.GetStatus()
				if status != last {
					last = status
					fmt.Fprintf(w, "Status: %s\n", status)
				}
				switch strings.ToLower(status) {
				case "available":
					a.writeBuildLog(ctx, agentID, deployment, tag, w)
					return nil
				case "failed":
					a.writeBuildLog(ctx, agentID, deployment, tag, w)
					return errors.New("the build failed; its build log is above")
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(buildPollInterval):
		}
	}
}

// writeBuildLog writes the build log of the version to w. The log is complete
// only once the build ends, so a build that is still running has none. A log
// that can't be read is reported and left out: the outcome of the build is
// already known.
func (a *userAuthAgentAPI) writeBuildLog(ctx context.Context, agentID, deployment, version string, w io.Writer) {
	log, err := a.client.GetCloudAgentBuildLog(ctx, a.projectID, agentID, deployment, version)
	if err != nil {
		fmt.Fprintf(w, "[can't read the build log: %v. Run `lk agent logs --log-type build`]\n", agentAPIError(err))
		return
	}
	fmt.Fprint(w, log.Text)
	if log.Truncated {
		fmt.Fprintln(w, "\n[the build log is longer than the API returns; this is its end]")
	}
}

// RegisterAgent, GetPushTarget and NewRegistryTransport go through the bridge:
// the push of an image is a long registry exchange that the Public API doesn't
// serve.
func (a *userAuthAgentAPI) RegisterAgent(ctx context.Context, secrets []*livekit.AgentSecret, regions []string) (string, error) {
	id, err := a.bridge.RegisterAgent(ctx, secrets, regions)
	return id, bridgeError(err)
}

func (a *userAuthAgentAPI) GetPushTarget(ctx context.Context, agentID string) (*cloudagents.PushTarget, error) {
	target, err := a.bridge.GetPushTarget(ctx, agentID)
	return target, bridgeError(err)
}

func (a *userAuthAgentAPI) NewRegistryTransport(attributes map[string]string) http.RoundTripper {
	return a.bridge.NewRegistryTransport(attributes)
}

// StreamLogs writes the build log through the Public API. The runtime log is a
// live stream from the agent's pods, which the Public API doesn't serve, so it
// goes through the bridge.
func (a *userAuthAgentAPI) StreamLogs(ctx context.Context, logType, agentID, deployment string, w io.Writer, serverRegion string) error {
	if logType != "build" {
		return bridgeError(a.bridge.StreamLogs(ctx, logType, agentID, deployment, w, serverRegion))
	}
	log, err := a.client.GetCloudAgentBuildLog(ctx, a.projectID, agentID, deployment, "")
	if err != nil {
		return agentAPIError(err)
	}
	if _, err := io.WriteString(w, log.Text); err != nil {
		return err
	}
	if log.Truncated {
		_, err = fmt.Fprintln(w, "\n[the build log is longer than the API returns; this is its end]")
	}
	return err
}

func (a *userAuthAgentAPI) CreatePrivateLink(ctx context.Context, req *livekit.CreatePrivateLinkRequest) (*livekit.CreatePrivateLinkResponse, error) {
	link, err := a.client.CreateCloudAgentPrivateLink(ctx, a.projectID, req)
	if err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.CreatePrivateLinkResponse{PrivateLink: link}, nil
}

// ListPrivateLinks reads the links with their health in one call. The status
// call that follows for each link answers from that health, so a list is one
// request, not one plus one for each link.
func (a *userAuthAgentAPI) ListPrivateLinks(ctx context.Context, _ *livekit.ListPrivateLinksRequest) (*livekit.ListPrivateLinksResponse, error) {
	links, err := a.client.ListCloudAgentPrivateLinks(ctx, a.projectID)
	if err != nil {
		return nil, agentAPIError(err)
	}
	items := make([]*livekit.PrivateLink, len(links))
	health := make(map[string]*livekit.PrivateLinkStatus, len(links))
	for i, l := range links {
		items[i] = l.Link
		if l.Status != nil {
			health[l.Link.GetPrivateLinkId()] = l.Status
		}
	}
	a.linkMu.Lock()
	a.linkHealth = health
	a.linkMu.Unlock()
	return &livekit.ListPrivateLinksResponse{Items: items}, nil
}

// GetPrivateLinkStatus returns the health that the last list read for the
// link, once, or reads it. A link whose health the list could not read is read
// again, so its error reaches the user.
func (a *userAuthAgentAPI) GetPrivateLinkStatus(ctx context.Context, req *livekit.GetPrivateLinkStatusRequest) (*livekit.GetPrivateLinkStatusResponse, error) {
	a.linkMu.Lock()
	status, ok := a.linkHealth[req.GetPrivateLinkId()]
	delete(a.linkHealth, req.GetPrivateLinkId())
	a.linkMu.Unlock()
	if ok {
		return &livekit.GetPrivateLinkStatusResponse{Value: status}, nil
	}
	status, err := a.client.GetCloudAgentPrivateLinkStatus(ctx, a.projectID, req.GetPrivateLinkId())
	if err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.GetPrivateLinkStatusResponse{Value: status}, nil
}

func (a *userAuthAgentAPI) DestroyPrivateLink(ctx context.Context, req *livekit.DestroyPrivateLinkRequest) (*livekit.DestroyPrivateLinkResponse, error) {
	if err := a.client.DeleteCloudAgentPrivateLink(ctx, a.projectID, req.GetPrivateLinkId()); err != nil {
		return nil, agentAPIError(err)
	}
	return &livekit.DestroyPrivateLinkResponse{}, nil
}
