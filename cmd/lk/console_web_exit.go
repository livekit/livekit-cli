package main

import (
	"errors"
	"time"
)

var errBrowserAgentExitTimeout = errors.New("timed out waiting for the agent process to exit")

func waitBrowserAgentExit(done <-chan error, failed <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-failed:
		return errors.New("agent job crashed")
	case <-timer.C:
		return errBrowserAgentExitTimeout
	}
}
