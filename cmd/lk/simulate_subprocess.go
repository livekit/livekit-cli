// Copyright 2025 LiveKit, Inc.
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
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// AgentProcess manages an agent subprocess.
type AgentProcess struct {
	cmd            *exec.Cmd
	readyCh        chan struct{}
	failCh         chan struct{} // closed when output matches a FailSignal
	doneCh         chan error
	exitCh         chan struct{} // closed when process exits, safe to read multiple times
	shutdownCalled bool          // true after Shutdown() sends SIGINT

	// LogStream receives log lines in real-time. Nil if not needed.
	LogStream chan string

	mu             sync.Mutex
	logLines       []string
	roomLogs       map[string][]string
	latestRoomByPx map[string]string // prefix → latest room name seen
	logFile        *os.File
	LogPath        string
}

// findPythonBinary locates a Python binary for the given project type.
func findPythonBinary(dir string, projectType agentfs.ProjectType) (string, []string, error) {
	if projectType == agentfs.ProjectTypePythonUV {
		uvPath, err := exec.LookPath("uv")
		if err == nil {
			return uvPath, []string{"run", "python"}, nil
		}
	}

	// Check common venv locations
	for _, venvDir := range []string{".venv", "venv"} {
		candidate := filepath.Join(dir, venvDir, "bin", "python")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil, nil
		}
	}

	// Fall back to system python
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			return "", nil, fmt.Errorf("could not find Python binary; ensure a virtual environment exists or Python is on PATH")
		}
	}
	return pythonPath, nil, nil
}

// findNodeBinary locates the Node binary used to run a JS/TS agent.
func findNodeBinary() (string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("could not find Node binary; ensure node is on PATH")
	}
	return nodePath, nil
}

// isTypeScriptEntry reports whether the entrypoint is TypeScript source that
// needs Node's type-stripping loader to run directly (no build step).
func isTypeScriptEntry(entry string) bool {
	switch strings.ToLower(filepath.Ext(entry)) {
	case ".ts", ".mts", ".cts":
		return true
	default:
		return false
	}
}

var nodeVersionRe = regexp.MustCompile(`v(\d+)\.(\d+)`)

// checkTypeStrippingSupport verifies the Node binary can run TypeScript
// directly (--experimental-strip-types requires Node >= 22.6). The probe
// runs in the project dir so version-manager shims resolve the same Node
// the spawn will use. Probing failures are ignored — the spawn itself will
// surface any real error.
func checkTypeStrippingSupport(dir, nodeBin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "--version")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	version := strings.TrimSpace(string(out))
	m := nodeVersionRe.FindStringSubmatch(version)
	if m == nil {
		return nil
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 22 || (major == 22 && minor < 6) {
		return fmt.Errorf("running a TypeScript entrypoint directly requires Node >= 22.6 (found %s); upgrade Node or point at built JS output", version)
	}
	return nil
}

// nodeAgentMinVersion is the minimum @livekit/agents (agents-js) release the
// CLI supports. Unlike the Python thin CLI (gated on thinCLIMinVersion), the
// Node entrypoint exposes the start/console/simulate subcommands directly, so
// the baseline differs. Local placeholder — the deploy path sources the
// equivalent floor from server client settings.
const nodeAgentMinVersion = "1.0.0"

// nodeResolveVersionScript asks Node to report the installed @livekit/agents
// version using its own module resolution paths (so pnpm/workspace symlinks
// and hoisting resolve exactly as they will at runtime). See the source file
// for details.
//
//go:embed node_resolve_version.js
var nodeResolveVersionScript string

// resolveNodeAgentVersion returns the installed @livekit/agents version as Node
// resolves it from fromDir, or "" if it can't be determined.
func resolveNodeAgentVersion(nodeBin, fromDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "-e", nodeResolveVersionScript)
	cmd.Dir = fromDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkNodeSDKVersion gates a Node agent on nodeAgentMinVersion, resolving the
