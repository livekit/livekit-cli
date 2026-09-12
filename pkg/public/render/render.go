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

// Package render presents the LiveKit Public API's generated types as tables or
// JSON for the CLI. It maps each oapi type to its columns and delegates the
// table/JSON mechanics to pkg/util; commands call these functions with the
// shared Printer and the --json flag value, keeping presentation out of the
// command layer.
package render

import (
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	memberHeaders     = []string{"User ID", "Email", "Role"}
	inviteHeaders     = []string{"Email", "Role", "Expires", "Token"}
	userHeaders       = []string{"User ID", "Email", "Created"}
	workspaceHeaders  = []string{"ID", "Name", "Organization", "Created"}
	simulationHeaders = []string{"ID", "Agent", "Status", "Mode", "Jobs", "Passed", "Failed", "Created"}
	sessionHeaders    = []string{"Session ID", "Room", "Status", "Participants", "Started", "Ended"}
)

func workspaceRow(w oapi.LivekitPublicapiWorkspacesV1Workspace) []string {
	return []string{util.DashString(w.Id), util.DashString(w.Name), util.DashString(w.OrganizationId), util.FormatTime(w.CreatedAt)}
}

func projectMemberRow(m oapi.LivekitPublicapiProjectsV1ProjectMember) []string {
	return []string{util.DashString(m.UserId), util.DashString(m.Email), public.RoleName(m.Role)}
}

func workspaceMemberRow(m oapi.LivekitPublicapiWorkspacesV1WorkspaceMember) []string {
	return []string{util.DashString(m.UserId), util.DashString(m.Email), public.RoleName(m.Role)}
}

func projectInviteRow(i oapi.LivekitPublicapiProjectsV1ProjectInvite) []string {
	return []string{util.DashString(i.Email), public.RoleName(i.Role), util.FormatTime(i.ExpiresAt), util.DashString(i.InviteToken)}
}

func workspaceInviteRow(i oapi.LivekitPublicapiWorkspacesV1WorkspaceInvite) []string {
	return []string{util.DashString(i.Email), public.RoleName(i.Role), util.FormatTime(i.ExpiresAt), util.DashString(i.InviteToken)}
}

func userRow(u oapi.LivekitPublicapiUsersV1User) []string {
	return []string{util.DashString(u.Id), util.DashString(u.Email), util.FormatTime(u.CreatedAt)}
}

func simulationRow(r oapi.LivekitSimulationRun) []string {
	return []string{
		util.DashString(r.Id), util.DashString(r.AgentName), util.DerefEnum(r.Status), util.DerefEnum(r.Mode),
		util.DashInt32(r.JobCount), util.DashInt32(r.PassedCount), util.DashInt32(r.FailedCount), util.FormatTime(r.CreatedAt),
	}
}

func sessionRow(s oapi.LivekitPublicapiAnalyticsV1Session) []string {
	return []string{
		util.DashString(s.SessionId), util.DashString(s.RoomName), util.DerefEnum(s.Status), util.DashInt32(s.NumParticipants),
		util.FormatTime(s.StartedAt), util.FormatTime(s.EndedAt),
	}
}

// Workspaces prints a list of workspaces.
func Workspaces(p *util.Printer, asJSON bool, ws []oapi.LivekitPublicapiWorkspacesV1Workspace) error {
	return util.RenderList(p, asJSON, ws, "No workspaces found.", workspaceHeaders, workspaceRow)
}

// Workspace prints a single workspace.
func Workspace(p *util.Printer, asJSON bool, w oapi.LivekitPublicapiWorkspacesV1Workspace) error {
	return util.RenderOne(p, asJSON, w, workspaceHeaders, workspaceRow)
}

// ProjectMembers prints a list of project members.
func ProjectMembers(p *util.Printer, asJSON bool, members []oapi.LivekitPublicapiProjectsV1ProjectMember) error {
	return util.RenderList(p, asJSON, members, "No members found.", memberHeaders, projectMemberRow)
}

// ProjectMember prints a single project member.
func ProjectMember(p *util.Printer, asJSON bool, m oapi.LivekitPublicapiProjectsV1ProjectMember) error {
	return util.RenderOne(p, asJSON, m, memberHeaders, projectMemberRow)
}

// WorkspaceMembers prints a list of workspace members.
func WorkspaceMembers(p *util.Printer, asJSON bool, members []oapi.LivekitPublicapiWorkspacesV1WorkspaceMember) error {
	return util.RenderList(p, asJSON, members, "No members found.", memberHeaders, workspaceMemberRow)
}

