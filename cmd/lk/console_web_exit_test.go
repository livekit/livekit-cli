package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitBrowserAgentExit(t *testing.T) {
	t.Run("exit failure survives log drain", func(t *testing.T) {
		done := make(chan error, 1)
		failed := make(chan struct{})
		exitErr := errors.New("exit status 1")
		timer := time.AfterFunc(10*time.Millisecond, func() { done <- exitErr })
		defer timer.Stop()
		require.ErrorIs(t, waitBrowserAgentExit(done, failed, time.Second), exitErr)
	})
	t.Run("clean exit", func(t *testing.T) {
		done := make(chan error, 1)
		done <- nil
		require.NoError(t, waitBrowserAgentExit(done, nil, time.Second))
	})
	t.Run("job crashed but worker still running", func(t *testing.T) {
		failed := make(chan struct{})
		close(failed)
		require.EqualError(t, waitBrowserAgentExit(nil, failed, time.Second), "agent job crashed")
	})
	t.Run("worker stays running", func(t *testing.T) {
		require.ErrorIs(t, waitBrowserAgentExit(nil, nil, time.Millisecond), errBrowserAgentExitTimeout)
	})
}

func TestFinishDisconnectedBrowserAgent(t *testing.T) {
	t.Run("original exit failure does not cause shutdown", func(t *testing.T) {
		done := make(chan error, 1)
		exitErr := errors.New("exit status 1")
		done <- exitErr
		err := finishDisconnectedBrowserAgent(done, nil, func() {
			t.Fatal("must not signal an agent that already exited")
		}, time.Second, time.Second)
		require.ErrorIs(t, err, exitErr)
	})
	t.Run("clean exit after shutdown", func(t *testing.T) {
		done := make(chan error, 1)
		err := finishDisconnectedBrowserAgent(done, nil, func() { done <- nil }, 0, time.Second)
		require.NoError(t, err)
	})
	t.Run("timeout after shutdown", func(t *testing.T) {
		shutdownCalled := false
		err := finishDisconnectedBrowserAgent(nil, nil, func() { shutdownCalled = true }, 0, 0)
		require.True(t, shutdownCalled)
		require.ErrorIs(t, err, errBrowserAgentExitTimeout)
	})
}