// installed @livekit/agents from the entrypoint's directory so monorepo and
// workspace layouts (where the dep is a workspace:* symlink, not a versioned
// entry in the root package.json) report the version that will actually run.
func checkNodeSDKVersion(cfg AgentStartConfig) error {
	nodeBin, err := findNodeBinary()
	if err != nil {
		return err
	}
	fromDir := filepath.Dir(filepath.Join(cfg.Dir, cfg.Entrypoint))
	version := resolveNodeAgentVersion(nodeBin, fromDir)
	if version == "" {
		return fmt.Errorf("@livekit/agents not found; install dependencies and make sure this is a LiveKit agent project")
	}
	// An unparseable version (e.g. a local "0.0.0-dev" tag) shouldn't block a run.
	if ok, err := agentfs.IsVersionSatisfied(version, nodeAgentMinVersion); err == nil && !ok {
		return fmt.Errorf("@livekit/agents version %s is too old, please upgrade to %s or newer", version, nodeAgentMinVersion)
	}
	return nil
}

// pythonResolveVersionScript prints the installed livekit-agents version, or
// exits non-zero if it isn't importable.
const pythonResolveVersionScript = `import importlib.metadata as m; print(m.version("livekit-agents"))`

// resolvePythonAgentVersion returns the installed livekit-agents version read
// via the project's interpreter, so any installer (uv, pip, poetry) reports the
// version that will actually run. Returns "" when it can't be determined (no
// interpreter, dependencies not installed, etc.). For uv it reads the existing
// environment without syncing, so a pre-flight check never mutates or downloads.
func resolvePythonAgentVersion(dir string, projectType agentfs.ProjectType) string {
	pythonBin, prefixArgs, err := findPythonBinary(dir, projectType)
	if err != nil {
		return ""
	}
	if projectType == agentfs.ProjectTypePythonUV && len(prefixArgs) > 0 && prefixArgs[0] == "run" {
		prefixArgs = append([]string{"run", "--no-sync"}, prefixArgs[1:]...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(append([]string{}, prefixArgs...), "-c", pythonResolveVersionScript)
	cmd := exec.CommandContext(ctx, pythonBin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkPythonSDKVersion gates a Python agent on thinCLIMinVersion. It prefers
// the installed version (resolved via the interpreter, accurate regardless of
// the package manager and not fooled by a loose version constraint); when
// dependencies aren't installed it falls back to static project-file parsing.
func checkPythonSDKVersion(cfg AgentStartConfig) error {
	if version := resolvePythonAgentVersion(cfg.Dir, cfg.ProjectType); version != "" {
		// An unparseable version (e.g. a local "0.0.0.dev" tag) shouldn't block a run.
		if ok, err := agentfs.IsVersionSatisfied(version, thinCLIMinVersion); err == nil && !ok {
			return fmt.Errorf("livekit-agents version %s is too old, please upgrade to %s or newer", version, thinCLIMinVersion)
		}
		return nil
	}
	return agentfs.CheckSDKVersion(cfg.Dir, cfg.ProjectType, map[string]string{
		"python-min-sdk-version": thinCLIMinVersion,
		"node-min-sdk-version":   thinCLIMinVersion,
	})
}

// defaultEntrypoints returns candidate entrypoint paths (relative to the
// project root or working directory) probed for a project type, in priority
// order. Forward slashes are valid on all platforms.
func defaultEntrypoints(projectType agentfs.ProjectType) []string {
	if projectType.IsNode() {
		return []string{"main.ts", "src/main.js"}
	}
	return []string{"agent.py"}
}

// fallbackEntrypoints are probed at the project root only after cwd-relative
// candidates, so a root src/ layout doesn't shadow an agent next to the
// user's working directory.
func fallbackEntrypoints(projectType agentfs.ProjectType) []string {
	if projectType.IsNode() {
		return []string{"src/main.ts", "src/main.js"}
	}
	return []string{"src/agent.py"}
}

// findEntrypoint resolves the agent entrypoint file.
func findEntrypoint(dir, explicit string, projectType agentfs.ProjectType) (string, error) {
	if explicit != "" {
		path := explicit
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("entrypoint file not found: %s", explicit)
		}
		return explicit, nil
	}
	rootCandidates := defaultEntrypoints(projectType)
	srcCandidates := fallbackEntrypoints(projectType)

	// Check project root first
	var checked []string
	probe := func(rel string) bool {
		abs := filepath.Join(dir, rel)
		checked = append(checked, abs)
		_, err := os.Stat(abs)
		return err == nil
	}
	for _, def := range rootCandidates {
		if probe(def) {
			return def, nil
		}
	}

	// Then cwd-relative paths (e.g. running from examples/drive-thru/)
	cwd, _ := os.Getwd()
	if rel, err := filepath.Rel(dir, cwd); err == nil && rel != "." {
		for _, def := range append(append([]string{}, rootCandidates...), srcCandidates...) {
			candidate := filepath.Join(rel, def)
			if probe(candidate) {
				return candidate, nil
			}
		}
	}

	// Finally the project root's src/ layout
	for _, def := range srcCandidates {
		if probe(def) {
			return def, nil
		}
	}

	example := rootCandidates[0]
	msg := "no agent entrypoint found, checked:\n"
	for _, p := range checked {
		msg += fmt.Sprintf("  - %s\n", p)
	}
	msg += "\nMake sure you are running this command from a directory containing a LiveKit agent.\n"
	msg += fmt.Sprintf("Specify the entrypoint file as a positional argument, e.g.: lk agent dev %s", example)
	return "", fmt.Errorf("%s", msg)
}

// AgentStartConfig configures how to launch an agent subprocess.
type AgentStartConfig struct {
	Dir           string
	Entrypoint    string
	ProjectType   agentfs.ProjectType
	RuntimeArgs   []string  // interpreter (node/python) args placed before the entrypoint, e.g. ["--env-file=.env"]
	CLIArgs       []string  // subcommand first, then flags: ["start", "--url", "..."] or ["console", "--connect-addr", addr]
	Env           []string  // e.g. ["LIVEKIT_AGENT_NAME_OVERRIDE=x"] or nil
	ReadySignal   string    // substring to scan for in output (e.g. "registered worker"), empty to skip
	FailSignals   []string  // output substrings meaning the agent has fatally failed even if the process is still alive
	ForwardOutput io.Writer // if set, forward each output line to this writer
}

// thinCLIMinVersion is the first livekit-agents release that exposes the
// start/dev/console/simulate subcommands under `python -m livekit.agents`.
const thinCLIMinVersion = "1.6.0"

// buildAgentCommand resolves the interpreter and argv for an agent subprocess,
// branching on project type. Python uses the thin CLI:
// `<python> <runtime-args> -m livekit.agents SUBCOMMAND ENTRYPOINT FLAGS`
// (uv prefixes `run python`). Node runs the entrypoint directly:
// `node [--experimental-strip-types] <runtime-args> ENTRYPOINT SUBCOMMAND FLAGS`,
// where the type-stripping flag lets a `.ts` entrypoint run without a build.
func buildAgentCommand(cfg AgentStartConfig) (string, []string, error) {
	if cfg.ProjectType.IsNode() {
		nodeBin, err := findNodeBinary()
		if err != nil {
			return "", nil, err
		}
		args := make([]string, 0, len(cfg.RuntimeArgs)+len(cfg.CLIArgs)+2)
		if isTypeScriptEntry(cfg.Entrypoint) {
			if err := checkTypeStrippingSupport(cfg.Dir, nodeBin); err != nil {
				return "", nil, err
			}
			args = append(args, "--experimental-strip-types")
		}
		args = append(args, cfg.RuntimeArgs...)
		args = append(args, cfg.Entrypoint)
		args = append(args, cfg.CLIArgs...)
		return nodeBin, args, nil
	}

	pythonBin, prefixArgs, err := findPythonBinary(cfg.Dir, cfg.ProjectType)
	if err != nil {
		return "", nil, err
	}
	// python -m livekit.agents SUBCOMMAND ENTRYPOINT FLAGS: the framework
	// discovers the AgentServer from the entrypoint and drives the thin CLI.
	args := make([]string, 0, len(prefixArgs)+len(cfg.RuntimeArgs)+len(cfg.CLIArgs)+4)
	args = append(args, prefixArgs...)
	args = append(args, cfg.RuntimeArgs...)
	args = append(args, "-m", "livekit.agents")
	if len(cfg.CLIArgs) > 0 {
		args = append(args, cfg.CLIArgs[0]) // subcommand: start | console
		args = append(args, cfg.Entrypoint) // entrypoint positional (server discovery)
		args = append(args, cfg.CLIArgs[1:]...)
	} else {
		args = append(args, cfg.Entrypoint)
	}
	return pythonBin, args, nil
}

// startAgent launches a Python or Node agent subprocess and monitors its output.
func startAgent(cfg AgentStartConfig) (*AgentProcess, error) {
	// fail fast when the agent SDK is older than the baseline the CLI supports.
	// Node resolves the installed package via the runtime so workspace/monorepo
	// layouts work; Python parses project files against the thin-CLI baseline.
	if cfg.ProjectType.IsNode() {
		if err := checkNodeSDKVersion(cfg); err != nil {
			return nil, err
		}
	} else if err := checkPythonSDKVersion(cfg); err != nil {
		return nil, err
	}

	bin, args, err := buildAgentCommand(cfg)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, args...)
	setProcAttr(cmd)
	cmd.Dir = cfg.Dir
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	logFile, err := os.CreateTemp("", "lk-simulate-*.log")
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	ap := &AgentProcess{
		cmd:            cmd,
		readyCh:        make(chan struct{}),
		failCh:         make(chan struct{}),
		doneCh:         make(chan error, 1),
		exitCh:         make(chan struct{}),
		roomLogs:       make(map[string][]string),
		latestRoomByPx: make(map[string]string),
		logFile:        logFile,
		LogPath:        logFile.Name(),
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		os.Remove(logFile.Name())
		return nil, fmt.Errorf("failed to start agent: %w", err)
	}

	// Capture output from both stdout and stderr
	readyOnce := sync.Once{}
	failOnce := sync.Once{}
	scanOutput := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			// Keep ANSI colors: the TUI renders them. Plain-text consumers
			// (log file, surfaced errors, fatal-marker matching) strip their own copy.
			line := scanner.Text()
			ap.appendLog(line)
			if cfg.ForwardOutput != nil {
				fmt.Fprintln(cfg.ForwardOutput, line)
			}
			if cfg.ReadySignal != "" && strings.Contains(line, cfg.ReadySignal) {
				readyOnce.Do(func() { close(ap.readyCh) })
			}
			for _, sig := range cfg.FailSignals {
				if strings.Contains(line, sig) {
					failOnce.Do(func() { close(ap.failCh) })
					break
				}
			}
		}
	}

	// If no ready signal, mark ready immediately
	if cfg.ReadySignal == "" {
		close(ap.readyCh)
	}

	var scanWg sync.WaitGroup
	scanWg.Add(2)
	go func() { defer scanWg.Done(); scanOutput(stdout) }()
	go func() { defer scanWg.Done(); scanOutput(stderr) }()
	go func() {
		ap.doneCh <- cmd.Wait()
		close(ap.exitCh)
		scanWg.Wait()
		if ap.LogStream != nil {
			close(ap.LogStream)
		}
	}()

	return ap, nil
}