// WorkspaceMember prints a single workspace member.
func WorkspaceMember(p *util.Printer, asJSON bool, m oapi.LivekitPublicapiWorkspacesV1WorkspaceMember) error {
	return util.RenderOne(p, asJSON, m, memberHeaders, workspaceMemberRow)
}

// ProjectInvites prints a list of project invites.
func ProjectInvites(p *util.Printer, asJSON bool, invites []oapi.LivekitPublicapiProjectsV1ProjectInvite) error {
	return util.RenderList(p, asJSON, invites, "No pending invites.", inviteHeaders, projectInviteRow)
}

// ProjectInvite prints a single project invite.
func ProjectInvite(p *util.Printer, asJSON bool, i oapi.LivekitPublicapiProjectsV1ProjectInvite) error {
	return util.RenderOne(p, asJSON, i, inviteHeaders, projectInviteRow)
}

// WorkspaceInvites prints a list of workspace invites.
func WorkspaceInvites(p *util.Printer, asJSON bool, invites []oapi.LivekitPublicapiWorkspacesV1WorkspaceInvite) error {
	return util.RenderList(p, asJSON, invites, "No pending invites.", inviteHeaders, workspaceInviteRow)
}

// WorkspaceInvite prints a single workspace invite.
func WorkspaceInvite(p *util.Printer, asJSON bool, i oapi.LivekitPublicapiWorkspacesV1WorkspaceInvite) error {
	return util.RenderOne(p, asJSON, i, inviteHeaders, workspaceInviteRow)
}

// Users prints a list of users.
func Users(p *util.Printer, asJSON bool, users []oapi.LivekitPublicapiUsersV1User) error {
	return util.RenderList(p, asJSON, users, "No users found.", userHeaders, userRow)
}

// User prints a single user.
func User(p *util.Printer, asJSON bool, u oapi.LivekitPublicapiUsersV1User) error {
	return util.RenderOne(p, asJSON, u, userHeaders, userRow)
}

// SimulationRuns prints a list of simulation runs.
func SimulationRuns(p *util.Printer, asJSON bool, runs []oapi.LivekitSimulationRun) error {
	return util.RenderList(p, asJSON, runs, "No simulation runs found.", simulationHeaders, simulationRow)
}

// SimulationRun prints a single simulation run.
func SimulationRun(p *util.Printer, asJSON bool, r oapi.LivekitSimulationRun) error {
	return util.RenderOne(p, asJSON, r, simulationHeaders, simulationRow)
}

// Sessions prints a list of analytics sessions.
func Sessions(p *util.Printer, asJSON bool, sessions []oapi.LivekitPublicapiAnalyticsV1Session) error {
	return util.RenderList(p, asJSON, sessions, "No sessions found", sessionHeaders, sessionRow)
}

// SessionsPage prints a cursor-paginated page of analytics sessions.
func SessionsPage(p *util.Printer, asJSON bool, sessions []oapi.LivekitPublicapiAnalyticsV1Session, nextCursor string) error {
	return util.RenderPage(p, asJSON, sessions, nextCursor, "No sessions found", sessionHeaders, sessionRow)
}

// WorkspacesPage prints a cursor-paginated page of workspaces.
func WorkspacesPage(p *util.Printer, asJSON bool, ws []oapi.LivekitPublicapiWorkspacesV1Workspace, nextCursor string) error {
	return util.RenderPage(p, asJSON, ws, nextCursor, "No workspaces found.", workspaceHeaders, workspaceRow)
}

// UsersPage prints a cursor-paginated page of users.
func UsersPage(p *util.Printer, asJSON bool, users []oapi.LivekitPublicapiUsersV1User, nextCursor string) error {
	return util.RenderPage(p, asJSON, users, nextCursor, "No users found.", userHeaders, userRow)
}

// SimulationRunsPage prints a token-paginated page of simulation runs (the
// nextCursor is the run list's next page token).
func SimulationRunsPage(p *util.Printer, asJSON bool, runs []oapi.LivekitSimulationRun, nextCursor string) error {
	return util.RenderPage(p, asJSON, runs, nextCursor, "No simulation runs found.", simulationHeaders, simulationRow)
}

// Session prints a single analytics session.
func Session(p *util.Printer, asJSON bool, s oapi.LivekitPublicapiAnalyticsV1Session) error {
	return util.RenderOne(p, asJSON, s, sessionHeaders, sessionRow)
}
