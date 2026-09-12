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

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// init appends the Public-API-only member and invite subcommands to `lk project`.
// They are Hidden and require --experimental-auth (user-based auth). The project
// they act on comes from the global --project flag (or a cached alias).
func init() {
	memberCommand := &cli.Command{
		Name:   "member",
		Usage:  "Manage members of a LiveKit Cloud project (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List project members",
				UsageText: "lk project member list --project PROJECT --experimental-auth",
				Action:    listProjectMembers,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a project member by user ID",
				UsageText: "lk project member get USER_ID --project PROJECT --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    getProjectMember,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "update",
				Usage:     "Change a project member's role",
				UsageText: "lk project member update USER_ID --role ROLE --project PROJECT --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    updateProjectMember,
				Flags:     []cli.Flag{roleFlag, jsonFlag},
			},
			{
				Name:      "remove",
				Usage:     "Remove a member from a project",
				UsageText: "lk project member remove USER_ID --project PROJECT --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    removeProjectMember,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "add",
				Usage:     "Grant existing workspace members access to a project",
				UsageText: "lk project member add USER_ID [USER_ID...] --role ROLE --project PROJECT --experimental-auth",
				ArgsUsage: "USER_ID [USER_ID...]",
				Action:    addProjectMembers,
				Flags:     []cli.Flag{roleFlag, jsonFlag},
			},
		},
	}
	inviteCommand := &cli.Command{
		Name:   "invite",
		Usage:  "Manage project invites (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List pending project invites",
				UsageText: "lk project invite list --project PROJECT --experimental-auth",
				Action:    listProjectInvites,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get an invite by its token",
				UsageText: "lk project invite get INVITE_TOKEN --experimental-auth",
				ArgsUsage: "INVITE_TOKEN",
				Action:    getProjectInvite,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Invite a member to a project by email",
				UsageText: "lk project invite create EMAIL --role ROLE --project PROJECT --experimental-auth",
				ArgsUsage: "EMAIL",
				Action:    createProjectInvite,
				Flags:     []cli.Flag{roleFlag, jsonFlag},
			},
			{
				Name:      "update",
				Usage:     "Change the role of a pending invite",
				UsageText: "lk project invite update EMAIL --role ROLE --project PROJECT --experimental-auth",
				ArgsUsage: "EMAIL",
				Action:    updateProjectInvite,
				Flags:     []cli.Flag{roleFlag, jsonFlag},
			},
			{
				Name:      "delete",
				Usage:     "Revoke a pending invite",
				UsageText: "lk project invite delete EMAIL --project PROJECT --experimental-auth",
				ArgsUsage: "EMAIL",
				Action:    deleteProjectInvite,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "answer",
				Usage:     "Accept or decline a project invite",
				UsageText: "lk project invite answer INVITE_TOKEN [--decline] --experimental-auth",
				ArgsUsage: "INVITE_TOKEN",
				Action:    answerProjectInvite,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "decline", Usage: "Decline the invite instead of accepting it"},
					jsonFlag,
				},
			},
		},
	}

	// ProjectCommands[0] is the `project` command.
	ProjectCommands[0].Commands = append(ProjectCommands[0].Commands, memberCommand, inviteCommand)
}

func requireArg(cmd *cli.Command, name string) (string, error) {
	v := cmd.Args().First()
	if v == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return "", errors.New(name + " is required")
	}
	return v, nil
}

func listProjectMembers(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	members, err := client.ListProjectMembers(ctx, projectID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.ProjectMembers(out, cmd.Bool("json"), members)
}

func getProjectMember(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	userID, err := requireArg(cmd, "user ID")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	member, err := client.GetProjectMember(ctx, projectID, userID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.ProjectMember(out, cmd.Bool("json"), *member)
}

func updateProjectMember(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	userID, err := requireArg(cmd, "user ID")
	if err != nil {
		return err
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	member, err := client.UpdateProjectMember(ctx, projectID, userID, role)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated member %s", util.Accented(userID))
	return render.ProjectMember(out, cmd.Bool("json"), *member)
}

func removeProjectMember(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	userID, err := requireArg(cmd, "user ID")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	if err := client.RemoveProjectMember(ctx, projectID, userID); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"userId": userID, "removed": true})
		return nil
	}
	out.Statusf("Removed member %s", util.Accented(userID))
	return nil
}

func addProjectMembers(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	userIDs := cmd.Args().Slice()
	if len(userIDs) == 0 {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("at least one user ID is required")
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	members, err := client.AddWorkspaceMembersToProject(ctx, projectID, userIDs, role)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Added %d member(s) to project", len(members))
	return render.ProjectMembers(out, cmd.Bool("json"), members)
}

func listProjectInvites(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	invites, err := client.ListProjectInvites(ctx, projectID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.ProjectInvites(out, cmd.Bool("json"), invites)
}

func getProjectInvite(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	token, err := requireArg(cmd, "invite token")
	if err != nil {
		return err
	}
	invite, err := client.GetProjectInvite(ctx, token)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.ProjectInvite(out, cmd.Bool("json"), *invite)
}

func createProjectInvite(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	email, err := requireArg(cmd, "email")
	if err != nil {
		return err
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	resp, err := client.InviteProjectMember(ctx, projectID, email, role)
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}
	out.Statusf("Invited %s to project with code:", util.Accented(email))
	out.Resultf("%s\n", util.Accented(*resp.InviteToken))
	return nil
}

func updateProjectInvite(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	email, err := requireArg(cmd, "email")
	if err != nil {
		return err
	}
	role, err := public.ParseRole(cmd.String("role"))
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	invite, err := client.UpdateProjectInvite(ctx, projectID, email, role)
	if err != nil {
		return cloudAPIError(err)
	}
	out.Statusf("Updated invite for %s", util.Accented(email))
	return render.ProjectInvite(out, cmd.Bool("json"), *invite)
}

func deleteProjectInvite(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	email, err := requireArg(cmd, "email")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	if err := client.DeleteProjectInvite(ctx, projectID, email); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"email": email, "deleted": true})
		return nil
	}
	out.Statusf("Revoked invite for %s", util.Accented(email))
	return nil
}

func answerProjectInvite(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	token, err := requireArg(cmd, "invite token")
	if err != nil {
		return err
	}
	accept := !cmd.Bool("decline")
	member, err := client.AnswerProjectInvitation(ctx, token, accept)
	if err != nil {
		return cloudAPIError(err)
	}
	if !accept {
		out.Status("Declined invite.")
		return nil
	}
	out.Status("Accepted invite.")
	if member != nil {
		return render.ProjectMember(out, cmd.Bool("json"), *member)
	}
	return nil
}
