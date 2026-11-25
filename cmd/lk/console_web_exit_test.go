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
