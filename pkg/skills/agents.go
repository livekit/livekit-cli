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

// Package skills installs LiveKit's agent skills (https://agentskills.io) and
// the LiveKit Docs MCP server into coding agents such as Claude Code, Codex and
// Cursor.
//
// Installs are plain copies, never symlinks, and are recorded in the same lock
// files the `skills` CLI (github.com/vercel-labs/skills) and `gh skill` use, so
// any of the three tools can manage what another installed.
package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Scope selects where skills and MCP config are written: the current project,
// or the user's home directory.
type Scope int

const (
	ScopeProject Scope = iota
	ScopeGlobal
)

func (s Scope) String() string {
	if s == ScopeGlobal {
		return "global"
	}
	return "project"
}

// Env is the filesystem context an install runs against. Tests point it at
// temporary directories.
type Env struct {
	Home string // user home directory
	Root string // project root (the working directory)
	// Getenv and LookPath default to os.Getenv and exec.LookPath.
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// DefaultEnv returns an Env for the real user and working directory.
func DefaultEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &Env{Home: home, Root: root}, nil
}

func (e *Env) getenv(key string) string {
	if e.Getenv != nil {
		return e.Getenv(key)
	}
	return os.Getenv(key)
}

func (e *Env) lookPath(file string) bool {
	lookPath := e.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath(file)
	return err == nil
}

// homeDir returns $env if set, else ~/fallback.
func (e *Env) homeDir(env, fallback string) string {
	if v := strings.TrimSpace(e.getenv(env)); v != "" {
		return v
	}
	return filepath.Join(e.Home, fallback)
}

func (e *Env) configHome() string {
	return e.homeDir("XDG_CONFIG_HOME", ".config")
}

func (e *Env) claudeHome() string { return e.homeDir("CLAUDE_CONFIG_DIR", ".claude") }
func (e *Env) codexHome() string  { return e.homeDir("CODEX_HOME", ".codex") }

// Agent describes one coding agent: where it reads skills from, how to tell it
// is installed, and how to register an MCP server with it.
type Agent struct {
	ID   string // stable identifier, matching the `skills` CLI's agent names
	Name string // display name

	// projectSkills is relative to the project root. Many agents read the
	// cross-agent .agents/skills directory, so they share one copy.
	projectSkills string
	globalSkills  func(*Env) string

	// markers are paths whose existence means the agent is installed: in the
	// home directory, or (for project dirs) in the project root. bins are
	// executables looked up on PATH.
	markers func(*Env) []string
	bins    []string

	mcp mcpTarget
}

// SkillsDir returns the directory this agent reads skills from in scope.
func (a *Agent) SkillsDir(env *Env, scope Scope) string {
	if scope == ScopeGlobal {
		return a.globalSkills(env)
	}
	return filepath.Join(env.Root, a.projectSkills)
}

// Detected reports whether the agent appears to be installed.
func (a *Agent) Detected(env *Env) bool {
	for _, p := range a.markers(env) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	for _, b := range a.bins {
		if env.lookPath(b) {
			return true
		}
	}
	return false
}

func homePaths(rel ...string) func(*Env) []string {
	return func(e *Env) []string {
		out := make([]string, len(rel))
		for i, r := range rel {
			out[i] = filepath.Join(e.Home, r)
		}
		return out
	}
}

// Agents is every agent lk knows how to install into, in display order. Skill
// paths follow the `skills` CLI's table so installs from either tool land in
// the same place.
var Agents = []*Agent{
	{
		ID: "claude-code", Name: "Claude Code",
		projectSkills: ".claude/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.claudeHome(), "skills") },
		markers:       func(e *Env) []string { return []string{e.claudeHome()} },
		bins:          []string{"claude"},
		mcp:           claudeMCP,
	},
	{
		ID: "codex", Name: "Codex",
		projectSkills: ".agents/skills",
		// Codex reads user skills from ~/.agents/skills (and the older
		// $CODEX_HOME/skills); prefer the cross-agent directory.
		globalSkills: func(e *Env) string { return filepath.Join(e.Home, ".agents", "skills") },
		markers:      func(e *Env) []string { return []string{e.codexHome()} },
		bins:         []string{"codex"},
		mcp:          codexMCP,
	},
	{
		ID: "cursor", Name: "Cursor",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.Home, ".cursor", "skills") },
		markers:       homePaths(".cursor"),
		bins:          []string{"cursor-agent"},
		mcp:           cursorMCP,
	},
	{
		ID: "github-copilot", Name: "GitHub Copilot",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.Home, ".copilot", "skills") },
		markers:       homePaths(".copilot"),
		bins:          []string{"copilot"},
		mcp:           copilotMCP,
	},
	{
		ID: "gemini-cli", Name: "Gemini CLI",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.Home, ".gemini", "skills") },
		markers:       homePaths(".gemini"),
		bins:          []string{"gemini"},
		mcp:           geminiMCP,
	},
	{
		ID: "opencode", Name: "OpenCode",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.configHome(), "opencode", "skills") },
		markers:       func(e *Env) []string { return []string{filepath.Join(e.configHome(), "opencode")} },
		bins:          []string{"opencode"},
		mcp:           opencodeMCP,
	},
	{
		ID: "windsurf", Name: "Windsurf",
		projectSkills: ".windsurf/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.Home, ".codeium", "windsurf", "skills") },
		markers:       homePaths(filepath.Join(".codeium", "windsurf")),
		mcp:           windsurfMCP,
	},
	{
		ID: "amp", Name: "Amp",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.configHome(), "agents", "skills") },
		markers:       func(e *Env) []string { return []string{filepath.Join(e.configHome(), "amp")} },
		bins:          []string{"amp"},
	},
	{
		ID: "cline", Name: "Cline",
		projectSkills: ".agents/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.Home, ".agents", "skills") },
		markers:       homePaths(".cline"),
	},
	{
		ID: "goose", Name: "Goose",
		projectSkills: ".goose/skills",
		globalSkills:  func(e *Env) string { return filepath.Join(e.configHome(), "goose", "skills") },
		markers:       func(e *Env) []string { return []string{filepath.Join(e.configHome(), "goose")} },
		bins:          []string{"goose"},
	},
}

// AgentIDs lists every known agent ID.
func AgentIDs() []string {
	ids := make([]string, len(Agents))
	for i, a := range Agents {
		ids[i] = a.ID
	}
	return ids
}

// FindAgent looks an agent up by ID.
func FindAgent(id string) *Agent {
	for _, a := range Agents {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// DetectAgents returns the agents that appear to be installed.
func DetectAgents(env *Env) []*Agent {
	var out []*Agent
	for _, a := range Agents {
		if a.Detected(env) {
			out = append(out, a)
		}
	}
	return out
}

// SkillsDirs groups agents by the directory they read skills from, so a shared
// directory like .agents/skills gets a single copy. Order follows agents.
type SkillsDir struct {
	Path   string
	Agents []*Agent
}

func GroupSkillsDirs(env *Env, scope Scope, agents []*Agent) []SkillsDir {
	var dirs []SkillsDir
	for _, a := range agents {
		p := a.SkillsDir(env, scope)
		i := slices.IndexFunc(dirs, func(d SkillsDir) bool { return d.Path == p })
		if i < 0 {
			dirs = append(dirs, SkillsDir{Path: p})
			i = len(dirs) - 1
		}
		dirs[i].Agents = append(dirs[i].Agents, a)
	}
	return dirs
}

// AgentNames joins agents' display names for messages.
func AgentNames(agents []*Agent) string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}
