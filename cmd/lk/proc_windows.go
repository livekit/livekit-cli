//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func setProcAttr(_ *exec.Cmd) {}

// setDetachedProcAttr starts the daemon in a new process group so it is not
// killed when the parent CLI invocation exits.
func setDetachedProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func (ap *AgentProcess) sendInterrupt() {
	if ap.cmd.Process != nil {
		ap.cmd.Process.Signal(os.Interrupt)
	}
}

// sendKill kills the process and all its children using taskkill /T.
func (ap *AgentProcess) sendKill() {
	if ap.cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(ap.cmd.Process.Pid))
	kill.Run()
}

func (ap *AgentProcess) sendShutdown() {
	ap.sendInterrupt()
}
