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

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// init appends the Public-API-only API key subcommands to `lk project`. Like
// member/invite, they are Hidden and require --experimental-auth; the project
// comes from the global --project flag (or a cached alias).
func init() {
	keyCommand := &cli.Command{
		Name:   "key",
		Usage:  "Manage API keys of a LiveKit Cloud project (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List project API keys",
				UsageText: "lk project key list [--mine] [--include-agent-keys] --project PROJECT --experimental-auth",
				Action:    listProjectKeys,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "mine", Usage: "Only list keys owned by you"},
					&cli.BoolFlag{Name: "include-agent-keys", Usage: "Include keys created for cloud agents"},
					jsonFlag,
				},
			},
			{
				Name:      "create",
				Usage:     "Generate a new API key and secret",
				UsageText: "lk project key create [--description TEXT] [--service-account] --project PROJECT --experimental-auth",
				Action:    createProjectKey,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "description", Usage: "Human-readable `DESCRIPTION` for the key"},
					&cli.BoolFlag{Name: "service-account", Usage: "Create a project-owned key that is not tied to your user"},
					jsonFlag,
				},
			},
			{
				Name:      "delete",
				Usage:     "Revoke an API key",
				UsageText: "lk project key delete API_KEY --project PROJECT --experimental-auth",
				ArgsUsage: "API_KEY",
				Action:    deleteProjectKey,
				Flags:     []cli.Flag{jsonFlag},
			},
		},
	}

	// ProjectCommands[0] is the `project` command.
	ProjectCommands[0].Commands = append(ProjectCommands[0].Commands, keyCommand)
}

func listProjectKeys(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	if cmd.Bool("mine") {
		if cmd.Bool("include-agent-keys") {
			return errors.New("--mine and --include-agent-keys cannot be combined")
		}
		personal, _, err := client.ListScopedKeys(ctx, projectID)
		if err != nil {
			return cloudAPIError(err)
		}
		return render.AccessKeys(out, cmd.Bool("json"), personal)
	}
	keys, err := client.ListKeys(ctx, projectID, cmd.Bool("include-agent-keys"))
	if err != nil {
		return cloudAPIError(err)
	}
	return render.AccessKeys(out, cmd.Bool("json"), keys)
}

func createProjectKey(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	key, err := client.GenerateKey(ctx, projectID, cmd.String("description"), cmd.Bool("service-account"))
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(key)
		return nil
	}
	out.Status("Created API key. Store the secret now; it cannot be retrieved again.")
	out.Resultf("API Key:    %s\n", util.Accented(util.Deref(key.ApiKey)))
	out.Resultf("API Secret: %s\n", util.Accented(util.Deref(key.Secret)))
	return nil
}

func deleteProjectKey(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	apiKey, err := requireArg(cmd, "API key")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Revoke API key %s? Anything using it will stop working.", apiKey)) {
		return errors.New("aborted")
	}
	if err := client.DeleteKey(ctx, projectID, apiKey); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"apiKey": apiKey, "deleted": true})
		return nil
	}
	out.Statusf("Revoked API key %s", util.Accented(apiKey))
	return nil
}