func (ap *AgentProcess) appendLog(line string) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.logLines = append(ap.logLines, line)
	if room := extractLogRoom(line); room != "" {
		ap.roomLogs[room] = append(ap.roomLogs[room], line)
		ap.latestRoomByPx[roomNamePrefix(room)] = room
	}
	if ap.logFile != nil {
		// the agent logs in colored format; keep the file free of ANSI escapes
		fmt.Fprintln(ap.logFile, ansiEscapeRe.ReplaceAllString(line, ""))
	}
	if ap.LogStream != nil {
		select {
		case ap.LogStream <- line:
		default:
		}
	}
}

// Ready returns a channel that is closed when the agent worker has registered.
func (ap *AgentProcess) Ready() <-chan struct{} {
	return ap.readyCh
}

// Done returns a channel that receives the process exit error.
func (ap *AgentProcess) Done() <-chan error {
	return ap.doneCh
}

// Failed returns a channel that is closed when the agent's output matched one
// of the configured FailSignals — a fatal failure even if the process is alive.
func (ap *AgentProcess) Failed() <-chan struct{} {
	return ap.failCh
}

// RecentLogs returns the last n log lines from the subprocess. If n <= 0, returns all lines.
func (ap *AgentProcess) RecentLogs(n int) []string {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if n <= 0 || n >= len(ap.logLines) {
		result := make([]string, len(ap.logLines))
		copy(result, ap.logLines)
		return result
	}
	result := make([]string, n)
	copy(result, ap.logLines[len(ap.logLines)-n:])
	return result
}

