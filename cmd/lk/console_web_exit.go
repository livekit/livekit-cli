package main

import (
	"errors"
	"time"
)

var errBrowserAgentExitTimeout = errors.New("timed out waiting for the agent process to exit")

func finishDisconnectedBrowserAgent(done <-chan error, failed <-chan struct{}, shutdown func(), exitWait, shutdownWait time.Duration) error {
	// TCP can close before the CLI finishes draining subprocess logs. Let the
	// original process result arrive before signaling a still-running worker.
	err := waitBrowserAgentExit(done, failed, exitWait)
	if !errors.Is(err, errBrowserAgentExitTimeout) {
		return err
	}
	shutdown()
	return waitBrowserAgentExit(done, failed, shutdownWait)
}

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
