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

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// WorkspaceCommands are the Public-API-only workspace commands. The whole group
// is Hidden and requires --experimental-auth (user-based auth). Top-level
// commands take the workspace as a positional (id, name, or alias, resolved
// against the per-user cache, with a picker when omitted); nested project/member/
// invite commands take it via --workspace so the entity id stays positional.
var WorkspaceCommands = []*cli.Command{
	{
		Name:   "workspace",
		Usage:  "Manage LiveKit Cloud workspaces (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List accessible workspaces",
				UsageText: "lk workspace list --experimental-auth",
				Action:    listWorkspaces,
				Flags:     []cli.Flag{limitFlag, cursorFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a workspace by id, name, or alias",
				UsageText: "lk workspace get [WORKSPACE] --experimental-auth",
				ArgsUsage: "[WORKSPACE]",
				Action:    getWorkspace,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Create a workspace",
				UsageText: "lk workspace create NAME [--organization ORG_ID] --experimental-auth",
				ArgsUsage: "NAME",
				Action:    createWorkspace,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "organization", Usage: "Optional organization `ID` to associate"},
					jsonFlag,
				},
			},
			{
				Name:      "update",
				Usage:     "Rename a workspace",
				UsageText: "lk workspace update [WORKSPACE] --name NEW_NAME --experimental-auth",
				ArgsUsage: "[WORKSPACE]",
				Action:    updateWorkspace,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "New workspace `NAME`", Required: true},
					jsonFlag,
				},
			},
			{
				Name:      "delete",
				Usage:     "Delete a workspace",
				UsageText: "lk workspace delete [WORKSPACE] --experimental-auth",
				ArgsUsage: "[WORKSPACE]",
				Action:    deleteWorkspace,
				Flags:     []cli.Flag{jsonFlag},
			},
			workspaceProjectCommand(),
			workspaceMemberCommand(),
			workspaceInviteCommand(),
		},
	},
}

func workspaceProjectCommand() *cli.Command {
	return &cli.Command{
		Name:  "project",
		Usage: "Manage projects within a workspace",
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List projects in a workspace",
				UsageText: "lk workspace project list --workspace WORKSPACE --experimental-auth",
				Action:    listWorkspaceProjects,
				Flags:     []cli.Flag{workspaceFlag, limitFlag, cursorFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a project in a workspace",
				UsageText: "lk workspace project get PROJECT_ID --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "PROJECT_ID",
				Action:    getWorkspaceProject,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Create a project in a workspace",
				UsageText: "lk workspace project create NAME --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "NAME",
				Action:    createWorkspaceProject,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "update",
				Usage:     "Rename a project in a workspace",
				UsageText: "lk workspace project update PROJECT_ID --workspace WORKSPACE --name NAME --experimental-auth",
				ArgsUsage: "PROJECT_ID",
				Action:    updateWorkspaceProject,
				Flags: []cli.Flag{
					workspaceFlag,
					&cli.StringFlag{Name: "name", Usage: "New project `NAME`", Required: true},
					jsonFlag,
				},
			},
			{
				Name:      "delete",
				Usage:     "Delete a project in a workspace",
				UsageText: "lk workspace project delete PROJECT_ID --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "PROJECT_ID",
				Action:    deleteWorkspaceProject,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
		},
	}
}

func workspaceMemberCommand() *cli.Command {
	return &cli.Command{
		Name:  "member",
		Usage: "Manage workspace members",
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List workspace members",
				UsageText: "lk workspace member list --workspace WORKSPACE --experimental-auth",
				Action:    listWorkspaceMembers,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a workspace member by user ID",
				UsageText: "lk workspace member get USER_ID --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    getWorkspaceMember,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "update",
				Usage:     "Change a workspace member's role",
				UsageText: "lk workspace member update USER_ID --role ROLE --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    updateWorkspaceMember,
				Flags:     []cli.Flag{workspaceFlag, roleFlag, jsonFlag},
			},
			{
				Name:      "delete",
				Usage:     "Remove a member from a workspace",
				UsageText: "lk workspace member delete USER_ID --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    deleteWorkspaceMember,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
		},
	}
}

