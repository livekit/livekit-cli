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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	builderConfigFileFlag = &cli.StringFlag{
		Name:  "config-file",
		Usage: "`FILE` that holds the configuration, a JSON object, or - to read it from stdin",
	}
	builderVersionFlag = &cli.StringFlag{
		Name:  "version",
		Usage: "`VERSION_ID`, as listed by lk agent builder versions. If unset, the current version",
	}
)

// init appends `lk agent builder` to `lk agent`. Builder agents exist only on
// LiveKit Cloud, and only the Public API serves them, so every builder
// command requires --user-auth and calls pkg/public directly; there is no
// API-key implementation. Like the other Public-API-only groups, it is Hidden
// until a user signs in (see setUserAuthHidden).
func init() {
	builderCommand := &cli.Command{
		Name:     "builder",
		Usage:    "Manage Agent Builder agents, which LiveKit Cloud builds from a configuration (requires --user-auth)",
		Hidden:   true,
		Metadata: userAuthOnly,
		Description: `Agent Builder agents run on LiveKit Cloud from a configuration, with no code
to write. They are the agents of Agent Builder in the dashboard.

A configuration is a JSON object. To change one, save it from get, edit it, and
pass it to update:

  lk agent builder get BUILDER_AGENT_ID --json | jq .config > config.json
  lk agent builder update BUILDER_AGENT_ID --config-file config.json

Each update makes a new version. The running agent changes only when you
deploy. To roll back, deploy an earlier version.`,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List the builder agents of a project",
				UsageText: "lk agent builder list [--limit N] [--cursor CURSOR] --project PROJECT --user-auth",
				Action:    listBuilderAgents,
				Flags:     []cli.Flag{limitFlag, cursorFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get a builder agent, with its configuration in --json",
				UsageText: "lk agent builder get BUILDER_AGENT_ID --project PROJECT --user-auth",
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    getBuilderAgent,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Create a builder agent. It doesn't run until you deploy it",
				UsageText: "lk agent builder create --name NAME [--config-file FILE] --project PROJECT --user-auth",
				Action:    createBuilderAgent,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Usage:    "`NAME` the agent registers under, which dispatches use",
						Required: true,
					},
					builderConfigFileFlag,
					jsonFlag,
				},
			},
			{
				Name:      "update",
				Usage:     "Replace a builder agent's configuration, rename it, or both",
				UsageText: "lk agent builder update BUILDER_AGENT_ID [--config-file FILE] [--name NAME] --project PROJECT --user-auth",
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    updateBuilderAgent,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "name",
						Usage: "New `NAME`. The running agent keeps its old name until you deploy",
					},
					builderConfigFileFlag,
					jsonFlag,
				},
			},
			{
				Name:      "deploy",
				Usage:     "Deploy a builder agent's current configuration, or an earlier version to roll back",
				UsageText: "lk agent builder deploy BUILDER_AGENT_ID [--version VERSION_ID] [--wait] --project PROJECT --user-auth",
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    deployBuilderAgent,
				Flags: []cli.Flag{
					builderVersionFlag,
					&cli.BoolFlag{
						Name:  "wait",
						Usage: fmt.Sprintf("Wait until the agent runs, or its deployment fails, for up to %s", buildTimeout),
					},
					jsonFlag,
				},
			},
			{
				Name:      "delete",
				Usage:     "Delete a builder agent, its versions and its deployment",
				UsageText: "lk agent builder delete BUILDER_AGENT_ID --project PROJECT --user-auth",
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    deleteBuilderAgent,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "versions",
				Usage:     "List the versions of a builder agent, newest first",
				UsageText: "lk agent builder versions BUILDER_AGENT_ID [--limit N] [--cursor CURSOR] --project PROJECT --user-auth",
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    listBuilderAgentVersions,
				Flags:     []cli.Flag{limitFlag, cursorFlag, jsonFlag},
			},
		},
	}
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, builderCommand)
}

// builderProject returns a Public API client and the project a builder
// command acts on: --project, or the default project.
func builderProject(ctx context.Context, cmd *cli.Command) (*public.Client, string, error) {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return nil, "", err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return nil, "", err
	}
	return client, projectID, nil
}

// readBuilderConfig reads the configuration that --config-file names, or nil
// when it is unset. It sends the JSON as written, so properties that this lk
// doesn't know reach the API.
func readBuilderConfig(cmd *cli.Command) (json.RawMessage, error) {
	path := cmd.String("config-file")
	if path == "" {
		return nil, nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, fmt.Errorf("--config-file %s: the configuration must be a JSON object", path)
	}
	// The API ignores properties it doesn't know, so a whole builder agent
	// sent as its configuration would reset the configuration to defaults.
	if _, ok := object["config"]; ok {
		return nil, fmt.Errorf("--config-file %s holds a builder agent, not a configuration: pass its \"config\" object", path)
	}
	return bytes.TrimSpace(data), nil
}

// warnHiddenHeaderValues says, when config holds ******** in place of a
// header value, that a write of it would be refused.
func warnHiddenHeaderValues(configs ...json.RawMessage) {
	for _, c := range configs {
		if public.HasHiddenHeaderValues(c) {
			out.Warnf("Header values show as %s because your role can't read them. Create and update refuse %s: replace each with its value before you write this configuration back.",
				public.HiddenHeaderValue, public.HiddenHeaderValue)
			return
		}
	}
}

