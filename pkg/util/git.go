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
)

// IsGitRepository reports whether dir is inside a git working tree. It returns
// false (rather than an error) when git is not installed or dir is not tracked,
// so callers can treat "no git" as a soft, non-fatal condition.
func IsGitRepository(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// GitHeadCommit returns the abbreviated SHA of the current HEAD commit. Git
// picks the shortest prefix that is unambiguous within the repository (min 7
// chars, growing as needed), so the value uniquely identifies the commit in
// this repo. It errors when dir is not a git repository or has no commits yet.
func GitHeadCommit(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "log", "-1", "--pretty=format:%h")
}

// GitCurrentBranch returns the name of the currently checked-out branch. It
// returns an empty string (and no error) when HEAD is detached, since there is
// no meaningful branch to report.
func GitCurrentBranch(ctx context.Context, dir string) (string, error) {
	branch, err := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch == "HEAD" {
		return "", nil
	}
	return branch, nil
}

// HasUncommittedChanges reports whether the working tree at dir has staged or
// unstaged changes, or untracked files (i.e. `git status` is not clean).
func HasUncommittedChanges(ctx context.Context, dir string) (bool, error) {
	out, err := runGit(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// GitWorkingTreeHash returns an abbreviated, content-addressable SHA that
// uniquely identifies the exact current state of the working tree — including
// staged, unstaged, and untracked (but not ignored) files. This is useful for
// distinguishing dirty deploys that share the same HEAD commit. The snapshot is
// built in a throwaway index, so the user's staging area and working tree are
// left untouched. Two identical working trees produce the same hash. Like
// GitHeadCommit, the returned SHA is abbreviated to the shortest prefix that is
// unambiguous within the repository.
func GitWorkingTreeHash(ctx context.Context, dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "lk-git-index")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	// A throwaway index at a fresh (nonexistent) path lets git snapshot the
	// working tree without touching the user's real index.
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	if _, err := runGitEnv(ctx, dir, env, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := runGitEnv(ctx, dir, env, "write-tree")
	if err != nil {
		return "", err
	}
	// write-tree emits the full SHA; abbreviate it the same way as commits.
	// The tree object is now in the object store, so rev-parse can resolve it.
	return runGit(ctx, dir, "rev-parse", "--short", tree)
}

// runGit runs `git -C dir <args...>` in the current process environment and
// returns its trimmed stdout.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	return runGitEnv(ctx, dir, nil, args...)
}

// runGitEnv is runGit with an explicit environment. A nil env inherits the
// current process environment.
func runGitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
