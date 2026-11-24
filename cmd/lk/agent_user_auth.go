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
	"net"
	"net/http"
	"regexp"
	"slices"
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
// rollback, promote, delete and secret updates, and create and deploy from
// source. It sends every other call, such as a prebuilt image push, a
// runtime log and private links, through the bridge for now. A method moves
// to AgentService by calling a.client in place of a.bridge.

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

// UpdateAgent merges req.Secrets into the agent's secrets through
// AgentService's UpdateAgentSecrets, which a project admin may call, or,
// with no secrets, restarts the agent through AgentService's RestartAgent,
// which a WRITE member may call. Either rolls the agent, as cloud agents'
// UpdateAgent does with an API key, but neither is that call: cloud agents'
// UpdateAgent also saves the agent and marks its production deployment
// Updating, with secrets or without. lk sends no regions, which cloud agents
// ignores.
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
	return &rewordedError{msg: b.String(), err: err}
}

// rewordedError reads as msg, and unwraps to err, the Public API's refusal.
type rewordedError struct {
	msg string
	err error
}

func (e *rewordedError) Error() string { return e.msg }
func (e *rewordedError) Unwrap() error { return e.err }

// secretFieldPattern matches a violation's field: set[i] or secrets[i] (a
// secret to set), or remove[i], then the field of the secret to set, if any.
var secretFieldPattern = regexp.MustCompile(`^(set|secrets|remove)\[(\d+)\](?:\.(\w+))?$`)

// secretViolation words v as "secret NAME: FIELD DESCRIPTION", such as
// "secret 1KEY: name must have only letters, ...", or, for a field it
// doesn't know, as the API names it.
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
	case m[1] != "remove" && m[3] != "" && i < len(set):
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

// CreateAgent creates the agent in regions[0] through AgentService, then
// deploys source to its production deployment as DeployAgentV2 does. A WRITE
// member may create an agent; with secrets, AgentService needs a project
// admin. The response has the agent's ID, and the server regions that lk
// streams its runtime log from.
func (a *userAuthAgentAPI) CreateAgent(ctx context.Context, source fs.FS, secrets []*livekit.AgentSecret, regions []string, attributes map[string]string, excludeFiles []string, buildLog io.Writer) (*livekit.CreateAgentResponse, error) {
	if len(regions) != 1 {
		return nil, fmt.Errorf("an agent is created in one region, not %d", len(regions))
	}
	agentID, err := a.client.CreateAgent(ctx, a.projectID, regions[0], secrets)
	if err != nil {
		return nil, deployError(err, "", secrets)
	}
	info, err := a.deploy(ctx, agentID, source, nil, attributes, "", excludeFiles, buildLog)
	if err != nil {
		return nil, fmt.Errorf("created agent %s, but %w", agentID, err)
	}
	return &livekit.CreateAgentResponse{AgentId: agentID, ServerRegions: serverRegions(info, "")}, nil
}

// DeployAgentV2 deploys source to the agent's deployment, production when
// "", through AgentService, which a WRITE member may call; with secrets, it
// needs a project admin.
func (a *userAuthAgentAPI) DeployAgentV2(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, buildLog io.Writer) error {
	_, err := a.deploy(ctx, agentID, source, secrets, attributes, deployment, excludeFiles, buildLog)
	return err
}

// deploy deploys source to deployment of the agent agentID, as cloud
// agents' DeployAgentV2 does: it sets secrets and starts the deploy with
// DeployAgent, uploads source as a tarball without excludeFiles, starts the
// build with StartAgentBuild, and follows it with awaitBuild. It returns the
// agent once the build succeeds.
func (a *userAuthAgentAPI) deploy(ctx context.Context, agentID string, source fs.FS, secrets []*livekit.AgentSecret, attributes map[string]string, deployment string, excludeFiles []string, w io.Writer) (*livekit.AgentInfo, error) {
	d, err := a.client.DeployAgent(ctx, a.projectID, agentID, deployment, secrets)
	if err != nil {
		return nil, deployError(err, agentID, secrets)
	}
	var tarball bytes.Buffer
	if err := cloudagents.CreateSourceTarball(source, excludeFiles, &tarball); err != nil {
		return nil, fmt.Errorf("failed to sanitize source: %w", err)
	}
	if err := cloudagents.MultipartUpload(d.UploadURL, d.UploadFields, &tarball); err != nil {
		return nil, fmt.Errorf("multipart upload failed: %w", err)
	}
	// lk reads a canceled ctx as a build that continues on LiveKit Cloud,
	// which isn't so before StartAgentBuild. DeployAgent has already set the
	// secrets, and cloud agents shows production as Building from then.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("interrupted before the build started: no version was built or deployed, "+
			"but the secrets this deploy sent are set, and the agent's status may show Building (%v)", err)
	}
	if err := a.client.StartAgentBuild(ctx, a.projectID, agentID, deployment, d.Tag, attributes); err != nil {
		return nil, agentServiceError(err, projectWriter)
	}
	return a.awaitBuild(ctx, agentID, deployment, d.Tag, w)
}

