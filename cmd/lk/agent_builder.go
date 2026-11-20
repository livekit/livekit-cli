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
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
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

// init appends `lk agent builder` to `lk agent`. Agent Builder agents exist only on
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

AGENT_ID is the AB_ ID that list prints and the dashboard shows, not the
cloud agent ID (CA_) that the other lk agent commands take.

A configuration is a JSON object. To change one, save it from get, edit it, and
pass it to update:

  lk agent builder get AGENT_ID --json | jq .config > config.json
  lk agent builder update AGENT_ID --config-file config.json

Each update makes a new version. The running agent changes only when you
deploy. To roll back, deploy an earlier version.`,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List the Agent Builder agents of a project",
				UsageText: "lk agent builder list [--limit N] [--cursor CURSOR] --project PROJECT --user-auth",
				Action:    listAgentBuilderAgents,
				Flags:     []cli.Flag{limitFlag, cursorFlag, jsonFlag},
			},
			{
				Name:      "get",
				Usage:     "Get an Agent Builder agent, with its configuration in --json",
				UsageText: "lk agent builder get AGENT_ID --project PROJECT --user-auth",
				ArgsUsage: "AGENT_ID",
				Action:    getAgentBuilderAgent,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Create an Agent Builder agent. It doesn't run until you deploy it",
				UsageText: "lk agent builder create --name NAME [--config-file FILE] --project PROJECT --user-auth",
				Action:    createAgentBuilderAgent,
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
				Usage:     "Replace an Agent Builder agent's configuration, rename it, or both",
				UsageText: "lk agent builder update AGENT_ID [--config-file FILE] [--name NAME] --project PROJECT --user-auth",
				ArgsUsage: "AGENT_ID",
				Action:    updateAgentBuilderAgent,
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
				Usage:     "Deploy an Agent Builder agent's current configuration, or an earlier version to roll back",
				UsageText: "lk agent builder deploy AGENT_ID [--version VERSION_ID] [--wait] --project PROJECT --user-auth",
				ArgsUsage: "AGENT_ID",
				Action:    deployAgentBuilderAgent,
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
				Usage:     "Download the agent code that LiveKit generates from an Agent Builder agent version",
				UsageText: "lk agent builder download AGENT_ID [--version VERSION_ID] [--output FILE | --unzip DIR] --project PROJECT --user-auth",
				Description: `Downloads a zip with src/agent.py, pyproject.toml, .env.example, a
