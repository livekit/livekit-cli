//go:build console && !windows

package main

import (
	"os/exec"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// sendInterrupt sends SIGINT to the entire process group.
func (ap *AgentProcess) sendInterrupt() {
	if ap.cmd.Process != nil {
		_ = syscall.Kill(-ap.cmd.Process.Pid, syscall.SIGINT)
	}
}

// sendKill sends SIGKILL to the entire process group.
func (ap *AgentProcess) sendKill() {
	if ap.cmd.Process != nil {
		_ = syscall.Kill(-ap.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// sendShutdown sends SIGINT to the main process only (not the group),
// letting Python manage its own child cleanup.
func (ap *AgentProcess) sendShutdown() {
	if ap.cmd.Process != nil {
		ap.cmd.Process.Signal(syscall.SIGINT)
	}
}
