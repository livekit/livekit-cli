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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// AgentProcess manages a Python agent subprocess.
type AgentProcess struct {
	cmd            *exec.Cmd
	readyCh        chan struct{}
	doneCh         chan error
	exitCh         chan struct{} // closed when process exits, safe to read multiple times
	shutdownCalled bool         // true after Shutdown() sends SIGINT

	// LogStream receives log lines in real-time. Nil if not needed.
	LogStream chan string

	mu       sync.Mutex
	logLines []string
	maxLogs  int
	logFile  *os.File
	LogPath  string
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

	msg := "no agent entrypoint found, checked:\n"
	for _, p := range checked {
		msg += fmt.Sprintf("  - %s\n", p)
	}
	msg += "\nMake sure you are running this command from a directory containing a LiveKit agent.\n"
	msg += "Use --entrypoint to specify the agent entrypoint file."
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

// startAgent launches a Python agent subprocess and monitors its output.
func startAgent(cfg AgentStartConfig) (*AgentProcess, error) {
	pythonBin, prefixArgs, err := findPythonBinary(cfg.Dir, cfg.ProjectType)
	if err != nil {
		return nil, err
	}

	args := append(prefixArgs, cfg.Entrypoint)
	args = append(args, cfg.CLIArgs...)
	cmd := exec.Command(pythonBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		cmd:     cmd,
		readyCh: make(chan struct{}),
		doneCh:  make(chan error, 1),
		exitCh:  make(chan struct{}),
		maxLogs: 200,
		logFile: logFile,
		LogPath: logFile.Name(),
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
	if len(ap.logLines) > ap.maxLogs {
		ap.logLines = ap.logLines[len(ap.logLines)-ap.maxLogs:]
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

// RecentLogs returns the last n log lines from the subprocess.
func (ap *AgentProcess) RecentLogs(n int) []string {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if n >= len(ap.logLines) {
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

// Kill sends SIGINT to the process group and SIGKILL after a timeout.
// If Shutdown() was already called, it just waits for exit (no duplicate SIGINT).
func (ap *AgentProcess) Kill() {
	if ap.cmd.Process == nil {
		return
	}
	// Already exited — nothing to do.
	select {
	case <-ap.exitCh:
		ap.closeLogFile()
		return
	default:
	}
	if !ap.shutdownCalled {
		ap.signalGroup(syscall.SIGINT)
	}
	select {
	case <-ap.exitCh:
	case <-time.After(5 * time.Second):
		ap.signalGroup(syscall.SIGKILL)
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

// Shutdown sends SIGINT to the main process to initiate graceful shutdown.
// Only signals the main process (not the group) so that Python manages
// its own child process cleanup without stray signal bouncing.
func (ap *AgentProcess) Shutdown() {
	if ap.cmd.Process == nil {
		return
	}
	ap.shutdownCalled = true
	ap.cmd.Process.Signal(syscall.SIGINT)
}

// ForceKill sends SIGKILL to the process group immediately.
func (ap *AgentProcess) ForceKill() {
	if ap.cmd.Process == nil {
		return
	}
	ap.signalGroup(syscall.SIGKILL)
}

// signalGroup sends a signal to the entire process group (Setpgid must be true).
func (ap *AgentProcess) signalGroup(sig syscall.Signal) {
	if ap.cmd.Process == nil {
		return
	}
	// Negative PID signals the entire process group.
	_ = syscall.Kill(-ap.cmd.Process.Pid, sig)
}
