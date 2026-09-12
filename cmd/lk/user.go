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

	"github.com/livekit/livekit-cli/v2/pkg/public/render"
)

// UserCommands are the Public-API-only user commands. The group is Hidden and
// requires --experimental-auth (user-based auth).
var UserCommands = []*cli.Command{
	{
		Name:   "user",
		Usage:  "List and inspect LiveKit Cloud users (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List users in a project or workspace",
				UsageText: "lk user list --project PROJECT | --workspace WORKSPACE --experimental-auth",
				Action:    listUsers,
				Flags:     []cli.Flag{workspaceFlag, limitFlag, cursorFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a user by ID",
				UsageText: "lk user get USER_ID --experimental-auth",
				ArgsUsage: "USER_ID",
				Action:    getUser,
				Flags:     []cli.Flag{jsonFlag},
			},
		},
	},
}

func listUsers(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	var projectID, workspaceID string
	if ref := cmd.String("project"); ref != "" {
		if projectID, err = resolveProjectRef(ctx, cmd, conf, user, ref); err != nil {
			return err
		}
	}
	if ref := cmd.String("workspace"); ref != "" {
		if workspaceID, err = resolveWorkspaceRef(ctx, cmd, conf, user, ref); err != nil {
			return err
		}
	}
	if projectID == "" && workspaceID == "" {
		return errors.New("one of --project or --workspace is required")
	}
	users, nextCursor, err := client.ListUsers(ctx, projectID, workspaceID, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudAPIError(err)
	}
	return render.UsersPage(out, cmd.Bool("json"), users, nextCursor)
}

func getUser(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	id, err := argN(cmd, 0, "user ID")
	if err != nil {
		return err
	}
	u, err := client.GetUser(ctx, id)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.User(out, cmd.Bool("json"), *u)
}

// cloudWhoami implements `lk cloud whoami`: it prints the signed-in user as
// resolved by the Public API. Wired into CloudCommands (cloud.go).
func cloudWhoami(ctx context.Context, cmd *cli.Command) error {
	client, _, _, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	u, err := client.GetCurrentUser(ctx)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.User(out, cmd.Bool("json"), *u)
}
