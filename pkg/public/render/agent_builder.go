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

package render

import (
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	agentBuilderAgentHeaders        = []string{"ID", "Name", "Cloud Agent ID", "Status", "Version", "Updated"}
	agentBuilderAgentDetailHeaders  = []string{"ID", "Name", "Cloud Agent ID", "Status", "Version", "Deployed Version", "Updated"}
	agentBuilderAgentVersionHeaders = []string{"Version ID", "Created", "Owner", "Deployed"}
)

// DeploymentStatus is an Agent Builder agent's deployment status without the enum's
// DEPLOYMENT_STATUS_ prefix, such as RUNNING, or a dash when it is unknown.
func DeploymentStatus(d *oapi.LivekitPublicapiAgentbuilderV1Deployment) string {
	if d == nil || d.Status == nil || *d.Status == "" {
		return "-"
	}
	return strings.TrimPrefix(string(*d.Status), "DEPLOYMENT_STATUS_")
}

// agentBuilderAgentRow is an Agent Builder agent's list row. Version is the version that
// holds the current configuration.
func agentBuilderAgentRow(a oapi.LivekitPublicapiAgentbuilderV1Agent) []string {
	return []string{
		util.DashString(a.Id), dashText(a.Name), util.DashString(a.CloudAgentId), DeploymentStatus(a.Deployment),
		util.DashString(a.VersionId), util.FormatTime(a.UpdatedAt),
	}
}

// agentBuilderAgentDetailRow is agentBuilderAgentRow with the version that runs, which
// the API sets on a get but not on a list.
func agentBuilderAgentDetailRow(a oapi.LivekitPublicapiAgentbuilderV1Agent) []string {
	deployed := "-"
	if a.Deployment != nil {
		deployed = util.DashString(a.Deployment.VersionId)
	}
	return []string{
		util.DashString(a.Id), dashText(a.Name), util.DashString(a.CloudAgentId), DeploymentStatus(a.Deployment),
		util.DashString(a.VersionId), deployed, util.FormatTime(a.UpdatedAt),
	}
}

func agentBuilderAgentVersionRow(v public.AgentBuilderAgentVersion) []string {
	return []string{util.DashString(v.Id), util.FormatTime(v.CreatedAt), util.DashString(v.OwnerId), util.FormatTime(v.DeployedAt)}
}

// AgentBuilderAgentsPage prints one page of Agent Builder agents.
func AgentBuilderAgentsPage(p *util.Printer, asJSON bool, agents []oapi.LivekitPublicapiAgentbuilderV1Agent, nextCursor string) error {
	return util.RenderPage(p, asJSON, agents, nextCursor, "No Agent Builder agents found.", agentBuilderAgentHeaders, agentBuilderAgentRow)
}

// AgentBuilderAgent prints an Agent Builder agent: as JSON with its configuration, or as
// a one-row table.
func AgentBuilderAgent(p *util.Printer, asJSON bool, a public.AgentBuilderAgent) error {
	if asJSON {
		return util.PrintJSONTo(p.ResultWriter(), a)
	}
	return util.RenderOne(p, false, a.LivekitPublicapiAgentbuilderV1Agent, agentBuilderAgentDetailHeaders, agentBuilderAgentDetailRow)
}

// AgentBuilderAgentVersionsPage prints one page of an Agent Builder agent's versions. As
// JSON each version has its configuration.
func AgentBuilderAgentVersionsPage(p *util.Printer, asJSON bool, versions []public.AgentBuilderAgentVersion, nextCursor string) error {
	return util.RenderPage(p, asJSON, versions, nextCursor, "No versions found.", agentBuilderAgentVersionHeaders, agentBuilderAgentVersionRow)
}
