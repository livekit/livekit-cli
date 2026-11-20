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
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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
				Name:      "download",
				Usage:     "Download the agent code that LiveKit generates from a builder agent version",
				UsageText: "lk agent builder download BUILDER_AGENT_ID [--version VERSION_ID] [--output FILE | --unzip DIR] --project PROJECT --user-auth",
				Description: `Downloads a zip with src/agent.py, pyproject.toml, .env.example, a
Dockerfile and the project docs, so that you can run and change the agent
yourself. The code holds every header value of the configuration, so it
needs the role that can update the agent, and lk writes it readable only
by you.`,
				ArgsUsage: "BUILDER_AGENT_ID",
				Action:    downloadBuilderAgentCode,
				Flags: []cli.Flag{
					builderVersionFlag,
					&cli.StringFlag{
						Name:  "output",
						Usage: "`FILE` to write the zip to. If unset, the name the server suggests, which must not exist yet",
					},
					&cli.StringFlag{
						Name:  "unzip",
						Usage: "Unpack the zip into `DIR`, which must be empty or not exist, instead of writing it",
					},
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
	err = json.Unmarshal(data, &object)
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, col := lineAndColumn(data, syntaxErr.Offset)
		return nil, fmt.Errorf("--config-file %s: line %d, column %d: %w", path, line, col, err)
	}
	// Any other error is valid JSON that isn't an object; so is null.
	if err != nil || object == nil {
		return nil, fmt.Errorf("--config-file %s: the configuration must be a JSON object", path)
	}
	// The API ignores properties it doesn't know, so a whole builder agent
	// sent as its configuration would reset the configuration to defaults.
	if _, ok := object["config"]; ok {
		return nil, fmt.Errorf("--config-file %s holds a builder agent, not a configuration: pass its \"config\" object", path)
	}
	return bytes.TrimSpace(data), nil
}

// lineAndColumn is the 1-based line and column of the byte at offset in data,
// where a json.SyntaxError's Offset points just past the bad byte.
func lineAndColumn(data []byte, offset int64) (line, col int) {
	before := data[:min(max(offset-1, 0), int64(len(data)))]
	line = bytes.Count(before, []byte("\n")) + 1
	return line, len(before) - bytes.LastIndexByte(before, '\n')
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
		return cloudOnlyAPIError(err)
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
		return cloudOnlyAPIError(err)
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
		return cloudOnlyAPIError(err)
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
		return cloudOnlyAPIError(err)
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
		return cloudOnlyAPIError(err)
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
		return cloudOnlyAPIError(err)
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
	res, err := client.DeployBuilderAgent(ctx, projectID, id, cmd.String("version"))
	if err != nil {
		return cloudOnlyAPIError(err)
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
	// The Date header is truncated to the second, so a deployment that
	// finished up to a second before it could still read as later.
	var after time.Time
	if !res.Date.IsZero() {
		after = res.Date.Add(time.Second)
	}
	agent, err := waitForBuilderDeployment(ctx, client, projectID, id, after)
	if err != nil {
		return err
	}
	if !cmd.Bool("json") {
		if util.Deref(agent.Deployment.Status) == oapi.DEPLOYMENTSTATUSSLEEPING {
			out.Statusf("Builder agent %s deployed version %s, and sleeps until a dispatch starts it", util.Accented(id), util.Accented(version))
		} else {
			out.Statusf("Builder agent %s runs version %s", util.Accented(id), util.Accented(version))
		}
	}
	return render.BuilderAgent(out, cmd.Bool("json"), *agent)
}

// waitForBuilderDeployment reads a builder agent until a new deployment is up
// or has failed, printing each status it reads. Up is RUNNING, or SLEEPING:
// an agent that nothing dispatched scales to zero, and a dispatch starts it.
//
// A status read before the new deployment shows can still be the previous
// one's. So an up status ends the wait only after a BUILDING or STARTING, or
// when its deployedAt is after after; and a failure only after a BUILDING or
// STARTING. after is a server time, from the deploy response's Date header,
// so the check doesn't depend on the local clock; the zero time turns it off.
func waitForBuilderDeployment(ctx context.Context, client *public.Client, projectID, id string, after time.Time) (*public.BuilderAgent, error) {
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
			return nil, cloudOnlyAPIError(err)
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
		case oapi.DEPLOYMENTSTATUSRUNNING, oapi.DEPLOYMENTSTATUSSLEEPING:
			if inProgress || (!after.IsZero() && deployedAt.After(after)) {
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

// builderCodeURLExpiry is how long, in seconds, the code URL that download
// asks for stays valid. download uses it at once, and the URL needs no other
// credential, so it asks for far less than the API's 15 minutes.
const builderCodeURLExpiry = 60

func downloadBuilderAgentCode(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "builder agent ID")
	if err != nil {
		return err
	}
	output, dir := cmd.String("output"), cmd.String("unzip")
	if output != "" && dir != "" {
		return errors.New("--output and --unzip can't be combined")
	}
	if dir != "" {
		if err := requireEmptyDir(dir); err != nil {
			return err
		}
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	code, err := client.GetBuilderAgentCodeURL(ctx, projectID, id, cmd.String("version"), builderCodeURLExpiry)
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	version := util.Deref(code.VersionId)
	data, filename, err := public.DownloadBuilderAgentCode(ctx, *code.Url)
	if err != nil {
		return err
	}
	if dir != "" {
		n, err := unzipBuilderAgentCode(data, dir)
		if err != nil {
			return err
		}
		out.Statusf("Unpacked %d files of version %s of builder agent %s into %s", n, util.Accented(version), util.Accented(id), dir)
		return nil
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if output == "" {
		output = cmp.Or(filename, version+".zip")
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(output, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; pass --output to choose the file", output)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out.Statusf("Downloaded version %s of builder agent %s to %s", util.Accented(version), util.Accented(id), output)
	return nil
}

// requireEmptyDir checks that dir is an empty directory or doesn't exist, so
// that unpacking into it overwrites nothing.
func requireEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("--unzip %s: the directory isn't empty", dir)
	}
	return nil
}

// unzipBuilderAgentCode unpacks the agent code zip data into dir, creating
// it, and returns the number of files. Every path stays inside dir (os.Root
// refuses the rest), and an entry that isn't a file or a directory is an
// error. Files and directories are readable only by the user, because the
// code holds the configuration's header values.
func unzipBuilderAgentCode(data []byte, dir string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("the agent code isn't a zip: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	files := 0
	for _, entry := range zr.File {
		name := path.Clean(entry.Name)
		switch mode := entry.Mode(); {
		case mode.IsDir():
			err = root.MkdirAll(name, 0o700)
		case mode.IsRegular():
			err = unzipFile(root, name, entry)
			files++
		default:
			err = fmt.Errorf("%s is not a file or a directory", entry.Name)
		}
		if err != nil {
			return 0, fmt.Errorf("unpack agent code: %w", err)
		}
	}
	return files, nil
}

func unzipFile(root *os.Root, name string, entry *zip.File) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
