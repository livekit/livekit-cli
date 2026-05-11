//go:build console && windows

package main

import (
	"os"
	"os/exec"
	"strconv"
)

func setProcAttr(_ *exec.Cmd) {}

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
