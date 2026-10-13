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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/assistant"
	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// The assistant agent runs lk commands through the agent channel. lk runs
// each one as a subprocess of itself, in the user's project directory, with
// the assistant's credentials.
//
// Commands that change something need the user's confirmation, by voice or
// on screen. The tool that proposes one shows a confirmation card and
// returns right away with its ID, so the agent can ask the user. If the
// user agrees by voice, the agent calls confirm_action, which runs the
// command and returns its result. If they click the card instead, lk runs
// the command and tells the agent the result.

// toolState tracks lk tool requests. It's only used from the session's run
// loop; background work reports back through results.
type toolState struct {
	ctx      context.Context
	dir      string   // the user's project directory
	creds    []string // LIVEKIT_* environment for subprocesses
	results  chan toolResult
	pending  map[string]pendingTool // confirmation id → command waiting for it
	confirmN int
	// lastProject is the project created this session, for try_project.
	lastProject string
}

// pendingTool is a command waiting for the user to confirm it.
type pendingTool struct {
	args    []string
	command string // as shown to the user
	project string // the project the command creates, if any
	// start, if set, runs instead of the lk command.
	start func(requestID, confirmID string)
}

// toolResult is the outcome of background work, handled in the run loop.
type toolResult struct {
	requestID string // the agent's request to reply to, if any
	confirmID string // the confirmation card to update, if any
	command   string
	project   string // the project the command created, if any
	output    string
	err       error
	// then, if set, handles the result instead of the default reply.
	then func(toolResult)
}

// toolOutputLimit bounds how much command output goes back to the LLM.
const toolOutputLimit = 6000

// Confirmation options in the overlay.
const (
	confirmRun    = "run"
	confirmCancel = "cancel"
)

type (
	overlayConfirm struct {
		Type    string                 `json:"type"`
		Turn    string                 `json:"turn"`
		ID      string                 `json:"id"`
		Heading string                 `json:"heading"`
		Command string                 `json:"command"` // as the user would type it
		Detail  string                 `json:"detail"`  // where it runs and what it changes
		Options []overlayConfirmOption `json:"options"`
	}
	overlayConfirmOption struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	overlayConfirmStatus struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"` // running, done, failed, or cancelled
		Text   string `json:"text,omitempty"`
	}
)

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// runTool handles an lk tool request from the assistant agent.
func (s *assistantSession) runTool(requestID, name string, args map[string]any) {
	str := func(key string) string {
		v, _ := args[key].(string)
		return strings.TrimSpace(v)
	}
	flag := func(key string) bool {
		v, _ := args[key].(bool)
		return v
	}
	switch name {
	case "get_project_info":
		s.replyTool(requestID, s.projectInfo(), nil)

	case "list_cloud_agents":
		s.startCommand(requestID, "", []string{"agent", "list", "--json"})

	case "list_templates":
		s.inBackground(func(ctx context.Context) toolResult {
			templates, err := bootstrap.FetchTemplates(ctx)
			if err != nil {
				return toolResult{requestID: requestID, err: err}
			}
			return toolResult{requestID: requestID, output: describeTemplates(templates)}
		})

	case "create_project":
		s.proposeCreateProject(requestID, str("template"), str("name"), flag("in_current_directory"), flag("confirmed"))

	case "set_up_coding_agent":
		if setup := checkCodingAgentSetup(s.tools.dir); setup.Ready && !flag("reinstall") {
			s.replyTool(requestID, "Nothing was installed: LiveKit's skills and the Docs MCP server are already set up. "+
				"Tell the user, and that \"lk skills update\" gets the latest skills. If they still want to reinstall, "+
				"call set_up_coding_agent again with reinstall=true. Current setup: "+setup.JSON(), nil)
			return
		}
		detail := "Adds LiveKit's skills and the Docs MCP server to the coding agents it finds, for this folder only."
		cmdArgs := []string{"skills", "install", "--json", "-y"}
		if flag("global") {
			detail = "Adds LiveKit's skills and the Docs MCP server to the coding agents it finds, for all your projects."
			cmdArgs = append(cmdArgs, "--global")
		}
		s.proposeCommand(requestID, detail, "Install", cmdArgs, "", flag("confirmed"))

	case "confirm_action":
		s.confirmByVoice(requestID, str("confirmation_id"), flag("approved"))

	case "copy_to_clipboard":
		s.copyByVoice(requestID, str("title"))

	case "try_project":
		s.proposeTryProject(requestID, flag("confirmed"))

	default:
		s.replyTool(requestID, "", fmt.Errorf("unknown tool %q", name))
	}
}

