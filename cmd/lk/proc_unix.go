//go:build !windows

package main

//lint:file-ignore U1000 consumed by console-tagged commands (hidden from the default tag-free lint build) and the lk session daemon (follow-up PR); remove once the daemon merges

import (
	"os/exec"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// setDetachedProcAttr puts the child in its own session so it survives the
// parent CLI invocation exiting (used for the detached session daemon).
func setDetachedProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
