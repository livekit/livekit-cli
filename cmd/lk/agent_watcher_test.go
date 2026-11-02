package main

import (
	"os"
	"os/exec"
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
		".cache/pip", "__pypackages__/3.12", "site-packages/livekit",
		"vendor/dependency", "out/generated", "coverage/report", "_build/html",
		"bower_components/dependency", "Pods/dependency", "Carthage/Build", "elm-stuff/packages",
	}
	for _, dir := range append(ignored, "examples/frontdesk", "src/livekit") {
		path := filepath.Join(root, filepath.FromSlash(dir))
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, "agent.py"), nil, 0o644))
	}
	if _, err := exec.LookPath("git"); err == nil {
		require.NoError(t, exec.Command("git", "init", "--quiet", root).Run())
		require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("cargo-output/\n"), 0o644))
		output := filepath.Join(root, "cargo-output", "debug")
		require.NoError(t, os.MkdirAll(output, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(output, "generated.py"), nil, 0o644))
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
	// A nested agent plus explicitly shared sources must not register the
	// unrelated workspace trees, even when they contain Python files.
	narrow, err := newAgentFileWatcher(filepath.Join(root, "examples", "frontdesk"), filepath.Join(root, "src"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, narrow.Close()) })
	require.ElementsMatch(t, []string{
		filepath.Join(root, "examples", "frontdesk"), filepath.Join(root, "src"), filepath.Join(root, "src", "livekit"),
	}, narrow.WatchList())
}