// proposeCreateProject proposes creating a project from a template: agent
// templates with lk agent init, others (frontends and apps) with lk app
// create.
func (s *assistantSession) proposeCreateProject(requestID, template, name string, here, confirmed bool) {
	dir := s.tools.dir
	if here {
		if empty, err := util.IsEmptyDir(dir); err != nil || !empty {
			s.replyTool(requestID, "", errors.New("the current directory isn't empty, so the project can't be created in it; create it in a new folder instead"))
			return
		}
	} else if !projectNameRe.MatchString(name) {
		s.replyTool(requestID, "", fmt.Errorf("%q isn't a valid project name: use lowercase letters, numbers, and dashes", name))
		return
	}

	s.inBackground(func(ctx context.Context) toolResult {
		templates, err := bootstrap.FetchTemplates(ctx)
		if err != nil {
			return toolResult{requestID: requestID, err: err}
		}
		i := slices.IndexFunc(templates, func(t bootstrap.Template) bool {
			return t.Name == template && !t.IsHidden && !t.IsSandbox
		})
		if i < 0 {
			return toolResult{requestID: requestID, err: fmt.Errorf("there's no template named %q; call list_templates for the options", template)}
		}
		tpl := templates[i]
		return toolResult{requestID: requestID, then: func(toolResult) {
			target, where, project := name, "in a new folder, "+tildePath(filepath.Join(dir, name)), filepath.Join(dir, name)
			if here {
				target, where, project = ".", fmt.Sprintf("in this folder, named after it (%s)", filepath.Base(dir)), dir
			}
			kind, cmdArgs := "an app", []string{"app", "create"}
			if isAgentTemplate(tpl) {
				kind, cmdArgs = "an agent project", []string{"agent", "init"}
			}
			cmdArgs = append(cmdArgs, target, "--template", tpl.Name, "--install", "-y")
			if !isAgentTemplate(tpl) {
				project = ""
			}
			s.proposeCommand(requestID,
				fmt.Sprintf("Creates %s from the %s template %s.", kind, tpl.Name, where),
				"Create", cmdArgs, project, confirmed)
		}}
	})
}

// isAgentTemplate reports whether a template is an agent (Python or Node.js)
// rather than a frontend or app.
func isAgentTemplate(t bootstrap.Template) bool {
	return slices.Contains(t.Tags, "agents") &&
		(slices.Contains(t.Tags, "python") || slices.Contains(t.Tags, "node.js"))
}

// describeTemplates lists the templates a user can create, for the LLM.
func describeTemplates(templates []bootstrap.Template) string {
	var agents, apps []string
	for _, t := range templates {
		if t.IsHidden || t.IsSandbox {
			continue
		}
		line := fmt.Sprintf("- %s: %s", t.Name, strings.TrimSpace(t.Desc))
		if isAgentTemplate(t) {
			agents = append(agents, line)
		} else {
			apps = append(apps, line)
		}
	}
	return "Agent templates:\n" + strings.Join(agents, "\n") +
		"\n\nFrontend and app templates:\n" + strings.Join(apps, "\n")
}

// proposeCommand shows a confirmation card for a command and replies to the
// agent right away with the confirmation ID, which it returns.
func (s *assistantSession) proposeCommand(requestID, detail, runLabel string, args []string, project string, confirmed bool) {
	t := &s.tools
	t.confirmN++
	id := fmt.Sprintf("confirm%d", t.confirmN)
	command := displayCommand(args)
	t.pending[id] = pendingTool{args: args, command: command, project: project}
	s.agentTurnForReply()
	s.out.Send(overlayConfirm{
		Type: "confirm", Turn: s.agentTurn, ID: id,
		Heading: "Run in " + tildePath(t.dir),
		Command: command,
		Detail:  detail,
		Options: []overlayConfirmOption{{ID: confirmRun, Name: runLabel}, {ID: confirmCancel, Name: "Cancel"}},
	})
	if confirmed {
		// The user already agreed by voice: run it, and reply when it's done.
		s.confirmByVoice(requestID, id, true)
		return
	}
	s.replyTool(requestID, fmt.Sprintf(
		"Waiting for the user to confirm. Confirmation ID: %s. Command: %s. %s "+
			"The command is on screen, so don't say it. Ask the user in a few words whether to go ahead. They can answer by voice or "+
			"on screen. If they agree by voice, call confirm_action with this ID and approved=true; "+
			"if they decline, approved=false. If they answer on screen, you'll be told the result.",
		id, command, detail), nil)
}

// confirmByVoice handles confirm_action: the user answered by voice.
func (s *assistantSession) confirmByVoice(requestID, id string, approved bool) {
	p, ok := s.tools.pending[id]
	if !ok {
		s.replyTool(requestID, "", fmt.Errorf("there's no pending confirmation %q; it may have been answered on screen already", id))
		return
	}
	delete(s.tools.pending, id)
	option := confirmCancel
	if approved {
		option = confirmRun
	}
	s.out.Send(map[string]string{"type": "confirm_done", "id": id, "option": option})
	if !approved {
		s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: id, Status: "cancelled"})
		s.replyTool(requestID, "Cancelled. Nothing was changed.", nil)
		return
	}
	s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: id, Status: "running"})
	s.runConfirmed(requestID, id, p)
}

// confirmTool handles the user's answer on screen.
func (s *assistantSession) confirmTool(id, option string) {
	p, ok := s.tools.pending[id]
	if !ok {
		return
	}
	delete(s.tools.pending, id)
	s.out.Send(map[string]string{"type": "confirm_done", "id": id, "option": option})
	if option != confirmRun {
		s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: id, Status: "cancelled"})
		s.tellUser(fmt.Sprintf("The user cancelled %s on screen. Nothing was changed. Acknowledge it in a few words.", p.command))
		return
	}
	s.out.Send(overlayConfirmStatus{Type: "confirm_status", ID: id, Status: "running"})
	// No agent request waits for this result; toolFinished tells the agent.
	s.runConfirmed("", id, p)
}

