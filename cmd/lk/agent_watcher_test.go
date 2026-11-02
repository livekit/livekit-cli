package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentFileWatcherSkipsBuildAndDependencyTrees(t *testing.T) {
	root := t.TempDir()
	// Even Python files under these trees must never be visited or watched.
	ignored := []string{
		"rust/target/debug/deps", "target/release", ".venv/lib/python",
		"node_modules/dependency", "build/generated", ".git/objects",
	}
	for _, dir := range append(ignored, "examples/frontdesk", "src/livekit") {
		path := filepath.Join(root, filepath.FromSlash(dir))
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, "agent.py"), nil, 0o644))
	}
	watcher, err := newAgentFileWatcher(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, watcher.Close()) })
	// The list checks the actual recursive registration, including that none of
	// the ignored trees' ancestors were registered before pruning their leaves.
	require.ElementsMatch(t, []string{
		root, filepath.Join(root, "rust"), filepath.Join(root, "examples"),
		filepath.Join(root, "examples", "frontdesk"), filepath.Join(root, "src"),
		filepath.Join(root, "src", "livekit"),
	}, watcher.WatchList())
}