Dockerfile and the project docs, so that you can run and change the agent
yourself. The code holds every header value of the configuration, so it
needs the role that can update the agent, and lk writes it readable only
by you.`,
				ArgsUsage: "AGENT_ID",
				Action:    downloadAgentBuilderAgentCode,
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
				Usage:     "Delete an Agent Builder agent, its versions and its deployment",
				UsageText: "lk agent builder delete AGENT_ID --project PROJECT --user-auth",
				ArgsUsage: "AGENT_ID",
				Action:    deleteAgentBuilderAgent,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "versions",
				Usage:     "List the versions of an Agent Builder agent, newest first",
				UsageText: "lk agent builder versions AGENT_ID [--limit N] [--cursor CURSOR] --project PROJECT --user-auth",
				ArgsUsage: "AGENT_ID",
				Action:    listAgentBuilderAgentVersions,
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
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("--config-file %s: the file doesn't exist", path)
	}
	if err != nil {
		return nil, fmt.Errorf("--config-file %s: %w", path, err)
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
	// The API ignores properties it doesn't know, so a whole Agent Builder agent
	// sent as its configuration would reset the configuration to defaults.
	if _, ok := object["config"]; ok {
		return nil, fmt.Errorf("--config-file %s holds an Agent Builder agent, not a configuration: pass its \"config\" object", path)
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

func listAgentBuilderAgents(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agents, next, err := client.ListAgentBuilderAgents(ctx, projectID, int32(cmd.Int("limit")), cmd.String("cursor"))
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	return render.AgentBuilderAgentsPage(out, cmd.Bool("json"), agents, next)
}

func getAgentBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agent, err := client.GetAgentBuilderAgent(ctx, projectID, id)
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	if cmd.Bool("json") {
		warnHiddenHeaderValues(agent.Config)
	}
	return render.AgentBuilderAgent(out, cmd.Bool("json"), *agent)
}

func createAgentBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	config, err := readBuilderConfig(cmd)
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	agent, err := client.CreateAgentBuilderAgent(ctx, projectID, cmd.String("name"), config)
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Created Agent Builder agent %s. Deploy it with: lk agent builder deploy %s", util.Accented(util.Deref(agent.Name)), util.Deref(agent.Id))
	}
	return render.AgentBuilderAgent(out, cmd.Bool("json"), *agent)
}

func updateAgentBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
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
	agent, err := client.UpdateAgentBuilderAgent(ctx, projectID, id, name, config)
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Updated Agent Builder agent %s. The running agent changes when you deploy: lk agent builder deploy %s", util.Accented(id), id)
	}
	return render.AgentBuilderAgent(out, cmd.Bool("json"), *agent)
}

func deleteAgentBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Delete Agent Builder agent %s, with its versions and its deployment?", id)) {
		return errors.New("aborted")
	}
	if err := client.DeleteAgentBuilderAgent(ctx, projectID, id); err != nil {
		return cloudOnlyAPIError(err)
	}
	return util.RenderAction(out, cmd.Bool("json"), "agentId", id, "deleted", fmt.Sprintf("Deleted Agent Builder agent %s", util.Accented(id)))
}

func listAgentBuilderAgentVersions(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	versions, next, err := client.ListAgentBuilderAgentVersions(ctx, projectID, id, int32(cmd.Int("limit")), cmd.String("cursor"))
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
	return render.AgentBuilderAgentVersionsPage(out, cmd.Bool("json"), versions, next)
}

// builderDeployPoll is how often deploy --wait reads the deployment status.
// Tests shorten it.
var builderDeployPoll = 3 * time.Second

func deployAgentBuilderAgent(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
	if err != nil {
		return err
	}
	client, projectID, err := builderProject(ctx, cmd)
	if err != nil {
		return err
	}
	// With --wait, read the agent first: a failure read later is this
	// deployment's for sure only if the agent hadn't failed already. An
	// agent that can't be read, or whose status is unknown, counts as
	// failed, the cautious answer.
	failedBefore := true
	if cmd.Bool("wait") {
		if before, err := client.GetAgentBuilderAgent(ctx, projectID, id); err == nil {
			failedBefore = mayHaveFailed(before.Deployment)
		}
	}
	res, err := client.DeployAgentBuilderAgent(ctx, projectID, id, cmd.String("version"))
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	version := util.Deref(res.VersionId)
	if !cmd.Bool("wait") {
		if cmd.Bool("json") {
			return util.PrintJSONTo(out.ResultWriter(), res)
		}
		out.Statusf("Deploying version %s of Agent Builder agent %s. Follow it with: lk agent builder get %s", util.Accented(version), util.Accented(id), id)
		return nil
	}
	out.Statusf("Deploying version %s of Agent Builder agent %s", util.Accented(version), util.Accented(id))
	// The Date header is truncated to the second, so a deployment that
	// finished up to a second before it could still read as later.
	base := deployBaseline{failedBefore: failedBefore}
	if !res.Date.IsZero() {
		base.after = res.Date.Add(time.Second)
	}
	agent, err := waitForBuilderDeployment(ctx, client, projectID, id, base)
	if err != nil {
		return err
	}
	if !cmd.Bool("json") {
		if util.Deref(agent.Deployment.Status) == oapi.DEPLOYMENTSTATUSSLEEPING {
			out.Statusf("Agent Builder agent %s deployed version %s, and sleeps until a dispatch starts it", util.Accented(id), util.Accented(version))
		} else {
			out.Statusf("Agent Builder agent %s runs version %s", util.Accented(id), util.Accented(version))
		}
	}
	return render.AgentBuilderAgent(out, cmd.Bool("json"), *agent)
}

// deployBaseline is what deploy --wait knows from before the deployment it
// waits for, to tell that deployment's statuses from the previous one's.
type deployBaseline struct {
	// after is a server time, from the deploy response's Date header, so
	// that comparing a deployedAt with it doesn't depend on the local clock.
	// The zero time turns that comparison off.
	after time.Time
	// failedBefore reports whether the agent's deployment may have failed
	// before the deploy: it read as failed, its status was unknown, or the
	// agent couldn't be read.
	failedBefore bool
}

// mayHaveFailed reports whether d may be a failed deployment: its status is a
// failure, or unknown. The API leaves the deployment out when it can't read
// the status, and an unknown status can't rule out a failure.
func mayHaveFailed(d *oapi.LivekitPublicapiAgentbuilderV1Deployment) bool {
	if d == nil {
		return true
	}
	switch util.Deref(d.Status) {
	case "", oapi.DEPLOYMENTSTATUSUNSPECIFIED, oapi.DEPLOYMENTSTATUSBUILDFAILED, oapi.DEPLOYMENTSTATUSFAILED:
		return true
	}
	return false
}

// waitForBuilderDeployment reads an Agent Builder agent until a new deployment is up
// or has failed, printing each status it reads. Up is RUNNING, or SLEEPING:
// an agent that nothing dispatched scales to zero, and a dispatch starts it.
//
// A status read before the new deployment shows can still be the previous
// one's. So an up status ends the wait only after a BUILDING or STARTING, or
// when its deployedAt is after base.after. A failure ends it after a BUILDING
// or STARTING, or when the agent hadn't failed before the deploy, so that a
// failure whose BUILDING went unseen, such as while reads failed, isn't
// mistaken for a timeout. After an earlier failure, a failure read without a
// BUILDING or STARTING may be the old one, and the API has no field that
// tells them apart, so the wait goes on.
func waitForBuilderDeployment(ctx context.Context, client *public.Client, projectID, id string, base deployBaseline) (*public.AgentBuilderAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	// However the wait ends early, the deployment goes on without it.
	following := fmt.Sprintf("The deployment continues; follow it with: lk agent builder get %s", id)
	stopped := func() error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("stopped waiting after %s. %s", buildTimeout, following)
		}
		return fmt.Errorf("stopped waiting. %s", following)
	}
	var last oapi.LivekitPublicapiAgentbuilderV1DeploymentStatus
	inProgress := false
	failures := 0
	for {
		agent, err := client.GetAgentBuilderAgent(ctx, projectID, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil, stopped()
			}
			failures++
			if !temporaryPollError(err) || failures > builderPollRetries {
				return nil, fmt.Errorf("%w\n%s", cloudOnlyAPIError(err), following)
			}
			out.Statusf("Reading the status failed (%v); trying again", err)
			select {
			case <-ctx.Done():
				return nil, stopped()
			case <-time.After(builderDeployPoll << failures):
			}
			continue
		}
		failures = 0
		var status oapi.LivekitPublicapiAgentbuilderV1DeploymentStatus
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
			if inProgress || (!base.after.IsZero() && deployedAt.After(base.after)) {
				return agent, nil
			}
		case oapi.DEPLOYMENTSTATUSBUILDFAILED, oapi.DEPLOYMENTSTATUSFAILED:
			if inProgress || !base.failedBefore {
				return nil, fmt.Errorf("deployment of Agent Builder agent %s failed: %s", id, render.DeploymentStatus(agent.Deployment))
			}
		}
		select {
		case <-ctx.Done():
			return nil, stopped()
		case <-time.After(builderDeployPoll):
		}
	}
}

// builderPollRetries is how many reads in a row deploy --wait retries after
// a temporary error, waiting twice as long before each.
const builderPollRetries = 3

// temporaryPollError reports whether a failed status read may succeed when
// tried again: a network error, a rate limit, or a server error.
func temporaryPollError(err error) bool {
	var apiErr *public.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= http.StatusInternalServerError
}

// builderCodeURLExpiry is how long, in seconds, the code URL that download
// asks for stays valid. download uses it at once, and the URL needs no other
// credential, so it asks for far less than the API's 15 minutes.
const builderCodeURLExpiry = 60

func downloadAgentBuilderAgentCode(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "Agent Builder agent ID")
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
	code, err := client.GetAgentBuilderAgentCodeURL(ctx, projectID, id, cmd.String("version"), builderCodeURLExpiry)
	if err != nil {
		return cloudOnlyAPIError(err)
	}
	version := util.Deref(code.VersionId)
	data, filename, err := public.DownloadAgentBuilderAgentCode(ctx, *code.Url)
	if err != nil {
		return err
	}
	if dir != "" {
		n, err := unzipAgentBuilderAgentCode(data, dir)
		if err != nil {
			return err
		}
		out.Statusf("Unpacked %d files of version %s of Agent Builder agent %s into %s", n, util.Accented(version), util.Accented(id), dir)
		return nil
	}
	overwrite := output != ""
	if !overwrite {
		output = cmp.Or(filename, version+".zip")
	}
	if err := writePrivateFile(output, data, overwrite); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; pass --output to choose the file", output)
		}
		return err
	}
	out.Statusf("Downloaded version %s of Agent Builder agent %s to %s", util.Accented(version), util.Accented(id), output)
	return nil
}

// writePrivateFile writes data to name, readable only by the user, because
// agent code holds header values. With overwrite, an existing file is
// replaced and made 0600 too (OpenFile's mode applies only to a new file);
// without it, an existing file is an os.ErrExist.
func writePrivateFile(name string, data []byte, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(name, flags, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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

// unzipAgentBuilderAgentCode unpacks the agent code zip data into dir, an empty
// directory or one that doesn't exist yet, and returns the number of files.
// Every path stays inside dir: a path that isn't local is an error, and
// os.Root refuses the rest. An entry that isn't a file or a directory is an
// error too. Files and directories are readable only by the user, because the
// code holds the configuration's header values.
//
// On an error it removes what it made, and only that: the download takes a
// while after the emptiness check, and something else may have written to
// dir meanwhile. A file it would overwrite is an error, since files are
// created exclusively.
func unzipAgentBuilderAgentCode(data []byte, dir string) (_ int, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("the agent code isn't a zip: %w", err)
	}
	u := &unpack{}
	defer func() {
		if err != nil {
			u.undo()
		}
	}()
	if err := u.mkdirOutside(dir); err != nil {
		return 0, err
	}
	if u.root, err = os.OpenRoot(dir); err != nil {
		return 0, err
	}
	files := 0
	for _, entry := range zr.File {
		name := path.Clean(entry.Name)
		switch mode := entry.Mode(); {
		case !fs.ValidPath(name):
			err = fmt.Errorf("%s is not a path inside the directory", entry.Name)
		case mode.IsDir():
			err = u.mkdirAll(name)
		case mode.IsRegular():
			err = u.writeFile(name, entry)
			files++
		default:
			err = fmt.Errorf("%s is not a file or a directory", entry.Name)
		}
		if err != nil {
			return 0, fmt.Errorf("unpack agent code: %w", err)
		}
	}
	return files, u.root.Close()
}

// unpack records what an unpack made, so that undo removes that and nothing
// else.
type unpack struct {
	root *os.Root
	// outside are the directories it made up to and including the unpack
	// directory, deepest first.
	outside []string
	// inside are the files and directories it made in root, in the order
	// it made them.
	inside []string
}

// mkdirOutside makes dir and any parents it lacks.
func (u *unpack) mkdirOutside(dir string) error {
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		u.outside = append(u.outside, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	return os.MkdirAll(dir, 0o700)
}

// mkdirAll makes the directory name in root and any parents it lacks.
func (u *unpack) mkdirAll(name string) error {
	if name == "." {
		return nil
	}
	if err := u.mkdirAll(path.Dir(name)); err != nil {
		return err
	}
	err := u.root.Mkdir(name, 0o700)
	if errors.Is(err, fs.ErrExist) {
		if info, statErr := u.root.Lstat(name); statErr == nil && info.IsDir() {
			return nil
		}
	}
	if err == nil {
		u.inside = append(u.inside, name)
	}
	return err
}

// writeFile writes the zip entry to the new file name in root.
func (u *unpack) writeFile(name string, entry *zip.File) error {
	if err := u.mkdirAll(path.Dir(name)); err != nil {
		return err
	}
	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := u.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	u.inside = append(u.inside, name)
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// undo removes what the unpack made, newest first. It removes each file and
// directory on its own, never a tree, and a directory only while it's empty,
// so whatever else is in the unpack directory stays.
func (u *unpack) undo() {
	if u.root != nil {
		for i := len(u.inside) - 1; i >= 0; i-- {
			_ = u.root.Remove(u.inside[i])
		}
		_ = u.root.Close()
	}
	for _, dir := range u.outside {
		_ = os.Remove(dir)
	}
}
