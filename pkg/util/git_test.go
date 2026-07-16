// Copyright 2025 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initTestRepo creates a git repo in a fresh temp dir and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	git("init")
	// Local identity so commits succeed regardless of global config.
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestIsGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	repo := initTestRepo(t)
	require.True(t, IsGitRepository(ctx, repo))

	// A plain temp dir with no git metadata.
	require.False(t, IsGitRepository(ctx, t.TempDir()))
}

func TestGitHeadCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo := initTestRepo(t)

	// No commits yet: should error rather than return a bogus SHA.
	_, err := GitHeadCommit(ctx, repo)
	require.Error(t, err)

	writeFile(t, repo, "a.txt", "hello")
	require.NoError(t, exec.Command("git", "-C", repo, "add", ".").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "commit", "-m", "init").Run())

	sha, err := GitHeadCommit(ctx, repo)
	require.NoError(t, err)
	// Abbreviated: shorter than a full 40-char SHA, and a prefix of it.
	require.NotEmpty(t, sha)
	require.Less(t, len(sha), 40)
	full, err := runGit(ctx, repo, "rev-parse", "HEAD")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(full, sha))
}

func TestHasUncommittedChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo := initTestRepo(t)

	writeFile(t, repo, "a.txt", "hello")
	require.NoError(t, exec.Command("git", "-C", repo, "add", ".").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "commit", "-m", "init").Run())

	// Clean tree.
	dirty, err := HasUncommittedChanges(ctx, repo)
	require.NoError(t, err)
	require.False(t, dirty)

	// Untracked file makes it dirty.
	writeFile(t, repo, "b.txt", "world")
	dirty, err = HasUncommittedChanges(ctx, repo)
	require.NoError(t, err)
	require.True(t, dirty)
}

func TestGitCurrentBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo := initTestRepo(t)

	writeFile(t, repo, "a.txt", "hello")
	require.NoError(t, exec.Command("git", "-C", repo, "add", ".").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "commit", "-m", "init").Run())
	// Rename to a deterministic branch name (init default varies by git version).
	require.NoError(t, exec.Command("git", "-C", repo, "branch", "-M", "main").Run())

	branch, err := GitCurrentBranch(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, "main", branch)

	// Detached HEAD reports no branch.
	sha, err := GitHeadCommit(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, exec.Command("git", "-C", repo, "checkout", sha).Run())
	branch, err = GitCurrentBranch(ctx, repo)
	require.NoError(t, err)
	require.Empty(t, branch)
}

func TestGitWorkingTreeHash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo := initTestRepo(t)

	writeFile(t, repo, "a.txt", "hello")
	require.NoError(t, exec.Command("git", "-C", repo, "add", ".").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "commit", "-m", "init").Run())

	// Deterministic: same working tree yields the same hash.
	clean1, err := GitWorkingTreeHash(ctx, repo)
	require.NoError(t, err)
	clean2, err := GitWorkingTreeHash(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, clean1, clean2)

	// Modifying a tracked file changes the hash.
	writeFile(t, repo, "a.txt", "changed")
	modified, err := GitWorkingTreeHash(ctx, repo)
	require.NoError(t, err)
	require.NotEqual(t, clean1, modified)

	// An untracked file also changes the hash.
	writeFile(t, repo, "b.txt", "new")
	untracked, err := GitWorkingTreeHash(ctx, repo)
	require.NoError(t, err)
	require.NotEqual(t, modified, untracked)

	// The user's real index/worktree must be untouched: the modification to
	// a.txt is still reported as an unstaged change (" M", not staged "M ").
	// Read raw output here; runGit trims the leading porcelain column.
	raw, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	require.NoError(t, err)
	require.Contains(t, string(raw), " M a.txt")
	require.Contains(t, string(raw), "?? b.txt")
}