// LogCount returns the total number of log lines captured.
func (ap *AgentProcess) LogCount() int {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return len(ap.logLines)
}

// RecentRoomLogs returns the last n log lines for a specific room. If n <= 0, returns all lines.
func (ap *AgentProcess) RecentRoomLogs(n int, roomName string) []string {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	lines := ap.roomLogs[roomName]
	if n <= 0 || n >= len(lines) {
		result := make([]string, len(lines))
		copy(result, lines)
		return result
	}
	result := make([]string, n)
	copy(result, lines[len(lines)-n:])
	return result
}

// roomNamePrefix returns the stable part of a simulation room name (before the random suffix).
// e.g. "sim-SRJ_xxx-RANDOM" → "sim-SRJ_xxx-"
func roomNamePrefix(roomName string) string {
	idx := strings.LastIndex(roomName, "-")
	if idx < 0 {
		return roomName
	}
	return roomName[:idx+1]
}

// RecentRoomLogsByPrefix returns log lines for the most recent room matching
// the prefix of the given room name. When a job is retried, each attempt gets
// a new room with the same prefix; only the latest attempt's logs are shown.
func (ap *AgentProcess) RecentRoomLogsByPrefix(n int, roomName string) []string {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	latest := ap.latestRoomByPx[roomNamePrefix(roomName)]
	if latest == "" {
		return nil
	}
	lines := ap.roomLogs[latest]
	if n <= 0 || n >= len(lines) {
		result := make([]string, len(lines))
		copy(result, lines)
		return result
	}
	result := make([]string, n)
	copy(result, lines[len(lines)-n:])
	return result
}

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// agentExitDetail surfaces the agent's own output and the log path when the
// worker exits early or never registers.
func agentExitDetail(ap *AgentProcess) string {
	logs := ap.RecentLogs(0)

	var b strings.Builder

	if len(logs) == 0 {
		b.WriteString("Agent exited with no output.")
	} else if tail := lastNonEmptyLines(logs, 12); len(tail) > 0 {
		for i, l := range tail {
			tail[i] = ansiEscapeRe.ReplaceAllString(l, "")
		}
		b.WriteString("Agent output:\n  " + strings.Join(tail, "\n  "))
	}

	if ap.LogPath != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Full log: " + ap.LogPath)
	}
	return b.String()
}

