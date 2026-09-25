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

package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

const (
	// MCPServerName is the name the Docs MCP server is registered under,
	// matching the setup instructions on docs.livekit.io.
	MCPServerName = "livekit-docs"
	MCPServerURL  = "https://docs.livekit.io/mcp"
	// MCPDocsURL covers manual setup for agents lk can't configure.
	MCPDocsURL = "https://docs.livekit.io/intro/mcp-server/"
)

// MCPState is whether an agent has the Docs MCP server configured.
type MCPState string

const (
	MCPConfigured  MCPState = "configured"
	MCPMissing     MCPState = "missing"
	MCPCustom      MCPState = "custom"      // an entry with our name points somewhere else; left alone
	MCPUnsupported MCPState = "unsupported" // lk can't configure this agent in this scope
)

type configFormat int

const (
	formatJSON configFormat = iota
	formatTOML
)

// mcpConfig is one agent's MCP config file.
type mcpConfig struct {
	path   string
	format configFormat
	key    string // the servers table: mcpServers, servers, mcp, mcp_servers
	entry  map[string]any
	// cli, when set and on PATH, is used to add the server instead of editing
	// path directly: for files the agent itself rewrites while running.
	cli []string
}

type mcpTarget struct {
	project func(*Env) *mcpConfig
	global  func(*Env) *mcpConfig
}

func (a *Agent) mcpConfig(env *Env, scope Scope) *mcpConfig {
	f := a.mcp.project
	if scope == ScopeGlobal {
		f = a.mcp.global
	}
	if f == nil {
		return nil
	}
	return f(env)
}

func jsonConfig(path, key string, entry map[string]any) *mcpConfig {
	return &mcpConfig{path: path, format: formatJSON, key: key, entry: entry}
}

var (
	httpEntry = map[string]any{"type": "http", "url": MCPServerURL}

	claudeMCP = mcpTarget{
		project: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Root, ".mcp.json"), "mcpServers", httpEntry)
		},
		global: func(e *Env) *mcpConfig {
			c := jsonConfig(filepath.Join(e.Home, ".claude.json"), "mcpServers", httpEntry)
			c.cli = []string{"claude", "mcp", "add", "--scope", "user", "--transport", "http", MCPServerName, MCPServerURL}
			return c
		},
	}
	codexMCP = mcpTarget{
		project: func(e *Env) *mcpConfig {
			return &mcpConfig{path: filepath.Join(e.Root, ".codex", "config.toml"), format: formatTOML, key: "mcp_servers"}
		},
		global: func(e *Env) *mcpConfig {
			return &mcpConfig{path: filepath.Join(e.codexHome(), "config.toml"), format: formatTOML, key: "mcp_servers"}
		},
	}
	cursorMCP = mcpTarget{
		project: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Root, ".cursor", "mcp.json"), "mcpServers", map[string]any{"url": MCPServerURL})
		},
		global: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Home, ".cursor", "mcp.json"), "mcpServers", map[string]any{"url": MCPServerURL})
		},
	}
	// Copilot: VS Code's workspace file for projects, the Copilot CLI's
	// config for the user.
	copilotMCP = mcpTarget{
		project: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Root, ".vscode", "mcp.json"), "servers", httpEntry)
		},
		global: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Home, ".copilot", "mcp-config.json"), "mcpServers",
				map[string]any{"type": "http", "url": MCPServerURL, "tools": []string{"*"}})
		},
	}
	geminiMCP = mcpTarget{
		project: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Root, ".gemini", "settings.json"), "mcpServers", map[string]any{"httpUrl": MCPServerURL})
		},
		global: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Home, ".gemini", "settings.json"), "mcpServers", map[string]any{"httpUrl": MCPServerURL})
		},
	}
	opencodeMCP = mcpTarget{
		project: func(e *Env) *mcpConfig { return opencodeConfig(e.Root) },
		global:  func(e *Env) *mcpConfig { return opencodeConfig(filepath.Join(e.configHome(), "opencode")) },
	}
	// Windsurf has no project-level MCP config.
	windsurfMCP = mcpTarget{
		global: func(e *Env) *mcpConfig {
			return jsonConfig(filepath.Join(e.Home, ".codeium", "windsurf", "mcp_config.json"), "mcpServers", map[string]any{"serverUrl": MCPServerURL})
		},
	}
)

// opencodeConfig uses whichever of opencode.jsonc / opencode.json exists.
func opencodeConfig(dir string) *mcpConfig {
	path := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(filepath.Join(dir, "opencode.jsonc")); err == nil {
		path = filepath.Join(dir, "opencode.jsonc")
	}
	return jsonConfig(path, "mcp", map[string]any{"type": "remote", "url": MCPServerURL, "enabled": true})
}

// MCPStatus reports an agent's Docs MCP setup and the file it lives in.
type MCPStatus struct {
	Agent *Agent
	State MCPState
	Path  string
	Err   error // the config exists but couldn't be read
	// Added is set by ConfigureMCP when it added the server (rather than
	// finding it already there).
	Added bool
}

// CheckMCP inspects an agent's MCP config without changing it.
func CheckMCP(env *Env, scope Scope, a *Agent) MCPStatus {
	c := a.mcpConfig(env, scope)
	if c == nil {
		return MCPStatus{Agent: a, State: MCPUnsupported}
	}
	st := MCPStatus{Agent: a, Path: c.path}
	entry, err := c.read()
	switch {
	case err != nil:
		st.State, st.Err = MCPMissing, err
	case entry == nil:
		st.State = MCPMissing
	case pointsAtDocs(entry):
		st.State = MCPConfigured
	default:
		st.State = MCPCustom
	}
	return st
}

