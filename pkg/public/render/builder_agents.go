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
	builderAgentHeaders        = []string{"ID", "Name", "Agent ID", "Status", "Version", "Updated"}
	builderAgentDetailHeaders  = []string{"ID", "Name", "Agent ID", "Status", "Version", "Deployed Version", "Updated"}
	builderAgentVersionHeaders = []string{"Version ID", "Created", "Owner", "Deployed"}
)

// DeploymentStatus is a builder agent's deployment status without the enum's
// DEPLOYMENT_STATUS_ prefix, such as RUNNING, or a dash when it is unknown.
func DeploymentStatus(d *oapi.LivekitPublicapiAgentsV1Deployment) string {
	if d == nil || d.Status == nil || *d.Status == "" {
		return "-"
	}
	return strings.TrimPrefix(string(*d.Status), "DEPLOYMENT_STATUS_")
}

// builderAgentRow is a builder agent's list row. Version is the version that
// holds the current configuration.
func builderAgentRow(a oapi.LivekitPublicapiAgentsV1BuilderAgent) []string {
	return []string{
		util.DashString(a.Id), dashText(a.Name), util.DashString(a.AgentId), DeploymentStatus(a.Deployment),
		util.DashString(a.VersionId), util.FormatTime(a.UpdatedAt),
	}
}

// builderAgentDetailRow is builderAgentRow with the version that runs, which
// the API sets on a get but not on a list.
func builderAgentDetailRow(a oapi.LivekitPublicapiAgentsV1BuilderAgent) []string {
	deployed := "-"
	if a.Deployment != nil {
		deployed = util.DashString(a.Deployment.VersionId)
	}
	return []string{
		util.DashString(a.Id), dashText(a.Name), util.DashString(a.AgentId), DeploymentStatus(a.Deployment),
		util.DashString(a.VersionId), deployed, util.FormatTime(a.UpdatedAt),
	}
}

func builderAgentVersionRow(v public.BuilderAgentVersion) []string {
	return []string{util.DashString(v.Id), util.FormatTime(v.CreatedAt), util.DashString(v.OwnerId), util.FormatTime(v.DeployedAt)}
}

// BuilderAgentsPage prints one page of builder agents.
func BuilderAgentsPage(p *util.Printer, asJSON bool, agents []oapi.LivekitPublicapiAgentsV1BuilderAgent, nextCursor string) error {
	return util.RenderPage(p, asJSON, agents, nextCursor, "No builder agents found.", builderAgentHeaders, builderAgentRow)
}

// BuilderAgent prints a builder agent: as JSON with its configuration, or as
// a one-row table.
func BuilderAgent(p *util.Printer, asJSON bool, a public.BuilderAgent) error {
	if asJSON {
		return util.PrintJSONTo(p.ResultWriter(), a)
	}
	return util.RenderOne(p, false, a.LivekitPublicapiAgentsV1BuilderAgent, builderAgentDetailHeaders, builderAgentDetailRow)
}

// BuilderAgentVersionsPage prints one page of a builder agent's versions. As
// JSON each version has its configuration.
func BuilderAgentVersionsPage(p *util.Printer, asJSON bool, versions []public.BuilderAgentVersion, nextCursor string) error {
	return util.RenderPage(p, asJSON, versions, nextCursor, "No versions found.", builderAgentVersionHeaders, builderAgentVersionRow)
}