func workspaceInviteCommand() *cli.Command {
	return &cli.Command{
		Name:  "invite",
		Usage: "Manage workspace invites",
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List pending workspace invites",
				UsageText: "lk workspace invite list --workspace WORKSPACE --experimental-auth",
				Action:    listWorkspaceInvites,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a workspace invite by its token",
				UsageText: "lk workspace invite get INVITE_TOKEN --experimental-auth",
				ArgsUsage: "INVITE_TOKEN",
				Action:    getWorkspaceInvite,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Invite a member to a workspace by email",
				UsageText: "lk workspace invite create EMAIL --role ROLE --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "EMAIL",
				Action:    createWorkspaceInvite,
				Flags:     []cli.Flag{workspaceFlag, roleFlag, jsonFlag},
			},
			{
				Name:      "delete",
				Usage:     "Revoke a pending workspace invite",
				UsageText: "lk workspace invite delete EMAIL --workspace WORKSPACE --experimental-auth",
				ArgsUsage: "EMAIL",
				Action:    deleteWorkspaceInvite,
				Flags:     []cli.Flag{workspaceFlag, jsonFlag},
			},
			{
				Name:      "answer",
				Usage:     "Accept or decline a workspace invite",
				UsageText: "lk workspace invite answer INVITE_TOKEN [--decline] --experimental-auth",
				ArgsUsage: "INVITE_TOKEN",
				Action:    answerWorkspaceInvite,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "decline", Usage: "Decline the invite instead of accepting it"},
					jsonFlag,
				},
			},
		},
	}
}

// argN returns the nth positional argument, or an error naming it when missing.
func argN(cmd *cli.Command, n int, name string) (string, error) {
	v := cmd.Args().Get(n)
	if v == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

// resolveWorkspace builds a client and resolves ref (id, name, or alias, or a
// picker when empty) to a workspace id.
func resolveWorkspace(ctx context.Context, cmd *cli.Command, ref string) (*public.Client, string, error) {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return nil, "", err
	}
	wsID, err := resolveWorkspaceRef(ctx, cmd, conf, user, ref)
	if err != nil {
		return nil, "", err
	}
	return client, wsID, nil
}

// workspaceCacheEntries builds per-user cache entries for the given workspaces,
// assigning each a unique alias derived from its name (mirroring
// projectCacheEntries).
func workspaceCacheEntries(workspaces []oapi.LivekitPublicapiWorkspacesV1Workspace) []config.UserWorkspaceConfig {
	used := make(map[string]bool, len(workspaces))
	entries := make([]config.UserWorkspaceConfig, len(workspaces))
	for i, w := range workspaces {
		base := util.Slugify(util.Deref(w.Name))
		alias := base
		for k := 2; alias != "" && used[alias]; k++ {
			alias = fmt.Sprintf("%s-%d", base, k)
		}
		if alias != "" {
			used[alias] = true
		}
		entries[i] = config.UserWorkspaceConfig{
			WorkspaceId:    util.Deref(w.Id),
			Name:           util.Deref(w.Name),
			Alias:          alias,
			OrganizationId: util.Deref(w.OrganizationId),
		}
	}
	return entries
}

func listWorkspaces(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	workspaces, nextCursor, err := client.ListWorkspaces(ctx, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudAPIError(err)
	}
	return render.WorkspacesPage(out, cmd.Bool("json"), workspaces, nextCursor)
}

func getWorkspace(ctx context.Context, cmd *cli.Command) error {
	client, id, err := resolveWorkspace(ctx, cmd, cmd.Args().First())
	if err != nil {
		return err
	}
	w, err := client.GetWorkspace(ctx, id)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.Workspace(out, cmd.Bool("json"), *w)
}

func createWorkspace(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	name, err := argN(cmd, 0, "workspace name")
	if err != nil {
		return err
	}
	w, err := client.CreateWorkspace(ctx, name, cmd.String("organization"))
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Created workspace %s", util.Accented(util.DashString(w.Name)))
	return render.Workspace(out, cmd.Bool("json"), *w)
}

func updateWorkspace(ctx context.Context, cmd *cli.Command) error {
	client, id, err := resolveWorkspace(ctx, cmd, cmd.Args().First())
	if err != nil {
		return err
	}
	w, err := client.UpdateWorkspace(ctx, id, cmd.String("name"))
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated workspace %s", util.Accented(util.DashString(w.Id)))
	return render.Workspace(out, cmd.Bool("json"), *w)
}

func deleteWorkspace(ctx context.Context, cmd *cli.Command) error {
	client, id, err := resolveWorkspace(ctx, cmd, cmd.Args().First())
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Delete workspace %s? This cannot be undone.", id)) {
		return errors.New("aborted")
	}
	if err := client.DeleteWorkspace(ctx, id); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"id": id, "deleted": true})
		return nil
	}
	out.Statusf("Deleted workspace %s", util.Accented(id))
	return nil
}

func listWorkspaceProjects(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	projects, nextCursor, err := client.ListWorkspaceProjects(ctx, wsID, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudAPIError(err)
	}
	return renderProjects(cmd, projects, nextCursor)
}

func getWorkspaceProject(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	projectID, err := argN(cmd, 0, "project ID")
	if err != nil {
		return err
	}
	p, err := client.GetWorkspaceProject(ctx, wsID, projectID)
	if err != nil {
		return cloudAPIError(err)
	}
	return renderProject(cmd, *p)
}