func listBuilderAgents(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agents, next, err := client.ListBuilderAgents(ctx, projectID, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudAPIError(err)
	}
	return render.BuilderAgentsPage(out, cmd.Bool("json"), agents, next)
}

func getBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agent, err := client.GetBuilderAgent(ctx, projectID, id)
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		warnHiddenHeaderValues(agent.Config)
	}
	return render.BuilderAgent(out, cmd.Bool("json"), *agent)
}

func createBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	config, err := readBuilderConfig(cmd)
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agent, err := client.CreateBuilderAgent(ctx, projectID, cmd.String("name"), config)
	if err != nil {
		return cloudAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Created builder agent %s. Deploy it with: lk agent builder deploy %s", util.Accented(util.Deref(agent.Name)), util.Deref(agent.Id))
	}
	return render.BuilderAgent(out, cmd.Bool("json"), *agent)
}

func updateBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	config, err := readBuilderConfig(cmd)
	if err != nil {
		return err
	}
	name := optionalString(cmd, "name")
	if config == nil && name == nil {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("nothing to update: pass --config-file, --name or both")
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agent, err := client.UpdateBuilderAgent(ctx, projectID, id, name, config)
	if err != nil {
		return cloudAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Updated builder agent %s. The running agent changes when you deploy: lk agent builder deploy %s", util.Accented(id), id)
	}
	return render.BuilderAgent(out, cmd.Bool("json"), *agent)
}

func deleteBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Delete builder agent %s, with its versions and its deployment?", id)) {
		return errors.New("aborted")
	}
	if err := client.DeleteBuilderAgent(ctx, projectID, id); err != nil {
		return cloudAPIError(err)
	}
	return util.RenderAction(out, cmd.Bool("json"), "builderAgentId", id, "deleted", fmt.Sprintf("Deleted builder agent %s", util.Accented(id)))
}

func listBuilderAgentVersions(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	versions, next, err := client.ListBuilderAgentVersions(ctx, projectID, id, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		configs := make([]json.RawMessage, len(versions))
		for i, v := range versions {
			configs[i] = v.Config
		}
		warnHiddenHeaderValues(configs...)
	}
	return render.BuilderAgentVersionsPage(out, cmd.Bool("json"), versions, next)
}

// builderDeployPoll is how often deploy --wait reads the deployment status.
// Tests shorten it.
var builderDeployPoll = 3 * time.Second

func deployBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	started := time.Now()
	res, err := client.DeployBuilderAgent(ctx, projectID, id, cmd.String("version"))
	if err != nil {
		return cloudAPIError(err)
	}
	version := util.Deref(res.VersionId)
	if !cmd.Bool("wait") {
		if cmd.Bool("json") {
			return util.PrintJSONTo(out.ResultWriter(), res)
		}
		out.Statusf("Deploying version %s of builder agent %s. Follow it with: lk agent builder get %s", util.Accented(version), util.Accented(id), id)
		return nil
	}
	out.Statusf("Deploying version %s of builder agent %s", util.Accented(version), util.Accented(id))
	agent, err := waitForBuilderDeployment(ctx, client, projectID, id, started)
	if err != nil {
		return err
	}
	if !cmd.Bool("json") {
		out.Statusf("Builder agent %s runs version %s", util.Accented(id), util.Accented(version))
	}
	return render.BuilderAgent(out, cmd.Bool("json"), *agent)
}

// waitForBuilderDeployment reads a builder agent until the deployment that
// started at started runs or fails, printing each status it reads. A status
// read before the new deployment shows can still be the previous one's, so
// RUNNING ends the wait only after a BUILDING or STARTING, or with a
// deployedAt after started, and a failure only after a BUILDING or STARTING.
func waitForBuilderDeployment(ctx context.Context, client *public.Client, projectID, id string, started time.Time) (*public.BuilderAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	following := fmt.Sprintf("the deployment continues; follow it with: lk agent builder get %s", id)
	var last oapi.LivekitPublicapiAgentsV1DeploymentStatus
	inProgress := false
	for {
		agent, err := client.GetBuilderAgent(ctx, projectID, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("stopped waiting after %s: %s", buildTimeout, following)
			}
			return nil, cloudAPIError(err)
		}
		var status oapi.LivekitPublicapiAgentsV1DeploymentStatus
		var deployedAt time.Time
		if d := agent.Deployment; d != nil {
			status, deployedAt = util.Deref(d.Status), util.Deref(d.DeployedAt)
		}
		if status != last {
			out.Statusf("Status: %s", render.DeploymentStatus(agent.Deployment))
			last = status
		}
		switch status {
		case oapi.DEPLOYMENTSTATUSBUILDING, oapi.DEPLOYMENTSTATUSSTARTING:
			inProgress = true
		case oapi.DEPLOYMENTSTATUSRUNNING:
			if inProgress || deployedAt.After(started) {
				return agent, nil
			}
		case oapi.DEPLOYMENTSTATUSBUILDFAILED, oapi.DEPLOYMENTSTATUSFAILED:
			if inProgress {
				return nil, fmt.Errorf("deployment of builder agent %s failed: %s", id, render.DeploymentStatus(agent.Deployment))
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("stopped waiting after %s: %s", buildTimeout, following)
		case <-time.After(builderDeployPoll):
		}
	}
}