func lastNonEmptyLines(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append([]string{lines[i]}, out...)
		}
	}
	return out
}

func extractLogRoom(line string) string {
	idx := strings.LastIndex(line, "{")
	if idx < 0 {
		return ""
	}
	end := strings.LastIndex(line, "}")
	if end <= idx {
		return ""
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(line[idx:end+1]), &extra); err != nil {
		return ""
	}
	if room, ok := extra["room"].(string); ok {
		return room
	}
	return ""
}

// Kill gives the worker a short SIGINT grace (an idle one exits cleanly; one
// draining jobs would take minutes), then SIGKILLs the whole process group and
// waits so the port is free on return.
func (ap *AgentProcess) Kill() {
	if ap.cmd.Process == nil {
		return
	}
	select {
	case <-ap.exitCh:
		ap.closeLogFile()
		return
	default:
	}
	if !ap.shutdownCalled {
		ap.sendInterrupt()
	}
	select {
	case <-ap.exitCh:
	case <-time.After(1 * time.Second):
	}
	ap.sendKill()
	select {
	case <-ap.exitCh:
	case <-time.After(2 * time.Second):
	}
	ap.closeLogFile()
}

func (ap *AgentProcess) closeLogFile() {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.logFile != nil {
		ap.logFile.Close()
		ap.logFile = nil
	}
}

// Shutdown initiates graceful shutdown of the agent process.
func (ap *AgentProcess) Shutdown() {
	if ap.cmd.Process == nil {
		return
	}
	ap.shutdownCalled = true
	ap.sendShutdown()
}

// ForceKill kills the process immediately.
func (ap *AgentProcess) ForceKill() {
	if ap.cmd.Process == nil {
		return
	}
	ap.sendKill()
}