func createWorkspaceProject(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	name, err := argN(cmd, 0, "project name")
	if err != nil {
		return err
	}
	p, err := client.CreateWorkspaceProject(ctx, wsID, name)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Created project %s", util.Accented(util.DashString(p.Name)))
	return renderProject(cmd, *p)
}

func updateWorkspaceProject(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	projectID, err := argN(cmd, 0, "project ID")
	if err != nil {
		return err
	}
	p, err := client.UpdateWorkspaceProject(ctx, wsID, projectID, cmd.String("name"))
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated project %s", util.Accented(util.DashString(p.Id)))
	return renderProject(cmd, *p)
}

func deleteWorkspaceProject(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	projectID, err := argN(cmd, 0, "project ID")
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Delete project %s? This cannot be undone.", projectID)) {
		return errors.New("aborted")
	}
	if err := client.DeleteWorkspaceProject(ctx, wsID, projectID); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"id": projectID, "deleted": true})
		return nil
	}
	out.Statusf("Deleted project %s", util.Accented(projectID))
	return nil
}

func listWorkspaceMembers(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	members, err := client.ListWorkspaceMembers(ctx, wsID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.WorkspaceMembers(out, cmd.Bool("json"), members)
}

func getWorkspaceMember(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	userID, err := argN(cmd, 0, "user ID")
	if err != nil {
		return err
	}
	member, err := client.GetWorkspaceMember(ctx, wsID, userID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.WorkspaceMember(out, cmd.Bool("json"), *member)
}

func updateWorkspaceMember(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	userID, err := argN(cmd, 0, "user ID")
	if err != nil {
		return err
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	member, err := client.UpdateWorkspaceMember(ctx, wsID, userID, role)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated member %s", util.Accented(userID))
	return render.WorkspaceMember(out, cmd.Bool("json"), *member)
}

func deleteWorkspaceMember(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	userID, err := argN(cmd, 0, "user ID")
	if err != nil {
		return err
	}
	if err := client.DeleteWorkspaceMember(ctx, wsID, userID); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"userId": userID, "removed": true})
		return nil
	}
	out.Statusf("Removed member %s", util.Accented(userID))
	return nil
}

func listWorkspaceInvites(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	invites, err := client.ListWorkspaceInvites(ctx, wsID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.WorkspaceInvites(out, cmd.Bool("json"), invites)
}

func getWorkspaceInvite(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	token, err := argN(cmd, 0, "invite token")
	if err != nil {
		return err
	}
	invite, err := client.GetWorkspaceInvite(ctx, token)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.WorkspaceInvite(out, cmd.Bool("json"), *invite)
}

func createWorkspaceInvite(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	email, err := argN(cmd, 0, "email")
	if err != nil {
		return err
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	resp, err := client.CreateWorkspaceInvite(ctx, wsID, email, role)
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}
	out.Statusf("Invited %s to workspace", util.Accented(email))
	out.Statusf("Invite token: %s", util.DashString(resp.InviteToken))
	return nil
}

func deleteWorkspaceInvite(ctx context.Context, cmd *cli.Command) error {
	client, wsID, err := resolveWorkspace(ctx, cmd, cmd.String("workspace"))
	if err != nil {
		return err
	}
	email, err := argN(cmd, 0, "email")
	if err != nil {
		return err
	}
	if err := client.DeleteWorkspaceInvite(ctx, wsID, email); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"email": email, "deleted": true})
		return nil
	}
	out.Statusf("Revoked invite for %s", util.Accented(email))
	return nil
}

func answerWorkspaceInvite(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	token, err := argN(cmd, 0, "invite token")
	if err != nil {
		return err
	}
	accept := !cmd.Bool("decline")
	member, err := client.AnswerWorkspaceInvite(ctx, token, accept)
	if err != nil {
		return cloudAPIError(err)
	}
	if !accept {
		out.Status("Declined invite.")
		return nil
	}
	out.Status("Accepted invite.")
	if member != nil {
		return render.WorkspaceMember(out, cmd.Bool("json"), *member)
	}
	return nil
}

// confirmDestroy prompts for confirmation of a destructive action, honoring
// --yes / non-interactive mode (which auto-confirm).
func confirmDestroy(ctx context.Context, cmd *cli.Command, prompt string) bool {
	if SkipPrompts(cmd) {
		return true
	}
	confirm := false
	if err := huh.NewForm(huh.NewGroup(util.Confirm().
		Title(prompt).
		Value(&confirm).
		WithTheme(util.FormTheme()))).
		RunWithContext(ctx); err != nil {
		return false
	}
	return confirm
}
