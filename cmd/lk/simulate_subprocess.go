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

//lint:file-ignore U1000 consumed by console-tagged commands (hidden from the default tag-free lint build) and the lk session daemon (follow-up PR); remove once the daemon merges

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// AgentProcess manages a Python agent subprocess.
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
	def := projectType.DefaultEntrypoint()
	if def == "" {
		def = "agent.py"
	}

	// Check project root first
	checked := []string{filepath.Join(dir, def)}
	if _, err := os.Stat(checked[0]); err == nil {
		return def, nil
	}

	// Fall back to cwd-relative path (e.g. running from examples/drive-thru/)
	cwd, _ := os.Getwd()
	if rel, err := filepath.Rel(dir, cwd); err == nil && rel != "." {
		candidate := filepath.Join(rel, def)
		absCandidate := filepath.Join(dir, candidate)
		checked = append(checked, absCandidate)
		if _, err := os.Stat(absCandidate); err == nil {
			return candidate, nil
		}
	}

	var msg strings.Builder
	msg.WriteString("no agent entrypoint found, checked:\n")
	for _, p := range checked {
		fmt.Fprintf(&msg, "  - %s\n", p)
	}
	msg.WriteString("\nMake sure you are running this command from a directory containing a LiveKit agent.\n")
	msg.WriteString("Specify the entrypoint file as a positional argument, e.g.: lk agent simulate agent.py")
	return "", fmt.Errorf("%s", msg.String())
}

// AgentStartConfig configures how to launch an agent subprocess.
type AgentStartConfig struct {
	Dir           string
	Entrypoint    string
	ProjectType   agentfs.ProjectType
	CLIArgs       []string  // subcommand first, then flags: ["start", "--url", "..."] or ["console", "--connect-addr", addr]
	Env           []string  // e.g. ["LIVEKIT_AGENT_NAME_OVERRIDE=x"] or nil
	ReadySignal   string    // substring to scan for in output (e.g. "registered worker"), empty to skip
	ForwardOutput io.Writer // if set, forward each output line to this writer
}

// startAgent launches a Python agent subprocess and monitors its output.
func startAgent(cfg AgentStartConfig) (*AgentProcess, error) {
	pythonBin, prefixArgs, err := findPythonBinary(cfg.Dir, cfg.ProjectType)
	if err != nil {
		return nil, err
	}

	// Launch via the framework CLI module rather than running the user's file
	// directly: python -m livekit.agents SUBCOMMAND ENTRYPOINT FLAGS. The framework
	// discovers the AgentServer from the entrypoint and drives the thin CLI. Requires a
	// livekit-agents that supports start/console under -m livekit.agents; older versions
	// only expose download-files there.
	args := append(prefixArgs, "-m", "livekit.agents")
	if len(cfg.CLIArgs) > 0 {
		args = append(args, cfg.CLIArgs[0]) // subcommand: start | console
		args = append(args, cfg.Entrypoint) // entrypoint positional (server discovery)
		args = append(args, cfg.CLIArgs[1:]...)
	} else {
		args = append(args, cfg.Entrypoint)
	}
	cmd := exec.Command(pythonBin, args...)
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
