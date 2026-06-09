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

// defaultEntrypoints returns candidate entrypoint paths (relative to the
// project root or working directory) probed for a project type, in priority
// order. Forward slashes are valid on all platforms.
func defaultEntrypoints(projectType agentfs.ProjectType) []string {
	if projectType.IsNode() {
		return []string{"agent.ts", "agent.js"}
	}
	return []string{"agent.py"}
}

// fallbackEntrypoints are probed at the project root only after cwd-relative
// candidates, so a root src/ layout doesn't shadow an agent next to the
// user's working directory.
func fallbackEntrypoints(projectType agentfs.ProjectType) []string {
	if projectType.IsNode() {
		return []string{"src/agent.ts", "src/agent.js"}
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
	CLIArgs       []string  // e.g. ["start", "--url", "..."] or ["console", "--connect-addr", addr]
	Env           []string  // e.g. ["LIVEKIT_AGENT_NAME=x"] or nil
	ReadySignal   string    // substring to scan for in output (e.g. "registered worker"), empty to skip
	ForwardOutput io.Writer // if set, forward each output line to this writer
}

// buildAgentCommand resolves the interpreter and argv for an agent subprocess,
// branching on project type. Python: `<python> <entry> <args>` (uv prefixes
// `run python`). Node: `node [--experimental-strip-types] <entry> <args>`,
// where the type-stripping flag lets a `.ts` entrypoint run without a build.
func buildAgentCommand(cfg AgentStartConfig) (string, []string, error) {
	if cfg.ProjectType.IsNode() {
		nodeBin, err := findNodeBinary()
		if err != nil {
			return "", nil, err
		}
		args := make([]string, 0, len(cfg.CLIArgs)+2)
		if isTypeScriptEntry(cfg.Entrypoint) {
			if err := checkTypeStrippingSupport(cfg.Dir, nodeBin); err != nil {
				return "", nil, err
			}
			args = append(args, "--experimental-strip-types")
		}
		args = append(args, cfg.Entrypoint)
		args = append(args, cfg.CLIArgs...)
		return nodeBin, args, nil
	}

	pythonBin, prefixArgs, err := findPythonBinary(cfg.Dir, cfg.ProjectType)
	if err != nil {
		return "", nil, err
	}
	args := make([]string, 0, len(prefixArgs)+len(cfg.CLIArgs)+1)
	args = append(args, prefixArgs...)
	args = append(args, cfg.Entrypoint)
	args = append(args, cfg.CLIArgs...)
	return pythonBin, args, nil
}

// startAgent launches a Python or Node agent subprocess and monitors its output.
func startAgent(cfg AgentStartConfig) (*AgentProcess, error) {
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
	scanOutput := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			ap.appendLog(line)
			if cfg.ForwardOutput != nil {
				fmt.Fprintln(cfg.ForwardOutput, line)
			}
			if cfg.ReadySignal != "" && strings.Contains(line, cfg.ReadySignal) {
				readyOnce.Do(func() { close(ap.readyCh) })
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
		fmt.Fprintln(ap.logFile, line)
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
// a new room with the same prefix — we show only the latest attempt's logs.
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

func extractLogRoom(line string) string {
	clean := ansiEscapeRe.ReplaceAllString(line, "")
	idx := strings.LastIndex(clean, "{")
	if idx < 0 {
		return ""
	}
	end := strings.LastIndex(clean, "}")
	if end <= idx {
		return ""
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(clean[idx:end+1]), &extra); err != nil {
		return ""
	}
	if room, ok := extra["room"].(string); ok {
		return room
	}
	return ""
}

// Kill sends interrupt to the process and force-kills after a timeout.
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
	case <-time.After(5 * time.Second):
		ap.sendKill()
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
