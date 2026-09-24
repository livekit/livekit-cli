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
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/skills"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	skillsGlobalFlag = &cli.BoolFlag{
		Name:    "global",
		Aliases: []string{"g"},
		Usage:   "Use your user-level agent directories instead of the current project",
	}
	skillsAgentFlag = &cli.StringSliceFlag{
		Name:    "agent",
		Aliases: []string{"a"},
		Usage:   "Coding `AGENT` to target; repeat for several (" + strings.Join(skills.AgentIDs(), ", ") + ")",
	}
	skillsForceFlag = &cli.BoolFlag{
		Name:  "force",
		Usage: "Overwrite skills you've edited locally",
	}
	// --ref installs from another branch of livekit/agent-skills, for trying
	// skill changes before they merge.
	skillsRefFlag = &cli.StringFlag{
		Name:   "ref",
		Value:  skills.DefaultRef,
		Hidden: true,
	}

	SkillsCommands = []*cli.Command{
		{
			Name:  "skills",
			Usage: "Install LiveKit's skills and Docs MCP server into your coding agents",
			Description: `Skills teach coding agents (Claude Code, Codex, Cursor, and others) how to
build, test, and debug LiveKit agents. The LiveKit Docs MCP server gives them
current API reference. Skills come from github.com/livekit/agent-skills.

Skills are copied into each agent's skills directory; agents that share
.agents/skills get one copy. Installs are recorded in skills-lock.json (or
~/.agents/.skill-lock.json with --global), the same lock file "npx skills" and
"gh skill" use, so commit it along with the skills.

  lk skills install          # all skills + Docs MCP, for detected agents
  lk skills list             # what's installed and whether it's current
  lk skills update           # pull the latest skills
  lk skills remove           # uninstall LiveKit's skills`,
			Commands: []*cli.Command{
				{
					Name:      "install",
					Aliases:   []string{"add"},
					Usage:     "Install skills and the Docs MCP server",
					ArgsUsage: "[SKILL ...]",
					Description: `Installs every LiveKit skill, or just the ones named, for the coding agents
detected on this machine (or those passed with --agent), and adds the LiveKit
Docs MCP server to each agent's MCP config.

Skills you've edited locally are left alone unless you pass --force. Skills
LiveKit no longer publishes are removed. LiveKit skills that lk didn't install
(such as copies committed to a starter template) are replaced once you confirm.`,
					Flags: []cli.Flag{
						skillsAgentFlag,
						skillsGlobalFlag,
						&cli.BoolFlag{
							Name:  "skip-mcp",
							Usage: "Don't add the Docs MCP server to agent configs",
						},
						skillsForceFlag,
						jsonFlag,
						skillsRefFlag,
					},
					Action: skillsInstall,
				},
				{
					Name:    "list",
					Aliases: []string{"ls"},
					Usage:   "Show available skills, where they're installed, and Docs MCP setup",
					Flags: []cli.Flag{
						skillsGlobalFlag,
						jsonFlag,
						skillsRefFlag,
					},
					Action: skillsList,
				},
				{
					Name:      "update",
					Aliases:   []string{"upgrade"},
					Usage:     "Update installed skills to the latest version",
					ArgsUsage: "[SKILL ...]",
					Description: `Brings every skills directory that has LiveKit skills in line with the
published set: outdated skills are replaced, new skills are added, and skills
LiveKit no longer publishes are removed. Name skills to update only those.

Skills you've edited locally are left alone unless you pass --force. LiveKit
skills that lk didn't install (such as copies committed to a starter template)
are replaced once you confirm.`,
					Flags: []cli.Flag{
						skillsGlobalFlag,
						skillsForceFlag,
						jsonFlag,
						skillsRefFlag,
					},
					Action: skillsUpdate,
				},
				{
					Name:      "remove",
					Aliases:   []string{"rm", "uninstall"},
					Usage:     "Remove LiveKit skills",
					ArgsUsage: "[SKILL ...]",
					Description: `Removes every LiveKit skill, or just the ones named, from all agents'
skills directories (or only those of --agent). MCP config is left as is.`,
					Flags: []cli.Flag{
						skillsAgentFlag,
						skillsGlobalFlag,
						jsonFlag,
					},
					Action: skillsRemove,
				},
			},
		},
	}
)

