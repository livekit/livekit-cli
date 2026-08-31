// Copyright 2026 LiveKit, Inc.
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

package main

import "testing"

func TestCIFromEnvOutsideGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	if ci := ciFromEnv(); ci != nil {
		t.Fatalf("expected no CI source, got %v", ci)
	}
}

func TestCIFromEnvPullRequest(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REF", "refs/pull/42/merge")
	t.Setenv("GITHUB_REF_NAME", "42/merge")
	t.Setenv("GITHUB_HEAD_REF", "jason/feature")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "livekit/e2e")
	t.Setenv("GITHUB_RUN_ID", "77")
	t.Setenv("GITHUB_ACTOR", "u9g")

	ci := ciFromEnv()
	if ci.GetPullRequest() != "42" {
		t.Errorf("pull request: got %q, want %q", ci.GetPullRequest(), "42")
	}
	if ci.GetRef() != "jason/feature" {
		t.Errorf("ref: got %q, want %q", ci.GetRef(), "jason/feature")
	}
	if want := "https://github.com/livekit/e2e/actions/runs/77"; ci.GetRunUrl() != want {
		t.Errorf("run url: got %q, want %q", ci.GetRunUrl(), want)
	}
}

func TestCIFromEnvBranchPush(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_HEAD_REF", "")

	ci := ciFromEnv()
	if ci.GetRef() != "main" {
		t.Errorf("ref: got %q, want %q", ci.GetRef(), "main")
	}
	if ci.GetPullRequest() != "" {
		t.Errorf("pull request: got %q, want empty", ci.GetPullRequest())
	}
}