// buildPollInterval is how often awaitBuild reads the status of a build.
var buildPollInterval = 5 * time.Second

// The statuses of a version that cloud agents lists once its build ends.
const (
	versionAvailable = "Available"
	versionFailed    = "Failed"
)

// buildFailed is the status of a deployment whose build failed, as cloud
// agents lists it.
const buildFailed = "Build Failed"

// buildingStatuses are the statuses that cloud agents lists for a
// deployment while it sets up, builds or schedules a version.
var buildingStatuses = []string{"Setting Up", "Building", "Scheduling"}

// buildEndGrace is how long awaitBuild waits for the version to be listed
// once the deployment has left buildingStatuses, before it decides that the
// build ended without making the version.
var buildEndGrace = time.Minute

// statusName is status, or "none" for "".
func statusName(status string) string {
	if status == "" {
		return "none"
	}
	return status
}

// awaitBuild follows the build of the version tag that StartAgentBuild
// started for deployment of the agent agentID. Every buildPollInterval it
// reads the agent and its versions, and writes each new status of the
// deployment to w. A read that fails transiently is made again a few times
// (see retryPoll) before awaitBuild gives up on following the build. Once
// the version is available or failed, it writes the version's build log to
// w, and returns the agent, or an error when the build failed. When the
// deployment leaves the building statuses and the version is still not
// listed buildEndGrace later, the build ended without making the version,
// and awaitBuild returns an error that says so. When ctx is done first, it
// returns ctx's error: the build continues on LiveKit Cloud.
func (a *userAuthAgentAPI) awaitBuild(ctx context.Context, agentID, deployment, tag string, w io.Writer) (*livekit.AgentInfo, error) {
	fmt.Fprintf(w, "Building version %s. This deploy continues even if you disconnect.\n", tag)
	ticker := time.NewTicker(buildPollInterval)
	defer ticker.Stop()
	last := ""
	sawBuilding := false
	var endedAt time.Time // when the deployment left the building statuses with the version not listed
	for {
		agent, err := retryPoll(ctx, func(ctx context.Context) (*public.Agent, error) {
			return a.client.GetAgent(ctx, a.projectID, agentID)
		})
		if err != nil {
			return nil, lostBuildError(tag, err)
		}
		status := deploymentStatus(agent.Info, deployment)
		if status != "" && status != last {
			fmt.Fprintf(w, "Status: %s\n", status)
			last = status
		}
		versions, err := retryPoll(ctx, func(ctx context.Context) ([]*livekit.AgentVersion, error) {
			return a.client.ListAgentVersions(ctx, a.projectID, agentID)
		})
		if err != nil {
			return nil, lostBuildError(tag, err)
		}
		version := versionStatus(versions, tag)
		switch version {
		case versionAvailable:
			if err := a.writeBuildLog(ctx, agentID, tag, w); err != nil {
				out.Warnf("Can't read the build log of version %s (%v): read it later with `lk agent logs --log-type=build`.", tag, err)
			}
			return agent.Info, nil
		case versionFailed:
			return nil, a.buildFailedError(ctx, agentID, tag, w)
		}
		if status == buildFailed {
			return nil, a.buildFailedError(ctx, agentID, tag, w)
		}
		switch {
		case slices.Contains(buildingStatuses, status):
			sawBuilding = true
			endedAt = time.Time{}
		case !sawBuilding || version != "":
		case endedAt.IsZero():
			endedAt = time.Now()
		case time.Since(endedAt) >= buildEndGrace:
			return nil, fmt.Errorf("the build of version %s ended without making it: the deployment is no longer building (status %s), "+
				"and version %s isn't listed. A newer deploy may have replaced this one, or the build stopped. "+
				"Run `lk agent versions` to see the agent's versions, and `lk agent logs --log-type=build` "+
				"for the build log of the version that the deployment runs", tag, statusName(status), tag)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// pollAttempts is how many times awaitBuild makes a read that fails
// transiently before it gives up on following the build.
const pollAttempts = 4

// pollRetryBackoff is how long awaitBuild waits before it makes a read that
// failed transiently again. It doubles for each next attempt.
var pollRetryBackoff = time.Second

// retryPoll returns read(ctx), made again after a backoff while it fails
// transiently (see transientPollError), up to pollAttempts times in all.
func retryPoll[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	backoff := pollRetryBackoff
	for attempt := 1; ; attempt++ {
		v, err := read(ctx)
		if err == nil || attempt == pollAttempts || !transientPollError(err) {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

// transientPollError reports whether err, from a read of the Public API,
// can pass when the read is made again: the API answered 429 or a 5xx, or
// lk got no answer. Any other refusal, such as a 401 or a 403, stays as it
// is, and so does the error of a done ctx.
func transientPollError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var refused *public.APIError
	if errors.As(err, &refused) {
		return refused.Status == http.StatusTooManyRequests || refused.Status >= http.StatusInternalServerError
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// buildFailedError writes the build log of the version tag, whose build
// failed, to w, and returns the error that says so.
func (a *userAuthAgentAPI) buildFailedError(ctx context.Context, agentID, tag string, w io.Writer) error {
	if err := a.writeBuildLog(ctx, agentID, tag, w); err != nil {
		return fmt.Errorf("the build of version %s failed, and its build log can't be read: %w", tag, err)
	}
	return fmt.Errorf("the build of version %s failed: its build log is above", tag)
}

// lostBuildError is err, which stopped awaitBuild from reading the build of
// the version tag, worded to say that the build continues. A done ctx's
// error stays as it is, which lk reads as a disconnect.
func lostBuildError(tag string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("can't follow the build of version %s, which continues on LiveKit Cloud: "+
		"run `lk agent status` to follow it (%w)", tag, cloudAPIError(err))
}

// deploymentStatus is the status of deployment ("" for production) of the
// agent info, as its first region lists it, or "" when it lists none.
func deploymentStatus(info *livekit.AgentInfo, deployment string) string {
	name := deploymentName(deployment)
	for _, d := range info.GetAgentDeployments() {
		if deploymentName(d.GetDeployment()) == name && d.GetStatus() != "" {
			return d.GetStatus()
		}
	}
	return ""
}

// versionStatus is the status of the version named version in versions, as
// versionAvailable or versionFailed spell it when it is one of them, or ""
// when versions doesn't have it.
func versionStatus(versions []*livekit.AgentVersion, version string) string {
	for _, v := range versions {
		if v.GetVersion() != version {
			continue
		}
		for _, s := range []string{versionAvailable, versionFailed} {
			if strings.EqualFold(v.GetStatus(), s) {
				return s
			}
		}
		return v.GetStatus()
	}
	return ""
}

// serverRegions is the server region of each region of deployment ("" for
// production) of the agent info, once each.
func serverRegions(info *livekit.AgentInfo, deployment string) []string {
	var regions []string
	name := deploymentName(deployment)
	for _, d := range info.GetAgentDeployments() {
		if deploymentName(d.GetDeployment()) == name && d.GetServerRegion() != "" && !slices.Contains(regions, d.GetServerRegion()) {
			regions = append(regions, d.GetServerRegion())
		}
	}
	return regions
}

// deployError is err from CreateAgent or DeployAgent, worded as
// agentServiceError words it for a WRITE member, except for refusals of
// secrets: invalid ones get a line each, by name, and a member below admin
// hears that a writer may deploy without secrets. A deploy of agentID, an
// Agent Builder agent, points at Agent Builder.
func deployError(err error, agentID string, secrets []*livekit.AgentSecret) error {
	if violations := public.FieldViolations(err); len(violations) > 0 {
		return secretsError(err, violations, secrets, nil)
	}
	if agentID != "" && public.IsAgentBuilderAgent(err) {
		return fmt.Errorf("agent %s is an Agent Builder agent: deploy it in Agent Builder, in the LiveKit Cloud dashboard (%w)", agentID, err)
	}
	var refused *public.APIError
	if len(secrets) > 0 && errors.As(err, &refused) && public.IsPermissionDenied(refused) && strings.HasPrefix(refused.Message, lacksRolePrefix) {
		return needsError(refused, projectAdmin, "Write access to the project is enough without secrets "+
			"(from --secrets, --secrets-file, --secret-mount or an env file): a project admin can set them with `lk agent update-secrets`. "+
			"A project API key also works: re-run without --user-auth")
	}
	return agentServiceError(err, projectWriter)
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
	return a.writeBuildLog(ctx, agentID, version, w)
}

// writeBuildLog writes the build log of version of the agent agentID to w,
// through AgentService, which a READ member may call.
func (a *userAuthAgentAPI) writeBuildLog(ctx context.Context, agentID, version string, w io.Writer) error {
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
	return needsError(refused, projectAdmin, "A project API key works meanwhile: re-run without --user-auth")
}

// needsError is refused, the Public API's refusal of a member whose role on
// the project is too low, worded as "signed in with --user-auth, this
// command needs <who>", then apiKey. It quotes refused unless refused names
// only the role that who names, such as "this needs ADMIN on the project"
// for projectAdmin; on a server that checks permissions, refused names them.
func needsError(refused *public.APIError, who, apiKey string) error {
	msg := "signed in with --user-auth, this command needs " + who
	if refused.Error() != lacksRolePrefix+roles[who]+" on the project" {
		msg += " (" + refused.Error() + ")"
	}
	return &rewordedError{msg: msg + ". " + apiKey, err: refused}
}

// projectAdmin and projectWriter are who may make a call that needs the
// ADMIN or the WRITE role on the project, as a refusal names them.
const (
	projectAdmin  = "a project admin"
	projectWriter = "write access to the project"
)

// roles maps projectAdmin and projectWriter to the role that the Public API
// names.
var roles = map[string]string{projectAdmin: "ADMIN", projectWriter: "WRITE"}

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
		return needsError(refused, who, "A project API key also works: re-run without --user-auth")
	}
	return cloudAPIError(err)
}

// lacksRolePrefix starts the Public API's refusal of a member whose role on
// the project is too low.
const lacksRolePrefix = "this needs "