// skillsSession is the context shared by every skills subcommand.
type skillsSession struct {
	env   *skills.Env
	scope skills.Scope
	json  bool
}

func newSkillsSession(cmd *cli.Command) (*skillsSession, error) {
	env, err := skills.DefaultEnv()
	if err != nil {
		return nil, err
	}
	s := &skillsSession{env: env, json: cmd.Bool("json")}
	if cmd.Bool("global") {
		s.scope = skills.ScopeGlobal
	}
	return s, nil
}

func (s *skillsSession) fetch(ctx context.Context, ref string) (*skills.Bundle, error) {
	var b *skills.Bundle
	err := out.Await("Fetching skills from "+skills.SourceRepo+"...", ctx, func(ctx context.Context) error {
		var err error
		b, err = skills.Fetch(ctx, &http.Client{Timeout: 30 * time.Second}, ref)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, msg := range b.Skipped {
		out.Warnf("Skipping invalid upstream skill: %s", msg)
	}
	return b, nil
}

// display shortens p for messages: relative to the project, or ~-prefixed.
func (s *skillsSession) display(p string) string {
	if s.scope == skills.ScopeProject {
		if rel, err := filepath.Rel(s.env.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if rel, err := filepath.Rel(s.env.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// allDirs is every skills directory any known agent reads in this scope.
func (s *skillsSession) allDirs() []skills.SkillsDir {
	return skills.GroupSkillsDirs(s.env, s.scope, skills.Agents)
}

// agentsFromFlag resolves --agent, or returns nil when it wasn't passed.
func agentsFromFlag(cmd *cli.Command) ([]*skills.Agent, error) {
	var agents []*skills.Agent
	for _, v := range cmd.StringSlice("agent") {
		for id := range strings.SplitSeq(v, ",") {
			id = strings.TrimSpace(id)
			a := skills.FindAgent(id)
			if a == nil {
				return nil, fmt.Errorf("unknown agent %q; choose from %s", id, strings.Join(skills.AgentIDs(), ", "))
			}
			if !slices.Contains(agents, a) {
				agents = append(agents, a)
			}
		}
	}
	return agents, nil
}

// chooseAgents resolves --agent, else asks (interactive) or uses the detected
// agents.
func (s *skillsSession) chooseAgents(cmd *cli.Command) ([]*skills.Agent, error) {
	agents, err := agentsFromFlag(cmd)
	if err != nil || len(agents) > 0 {
		return agents, err
	}
	detected := skills.DetectAgents(s.env)
	if SkipPrompts(cmd) {
		if len(detected) == 0 {
			return nil, fmt.Errorf("no coding agents detected; pass --agent (%s)", strings.Join(skills.AgentIDs(), ", "))
		}
		out.Statusf("Detected %s", skills.AgentNames(detected))
		return detected, nil
	}
	agents, err = pickAgents("Install for which coding agents?", "Detected agents are preselected", detected)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		return nil, errors.New("no agents selected")
	}
	return agents, nil
}

// pickAgents asks which agents to install for, with preselected checked. It
// may return none.
func pickAgents(title, description string, preselected []*skills.Agent) ([]*skills.Agent, error) {
	var ids []string
	options := make([]huh.Option[string], len(skills.Agents))
	for i, a := range skills.Agents {
		options[i] = huh.NewOption(a.Name, a.ID).Selected(slices.Contains(preselected, a))
	}
	if err := huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().
		Title(title).
		Description(description).
		Options(options...).
		// Leave room for the title and description rows (see token.go).
		Height(len(options) + 2).
		Value(&ids).
		WithTheme(util.FormTheme()))).
		Run(); err != nil {
		return nil, err
	}
	var agents []*skills.Agent
	for _, id := range ids {
		agents = append(agents, skills.FindAgent(id))
	}
	return agents, nil
}

// skillResult is one change (or non-change) to one copy of a skill.
type skillResult struct {
	Skill   string   `json:"skill"`
	Version string   `json:"version,omitempty"`
	Path    string   `json:"path"`
	Agents  []string `json:"agents"`
	// Action is installed, updated, unchanged, removed, or skipped (edited
	// locally; see --force). Skills LiveKit no longer publishes are removed,
	// or skipped if edited.
	Action string `json:"action"`
}

type mcpResult struct {
	Agent string `json:"agent"`
	State string `json:"state"`
	Path  string `json:"path,omitempty"`
	Added bool   `json:"added,omitempty"`
	Error string `json:"error,omitempty"`
}

type skillsChangeOutput struct {
	Scope    string        `json:"scope"`
	Ref      string        `json:"ref,omitempty"`
	Commit   string        `json:"commit,omitempty"`
	Skills   []skillResult `json:"skills"`
	MCP      []mcpResult   `json:"mcp,omitempty"`
	LockFile string        `json:"lock_file,omitempty"`
}

func agentIDs(agents []*skills.Agent) []string {
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	return ids
}

// confirmUntracked asks before replacing LiveKit skills that no lock file
// records (typically copies committed to a starter template), since lk can't
// tell whether they were edited. Without a terminal, or with --force or
// --yes, it proceeds.
func confirmUntracked(cmd *cli.Command, s *skillsSession, copies []skills.Copy) (bool, error) {
	var paths []string
	for _, c := range copies {
		if c.State == skills.StateUntracked {
			paths = append(paths, s.display(c.Path()))
		}
	}
	if len(paths) == 0 || cmd.Bool("force") || SkipPrompts(cmd) {
		return true, nil
	}
	ok := true
	if err := huh.NewForm(huh.NewGroup(util.Confirm().
		Title("Replace LiveKit skills that lk didn't install?").
		Description("These may be older copies (e.g. from a starter template), or have local edits:\n" + strings.Join(paths, "\n")).
		Value(&ok).
		WithTheme(util.FormTheme()))).
		Run(); err != nil {
		return false, err
	}
	return ok, nil
}

// sync applies the upstream bundle to copies: writes missing and outdated
// ones (and edited ones with force, untracked ones with replaceUntracked) and
// deletes skills LiveKit no longer publishes. It records results in the lock
// but doesn't save it.
func (s *skillsSession) sync(in *skills.Installer, copies []skills.Copy, force, replaceUntracked bool) ([]skillResult, error) {
	replaceUntracked = replaceUntracked || force
	results := []skillResult{}
	installed := map[string]bool{}
	var removed []string
	for _, c := range copies {
		r := skillResult{Skill: c.Skill, Path: s.display(c.Path()), Agents: agentIDs(c.Dir.Agents)}
		if c.Upstream != nil {
			r.Version = c.Upstream.Version
		}
		switch {
		case c.State == skills.StateCurrent:
			r.Action = "unchanged"
			installed[c.Skill] = true
		case c.State == skills.StateUntracked && !replaceUntracked:
			r.Action = "skipped"
		case c.State == skills.StateRemoved || (c.Upstream == nil && (force || c.State == skills.StateUntracked)):
			if err := in.Delete(c); err != nil {
				return nil, err
			}
			r.Action, r.Version = "removed", c.Version
			removed = append(removed, c.Skill)
		case c.State == skills.StateModified && !force:
			r.Action = "skipped"
		default:
			if err := in.Write(c); err != nil {
				return nil, fmt.Errorf("installing %s: %w", c.Skill, err)
			}
			r.Action = "installed"
			if c.State != skills.StateMissing {
				r.Action = "updated"
			}
			installed[c.Skill] = true
		}
		results = append(results, r)
	}
	for name := range installed {
		in.Record(in.Bundle.Find(name))
	}
	for _, name := range removed {
		if err := in.Forget(name); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (s *skillsSession) printResults(results []skillResult) {
	// One line per skill and action, listing the directories it applied to.
	type group struct {
		skill, action, version string
		dirs                   []string
	}
	var groups []*group
	var skipped []string
	for _, r := range results {
		switch r.Action {
		case "unchanged":
			continue
		case "skipped":
			skipped = append(skipped, r.Path)
			continue
		}
		i := slices.IndexFunc(groups, func(g *group) bool { return g.skill == r.Skill && g.action == r.Action })
		if i < 0 {
			groups = append(groups, &group{skill: r.Skill, action: r.Action, version: r.Version})
			i = len(groups) - 1
		}
		groups[i].dirs = append(groups[i].dirs, filepath.Dir(r.Path))
	}
	for _, g := range groups {
		version := ""
		if g.version != "" {
			version = " " + g.version
		}
		verb := strings.ToUpper(g.action[:1]) + g.action[1:]
		arrow := "→ "
		if g.action == "removed" {
			arrow = "from "
		}
		out.Statusf("%s %s%s %s", verb, util.Accented(g.skill), version, util.Dimmed(arrow+strings.Join(g.dirs, ", ")))
	}
	if len(skipped) > 0 {
		out.Warnf("Left skills you've edited alone (pass --force to overwrite): %s", strings.Join(skipped, ", "))
	}
}

func (s *skillsSession) printChange(o *skillsChangeOutput) error {
	if s.json {
		util.PrintJSON(o)
		return nil
	}
	s.printResults(o.Skills)
	if !slices.ContainsFunc(o.Skills, func(r skillResult) bool { return r.Action != "unchanged" && r.Action != "skipped" }) {
		out.Statusf("Skills are up to date")
	}
	// One line for all agents: paths are in --json and lk skills list.
	var added, manual, already []string
	for _, m := range o.MCP {
		switch skills.MCPState(m.State) {
		case skills.MCPConfigured:
			if m.Added {
				added = append(added, m.Agent)
			} else {
				already = append(already, m.Agent)
			}
		case skills.MCPCustom:
			out.Warnf("%s already has an MCP server named %q pointing elsewhere; left it alone (%s)", m.Agent, skills.MCPServerName, m.Path)
		case skills.MCPUnsupported:
			manual = append(manual, m.Agent)
		default:
			out.Warnf("Couldn't configure the Docs MCP server for %s: %s", m.Agent, m.Error)
		}
	}
	if len(added) > 0 {
		out.Statusf("Added the Docs MCP server for %s", strings.Join(added, ", "))
	}
	if len(already) > 0 {
		out.Statusf("Docs MCP server already set up for %s", strings.Join(already, ", "))
	}
	if len(manual) > 0 {
		out.Statusf("Add the Docs MCP server (%s) to %s by hand: %s", skills.MCPServerURL, strings.Join(manual, ", "), skills.MCPDocsURL)
	}
	return nil
}

func skillsInstall(ctx context.Context, cmd *cli.Command) error {
	s, err := newSkillsSession(cmd)
	if err != nil {
		return err
	}
	agents, err := s.chooseAgents(cmd)
	if err != nil {
		return err
	}
	o, err := s.install(ctx, installRequest{
		agents:  agents,
		names:   cmd.Args().Slice(),
		ref:     cmd.String("ref"),
		force:   cmd.Bool("force"),
		skipMCP: cmd.Bool("skip-mcp"),
		confirm: func(copies []skills.Copy) (bool, error) { return confirmUntracked(cmd, s, copies) },
	})
	if err != nil {
		return err
	}
	return s.printChange(o)
}

type installRequest struct {
	agents  []*skills.Agent
	names   []string // empty for every published skill
	ref     string
	force   bool
	skipMCP bool
	// confirm approves replacing untracked LiveKit skills; nil approves.
	confirm func([]skills.Copy) (bool, error)
}

// install writes skills for req.agents and adds the Docs MCP server to them.
func (s *skillsSession) install(ctx context.Context, req installRequest) (*skillsChangeOutput, error) {
	bundle, err := s.fetch(ctx, req.ref)
	if err != nil {
		return nil, err
	}
	names := req.names
	for _, n := range names {
		if bundle.Find(n) == nil {
			return nil, fmt.Errorf("no skill named %q; available: %s", n, strings.Join(bundle.Names(), ", "))
		}
	}
	if len(names) == 0 {
		names = bundle.Names()
	}

	in, err := skills.NewInstaller(s.env, s.scope, bundle)
	if err != nil {
		return nil, err
	}
	targets := skills.GroupSkillsDirs(s.env, s.scope, req.agents)
	all, err := in.Inspect(names, s.allDirs())
	if err != nil {
		return nil, err
	}
	// Install into the chosen agents' directories. Unedited copies elsewhere
	// are refreshed too: the lock holds one hash per skill, so leaving them
	// stale would make them look edited from now on.
	var copies []skills.Copy
	for _, c := range all {
		if slices.ContainsFunc(targets, func(d skills.SkillsDir) bool { return d.Path == c.Dir.Path }) || c.State == skills.StateOutdated {
			copies = append(copies, c)
		}
	}
	// Clean up skills LiveKit has since removed or renamed, wherever they are.
	orphanNames, err := in.Orphans(s.allDirs())
	if err != nil {
		return nil, err
	}
	orphans, err := in.Inspect(orphanNames, s.allDirs())
	if err != nil {
		return nil, err
	}
	for _, c := range orphans {
		if c.State != skills.StateMissing {
			copies = append(copies, c)
		}
	}
	replace := true
	if req.confirm != nil {
		if replace, err = req.confirm(copies); err != nil {
			return nil, err
		}
	}
	results, err := s.sync(in, copies, req.force, replace)
	if err != nil {
		return nil, err
	}
	if err := in.Save(); err != nil {
		return nil, fmt.Errorf("writing %s: %w", in.Lock.Path, err)
	}

	o := &skillsChangeOutput{
		Scope: s.scope.String(), Ref: bundle.Ref, Commit: bundle.Commit,
		Skills: results, LockFile: s.display(in.Lock.Path),
	}
	if !req.skipMCP {
		for _, a := range req.agents {
			st, err := skills.ConfigureMCP(ctx, s.env, s.scope, a)
			m := mcpResult{Agent: a.ID, State: string(st.State), Added: st.Added}
			if st.Path != "" {
				m.Path = s.display(st.Path)
			}
			if err != nil {
				m.State, m.Error = "error", err.Error()
			}
			if !s.json {
				m.Agent = a.Name
			}
			o.MCP = append(o.MCP, m)
		}
	}
	return o, nil
}

func skillsUpdate(ctx context.Context, cmd *cli.Command) error {
	s, err := newSkillsSession(cmd)
	if err != nil {
		return err
	}
	bundle, err := s.fetch(ctx, cmd.String("ref"))
	if err != nil {
		return err
	}
	in, err := skills.NewInstaller(s.env, s.scope, bundle)
	if err != nil {
		return err
	}
	dirs := s.allDirs()
	installed, err := in.InstalledLiveKitSkills(dirs)
	if err != nil {
		return err
	}
	names := cmd.Args().Slice()
	for _, n := range names {
		if !slices.Contains(installed, n) {
			return fmt.Errorf("%s isn't installed; see lk skills list", n)
		}
	}
	targeted := len(names) > 0
	if !targeted {
		names = installed
	}
	current, err := in.Inspect(names, dirs)
	if err != nil {
		return err
	}
	// Directories that hold any LiveKit skill get the whole published set,
	// so renamed and newly published skills arrive with the update.
	var withSkills []skills.SkillsDir
	var copies []skills.Copy
	for _, c := range current {
		if c.State == skills.StateMissing {
			continue
		}
		copies = append(copies, c)
		if !slices.ContainsFunc(withSkills, func(d skills.SkillsDir) bool { return d.Path == c.Dir.Path }) {
			withSkills = append(withSkills, c.Dir)
		}
	}
	if len(copies) == 0 {
		return errors.New("no LiveKit skills are installed here; run lk skills install")
	}
	if !targeted {
		var added []string
		for _, n := range bundle.Names() {
			if !slices.Contains(names, n) {
				added = append(added, n)
			}
		}
		fresh, err := in.Inspect(added, withSkills)
		if err != nil {
			return err
		}
		copies = append(copies, fresh...)
	}
	replace, err := confirmUntracked(cmd, s, copies)
	if err != nil {
		return err
	}
	results, err := s.sync(in, copies, cmd.Bool("force"), replace)
	if err != nil {
		return err
	}
	if err := in.Save(); err != nil {
		return fmt.Errorf("writing %s: %w", in.Lock.Path, err)
	}
	return s.printChange(&skillsChangeOutput{
		Scope: s.scope.String(), Ref: bundle.Ref, Commit: bundle.Commit,
		Skills: results, LockFile: s.display(in.Lock.Path),
	})
}

type skillsListCopy struct {
	Path    string   `json:"path"`
	Agents  []string `json:"agents"`
	State   string   `json:"state"`
	Version string   `json:"version,omitempty"`
}

type skillsListSkill struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Version     string           `json:"version,omitempty"`
	Published   bool             `json:"published"`
	Installed   []skillsListCopy `json:"installed"`
}

type skillsListOutput struct {
	Scope  string            `json:"scope"`
	Ref    string            `json:"ref"`
	Commit string            `json:"commit,omitempty"`
	Skills []skillsListSkill `json:"skills"`
	MCP    []mcpResult       `json:"mcp"`
}

func skillsList(ctx context.Context, cmd *cli.Command) error {
	s, err := newSkillsSession(cmd)
	if err != nil {
		return err
	}
	bundle, err := s.fetch(ctx, cmd.String("ref"))
	if err != nil {
		return err
	}
	in, err := skills.NewInstaller(s.env, s.scope, bundle)
	if err != nil {
		return err
	}
	dirs := s.allDirs()
	installed, err := in.InstalledLiveKitSkills(dirs)
	if err != nil {
		return err
	}
	names := bundle.Names()
	for _, n := range installed {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	copies, err := in.Inspect(names, dirs)
	if err != nil {
		return err
	}

	o := &skillsListOutput{Scope: s.scope.String(), Ref: bundle.Ref, Commit: bundle.Commit, Skills: []skillsListSkill{}}
	for _, n := range names {
		sk := skillsListSkill{Name: n, Installed: []skillsListCopy{}}
		if up := bundle.Find(n); up != nil {
			sk.Description, sk.Version, sk.Published = up.Description, up.Version, true
		}
		for _, c := range copies {
			if c.Skill == n && c.State != skills.StateMissing {
				sk.Installed = append(sk.Installed, skillsListCopy{
					Path: s.display(c.Path()), Agents: agentIDs(c.Dir.Agents), State: string(c.State), Version: c.Version,
				})
			}
		}
		o.Skills = append(o.Skills, sk)
	}
	// MCP status for agents that are installed or already configured.
	detected := skills.DetectAgents(s.env)
	for _, a := range skills.Agents {
		st := skills.CheckMCP(s.env, s.scope, a)
		if !slices.Contains(detected, a) && st.State != skills.MCPConfigured {
			continue
		}
		m := mcpResult{Agent: a.ID, State: string(st.State)}
		if st.Path != "" {
			m.Path = s.display(st.Path)
		}
		if st.Err != nil {
			m.State, m.Error = "error", st.Err.Error()
		}
		o.MCP = append(o.MCP, m)
	}

	if s.json {
		util.PrintJSON(o)
		return nil
	}
	return printSkillsList(o)
}

func printSkillsList(o *skillsListOutput) error {
	t := util.CreateTable().Headers("Skill", "Version", "Installed")
	var outdated, missing bool
	for _, sk := range o.Skills {
		var where []string
		for _, c := range sk.Installed {
			label := filepath.Dir(c.Path)
			if c.State != string(skills.StateCurrent) {
				label += " (" + c.State + ")"
			}
			if c.State == string(skills.StateOutdated) || c.State == string(skills.StateRemoved) || c.State == string(skills.StateUntracked) {
				outdated = true
			}
			where = append(where, label)
		}
		if len(where) == 0 {
			where = []string{"-"}
			missing = true
		}
		version := sk.Version
		if !sk.Published {
			version = "no longer published"
		}
		t.Row(sk.Name, version, strings.Join(where, "\n"))
	}
	out.Result(t)

	if len(o.MCP) > 0 {
		mt := util.CreateTable().Headers("Agent", "Docs MCP", "Config")
		for _, m := range o.MCP {
			name := m.Agent
			if a := skills.FindAgent(m.Agent); a != nil {
				name = a.Name
			}
			state := m.State
			if m.Error != "" {
				state = "unreadable: " + m.Error
			}
			if m.State == string(skills.MCPUnsupported) {
				state = "set up by hand"
				m.Path = skills.MCPDocsURL
			}
			mt.Row(name, state, m.Path)
		}
		out.Result(mt)
	}
	switch {
	case outdated:
		out.Statusf("Run %s to get the latest skills", util.Accented("lk skills update"))
	case missing:
		out.Statusf("Run %s to install skills", util.Accented("lk skills install"))
	}
	return nil
}

func skillsRemove(ctx context.Context, cmd *cli.Command) error {
	s, err := newSkillsSession(cmd)
	if err != nil {
		return err
	}
	agents, err := agentsFromFlag(cmd)
	if err != nil {
		return err
	}
	dirs := s.allDirs()
	if len(agents) > 0 {
		dirs = skills.GroupSkillsDirs(s.env, s.scope, agents)
	}
	// No download: what's installed is known from the lock and the skills'
	// own frontmatter.
	in, err := skills.NewInstaller(s.env, s.scope, nil)
	if err != nil {
		return err
	}
	installed, err := in.InstalledLiveKitSkills(dirs)
	if err != nil {
		return err
	}
	names := cmd.Args().Slice()
	for _, n := range names {
		if !slices.Contains(installed, n) {
			return fmt.Errorf("%s isn't installed; see lk skills list", n)
		}
	}
	if len(names) == 0 {
		names = installed
	}
	copies, err := in.Inspect(names, dirs)
	if err != nil {
		return err
	}
	copies = slices.DeleteFunc(copies, func(c skills.Copy) bool { return c.State == skills.StateMissing })
	if len(copies) == 0 {
		if s.json {
			util.PrintJSON(&skillsChangeOutput{Scope: s.scope.String(), Skills: []skillResult{}})
		} else {
			out.Statusf("No LiveKit skills installed")
		}
		return nil
	}

	if !SkipPrompts(cmd) {
		var paths []string
		for _, c := range copies {
			paths = append(paths, s.display(c.Path()))
		}
		ok := false
		if err := huh.NewForm(huh.NewGroup(util.Confirm().
			Title(fmt.Sprintf("Remove %d skill directories?", len(copies))).
			Description(strings.Join(paths, "\n")).
			Value(&ok).
			WithTheme(util.FormTheme()))).
			Run(); err != nil {
			return err
		}
		if !ok {
			return errors.New("cancelled")
		}
	}

	var results []skillResult
	for _, c := range copies {
		if err := in.Delete(c); err != nil {
			return err
		}
		results = append(results, skillResult{
			Skill: c.Skill, Version: c.Version, Path: s.display(c.Path()), Agents: agentIDs(c.Dir.Agents), Action: "removed",
		})
	}
	for _, n := range names {
		if err := in.Forget(n); err != nil {
			return err
		}
	}
	if err := in.Save(); err != nil {
		return fmt.Errorf("writing %s: %w", in.Lock.Path, err)
	}
	o := &skillsChangeOutput{Scope: s.scope.String(), Skills: results}
	if s.json {
		util.PrintJSON(o)
		return nil
	}
	s.printResults(results)
	return nil
}

// setupProjectSkills installs skills and the Docs MCP server into a freshly
// created agent project, for the coding agents on this machine. It asks first
// unless --skills was passed or there's no terminal; projects that aren't
// LiveKit agents are left alone.
func setupProjectSkills(ctx context.Context, cmd *cli.Command, dir string) error {
	if cmd.IsSet("skills") && !cmd.Bool("skills") {
		return nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !isAgentProject(root) {
		return nil
	}
	env, err := skills.DefaultEnv()
	if err != nil {
		return err
	}
	env.Root = root
	s := &skillsSession{env: env}

	agents := skills.DetectAgents(env)
	if !cmd.IsSet("skills") && !SkipPrompts(cmd) {
		// Detection only means an agent's config directory exists, which a
		// tool tried once also leaves behind; let the user trim the list.
		if agents, err = pickAgents(
			"Install LiveKit skills for which coding agents?",
			"Teaches them to build, test, and debug LiveKit agents, and adds the\nLiveKit Docs MCP server. Detected agents are preselected; select none to skip.",
			agents,
		); err != nil {
			return err
		}
		if len(agents) == 0 {
			return nil
		}
	} else if len(agents) == 0 {
		out.Statusf("No coding agents detected; run %s to add LiveKit skills later", util.Accented("lk skills install --agent AGENT"))
		return nil
	}
	// A new project has no edits to lose, so skills the template shipped are
	// replaced without asking.
	o, err := s.install(ctx, installRequest{agents: agents, ref: skills.DefaultRef})
	if err != nil {
		return err
	}
	return s.printChange(o)
}

// isAgentProject reports whether dir depends on the LiveKit Agents SDK, or
// already ships LiveKit skills.
func isAgentProject(dir string) bool {
	for file, dep := range map[string]string{
		"pyproject.toml":   "livekit-agents",
		"requirements.txt": "livekit-agents",
		"package.json":     "@livekit/agents",
	} {
		if data, err := os.ReadFile(filepath.Join(dir, file)); err == nil && strings.Contains(string(data), dep) {
			return true
		}
	}
	for _, p := range []string{".agents/skills", ".claude/skills"} {
		if entries, err := os.ReadDir(filepath.Join(dir, p)); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}