func (s *assistantSession) runConfirmed(requestID, confirmID string, p pendingTool) {
	if p.start != nil {
		p.start(requestID, confirmID)
		return
	}
	t := &s.tools
	s.inBackground(func(ctx context.Context) toolResult {
		output, err := runLK(ctx, t.dir, t.creds, p.args)
		return toolResult{requestID: requestID, confirmID: confirmID, command: p.command, project: p.project, output: output, err: err}
	})
}

// startCommand runs an lk command in the background and replies to the
// agent's request with its output.
func (s *assistantSession) startCommand(requestID, confirmID string, args []string) {
	s.runConfirmed(requestID, confirmID, pendingTool{args: args, command: displayCommand(args)})
}

// inBackground runs fn off the run loop and hands its result to toolFinished.
func (s *assistantSession) inBackground(fn func(ctx context.Context) toolResult) {
	t := &s.tools
	go func() {
		r := fn(t.ctx)
		select {
		case t.results <- r:
		case <-t.ctx.Done():
		}
	}()
}

func (s *assistantSession) toolFinished(r toolResult) {
	if r.then != nil {
		r.then(r)
		return
	}
	if r.err == nil && r.project != "" {
		s.tools.lastProject = r.project
	}
	if r.confirmID != "" {
		status := overlayConfirmStatus{Type: "confirm_status", ID: r.confirmID, Status: "done"}
		if r.err != nil {
			status.Status = "failed"
			status.Text = lastLine(r.err.Error())
		}
		s.out.Send(status)
	}
	if r.requestID != "" {
		s.replyTool(r.requestID, r.output, r.err)
		return
	}
	// Confirmed on screen: no request waits, so tell the agent what happened.
	if r.err != nil {
		s.tellUser(fmt.Sprintf("The user confirmed %s on screen, but it failed: %s. Tell them briefly.", r.command, truncate(r.err.Error(), 1500)))
		return
	}
	s.tellUser(fmt.Sprintf("The user confirmed %s on screen, and it finished. Its output:\n%s\nTell them briefly what happened and the next step.", r.command, truncate(r.output, 1500)))
}

func (s *assistantSession) replyTool(requestID, output string, err error) {
	msg := map[string]any{"type": "reply", "id": requestID, "ok": err == nil, "output": output}
	if err != nil {
		msg["error"] = err.Error()
	}
	s.channel.send(msg)
}

// tellUser asks the assistant agent to tell the user something.
func (s *assistantSession) tellUser(instructions string) {
	s.channel.send(map[string]string{"type": "say", "instructions": instructions})
}

// displayCommand shows an lk command as the user would type it, without the
// flags lk adds to run it non-interactively.
func displayCommand(args []string) string {
	shown := []string{"lk"}
	for _, a := range args {
		if a != "-y" && a != "--json" {
			shown = append(shown, a)
		}
	}
	return strings.Join(shown, " ")
}

// tildePath shortens a path in the user's home directory to start with ~.
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

// lastLine returns the last non-empty line of s, which for a failed command
// is usually its error.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// lkExecutable finds the lk binary that tools run. It's a variable for tests.
var lkExecutable = os.Executable

// ansiRe matches terminal escape codes: colors (CSI) and hyperlinks (OSC 8).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// runLK runs this lk binary with args in dir and returns its output.
func runLK(ctx context.Context, dir string, creds, args []string) (string, error) {
	exe, err := lkExecutable()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), creds...)
	out, err := cmd.CombinedOutput()
	text := truncate(strings.TrimSpace(ansiRe.ReplaceAllString(string(out), "")), toolOutputLimit)
	if err != nil {
		return "", fmt.Errorf("%s failed: %w\n%s", displayCommand(args), err, text)
	}
	return text, nil
}

// projectInfo describes the user's LiveKit Cloud project and their current
// directory, as JSON for the LLM.
func (s *assistantSession) projectInfo() string {
	info := map[string]any{
		"directory":     s.tools.dir,
		"folder":        assistant.DescribeFolder(s.tools.dir),
		"coding_agents": checkCodingAgentSetup(s.tools.dir),
	}
	if s.cmd != nil {
		if rp, err := resolveProject(s.cmd, loadParams{requireURL: true}); err == nil && rp.project != nil {
			info["livekit_project"] = map[string]string{"name": rp.project.Name, "url": rp.project.URL}
		}
	}
	if root, projectType, err := agentfs.DetectProjectRoot(s.tools.dir); err == nil {
		local := map[string]any{"root": root, "type": string(projectType)}
		if toml, ok, err := config.LoadTOMLFile(root, config.LiveKitTOMLFile); err == nil && ok && toml.HasAgent() {
			local["deployed_agent_id"] = toml.Agent.ID
		}
		info["agent_project"] = local
	} else {
		info["agent_project"] = nil
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	return string(b)
}