// ConfigureMCP adds the Docs MCP server to an agent's config. It never
// replaces an existing entry of the same name.
func ConfigureMCP(ctx context.Context, env *Env, scope Scope, a *Agent) (MCPStatus, error) {
	st := CheckMCP(env, scope, a)
	if st.State != MCPMissing {
		return st, nil
	}
	if st.Err != nil {
		return st, st.Err
	}
	c := a.mcpConfig(env, scope)
	if len(c.cli) > 0 && env.lookPath(c.cli[0]) {
		cmd := exec.CommandContext(ctx, c.cli[0], c.cli[1:]...)
		cmd.Dir = env.Root
		if out, err := cmd.CombinedOutput(); err != nil {
			return st, fmt.Errorf("%s: %w: %s", strings.Join(c.cli, " "), err, strings.TrimSpace(string(out)))
		}
	} else if err := c.add(); err != nil {
		return st, err
	}
	st.State, st.Added = MCPConfigured, true
	return st, nil
}

// read returns our server's entry from the config file, or nil if absent.
func (c *mcpConfig) read() (map[string]any, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc map[string]any
	if c.format == formatTOML {
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", c.path, err)
		}
	} else {
		std, err := hujson.Standardize(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.path, err)
		}
		if err := json.Unmarshal(std, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", c.path, err)
		}
	}
	servers, _ := doc[c.key].(map[string]any)
	entry, ok := servers[MCPServerName]
	if !ok {
		return nil, nil
	}
	m, _ := entry.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func pointsAtDocs(entry map[string]any) bool {
	for _, k := range []string{"url", "httpUrl", "serverUrl"} {
		if u, ok := entry[k].(string); ok && strings.TrimRight(u, "/") == MCPServerURL {
			return true
		}
	}
	return false
}

func (c *mcpConfig) add() error {
	data, err := os.ReadFile(c.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var out []byte
	if c.format == formatTOML {
		out = addTOML(data)
		// Appending a table is only wrong if the file already defines the
		// servers table inline; re-parse (strictly, as Codex does) rather
		// than guess.
		var doc map[string]any
		if err := toml.Unmarshal(out, &doc); err != nil {
			return fmt.Errorf("%s: can't add %s automatically: %w", c.path, MCPServerName, err)
		}
	} else if out, err = addJSON(data, c.key, c.entry); err != nil {
		return fmt.Errorf("%s: %w", c.path, err)
	}
	return writeFileAtomic(c.path, out, 0o644)
}

func addTOML(data []byte) []byte {
	var b bytes.Buffer
	b.Write(data)
	if len(bytes.TrimSpace(data)) > 0 {
		if !bytes.HasSuffix(data, []byte("\n")) {
			b.WriteString("\n")
		}
		if !bytes.HasSuffix(data, []byte("\n\n")) {
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "[mcp_servers.%s]\nurl = %q\n", MCPServerName, MCPServerURL)
	return b.Bytes()
}

// addJSON inserts servers[MCPServerName] = entry into a JSON (or JSONC) file,
// leaving the rest of the file, comments and formatting included, untouched.
func addJSON(data []byte, key string, entry map[string]any) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		out, _ := json.MarshalIndent(map[string]any{key: map[string]any{MCPServerName: entry}}, "", "  ")
		return append(out, '\n'), nil
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("top level is not an object")
	}
	unit := indentUnit(root)
	if servers := findMember(root, key); servers != nil {
		obj, ok := servers.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", key)
		}
		if err := appendMember(obj, MCPServerName, entry, 2, unit); err != nil {
			return nil, err
		}
	} else if err := appendMember(root, key, map[string]any{MCPServerName: entry}, 1, unit); err != nil {
		return nil, err
	}
	return v.Pack(), nil
}

func findMember(obj *hujson.Object, name string) *hujson.Value {
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && lit.String() == name {
			return &obj.Members[i].Value
		}
	}
	return nil
}

// indentUnit guesses the file's indent from its first member, defaulting to
// two spaces.
func indentUnit(root *hujson.Object) string {
	if len(root.Members) > 0 {
		before := string(root.Members[0].Name.BeforeExtra)
		if i := strings.LastIndex(before, "\n"); i >= 0 {
			if ws := before[i+1:]; ws != "" && strings.Trim(ws, " \t") == "" {
				return ws
			}
		}
	}
	return "  "
}

// appendMember adds name: value as the object's last member, indented to
// depth, matching whether the object is laid out on one line or many.
func appendMember(obj *hujson.Object, name string, value any, depth int, unit string) error {
	multiline := len(obj.Members) == 0 ||
		strings.Contains(string(obj.Members[len(obj.Members)-1].Name.BeforeExtra), "\n")
	var text []byte
	var before hujson.Extra
	if multiline {
		indent := strings.Repeat(unit, depth)
		text, _ = json.MarshalIndent(value, indent, unit)
		before = hujson.Extra("\n" + indent)
		if len(obj.Members) == 0 {
			obj.AfterExtra = hujson.Extra("\n" + strings.Repeat(unit, depth-1))
		}
	} else {
		text, _ = json.Marshal(value)
		before = hujson.Extra(" ")
	}
	val, err := hujson.Parse(text)
	if err != nil {
		return err
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: before, Value: hujson.String(name)},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: val.Value},
	})
	return nil
}
