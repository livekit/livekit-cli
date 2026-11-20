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

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// statusServer answers each Agent Builder agent get with the next of statuses,
// repeating the last, each with deployedAt; a status that is a number is an
// HTTP error with that code instead. reads counts the gets.
func statusServer(t *testing.T, deployedAt time.Time, statuses ...string) (client *public.Client, reads func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		status := statuses[min(n, len(statuses)-1)]
		n++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if code, err := strconv.Atoi(status); err == nil {
			w.WriteHeader(code)
			fmt.Fprintf(w, `{"code":14,"message":"%s"}`, http.StatusText(code))
			return
		}
		fmt.Fprintf(w, `{"agent":{"id":"ab_1","deployment":{"status":"DEPLOYMENT_STATUS_%s","deployedAt":%q}}}`,
			status, deployedAt.UTC().Format(time.RFC3339Nano))
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "st_1")
	require.NoError(t, err)
	return client, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func TestWaitForBuilderDeployment(t *testing.T) {
	prev := builderDeployPoll
	builderDeployPoll = time.Millisecond
	t.Cleanup(func() { builderDeployPoll = prev })

	// after is the server's time of the deploy; the local clock plays no part.
	after := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	before, later := after.Add(-time.Hour), after.Add(time.Minute)
	const (
		failedBefore = true
		upBefore     = false
	)
	for _, tc := range []struct {
		name         string
		deployedAt   time.Time
		after        time.Time
		failedBefore bool
		statuses     []string
		wantErr      string
	}{
		{"runs after building", before, after, upBefore, []string{"BUILDING", "STARTING", "RUNNING"}, ""},
		{"a previous deployment's RUNNING doesn't end the wait", before, after, upBefore, []string{"RUNNING", "RUNNING", "BUILDING", "RUNNING"}, ""},
		{"RUNNING with a newer deployedAt ends the wait", later, after, upBefore, []string{"RUNNING"}, ""},
		{"without a server time, only a build ends the wait", later, time.Time{}, upBefore, []string{"RUNNING", "BUILDING", "RUNNING"}, ""},
		{"SLEEPING after building ends the wait", before, after, upBefore, []string{"BUILDING", "STARTING", "SLEEPING"}, ""},
		{"a previous deployment's SLEEPING doesn't end the wait", before, after, upBefore, []string{"SLEEPING", "BUILDING", "SLEEPING"}, ""},
		{"a build failure after building fails", before, after, failedBefore, []string{"BUILDING", "BUILD_FAILED"},
			"deployment of Agent Builder agent ab_1 failed: BUILD_FAILED"},
		{"a start failure fails", before, after, failedBefore, []string{"STARTING", "FAILED"},
			"deployment of Agent Builder agent ab_1 failed: FAILED"},
		{"a failure fails when the agent hadn't failed before", before, after, upBefore, []string{"BUILD_FAILED"},
			"deployment of Agent Builder agent ab_1 failed: BUILD_FAILED"},
		{"a failure after temporary errors fails when the agent hadn't failed before", before, after, upBefore, []string{"503", "503", "BUILD_FAILED"},
			"deployment of Agent Builder agent ab_1 failed: BUILD_FAILED"},
		{"a previous deployment's failure doesn't end the wait", before, after, failedBefore, []string{"BUILD_FAILED", "BUILDING", "RUNNING"}, ""},
		{"after an earlier failure, a failure after temporary errors waits for a build", before, after, failedBefore, []string{"503", "BUILD_FAILED", "BUILDING", "RUNNING"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, reads := statusServer(t, tc.deployedAt, tc.statuses...)
			agent, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", deployBaseline{after: tc.after, failedBefore: tc.failedBefore})
			if tc.wantErr != "" {
				assert.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "ab_1", *agent.Id)
			assert.Equal(t, len(tc.statuses), reads(), "the wait ends on the last status")
		})
	}
}

func TestWaitForBuilderDeploymentEndsEarly(t *testing.T) {
	prev := builderDeployPoll
	builderDeployPoll = time.Millisecond
	t.Cleanup(func() { builderDeployPoll = prev })
	after := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	const following = "The deployment continues; follow it with: lk agent builder get ab_1"
	// flaky answers failures gets with the HTTP status code, then a RUNNING
	// agent deployed after the deploy.
	flaky := func(t *testing.T, failures, code int) *public.Client {
		statuses := slices.Repeat([]string{strconv.Itoa(code)}, failures)
		client, _ := statusServer(t, after.Add(time.Minute), append(statuses, "RUNNING")...)
		return client
	}

	t.Run("temporary errors are retried", func(t *testing.T) {
		client := flaky(t, builderPollRetries, http.StatusServiceUnavailable)
		_, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", deployBaseline{after: after})
		assert.NoError(t, err)
	})
	t.Run("too many temporary errors stop the wait", func(t *testing.T) {
		client := flaky(t, builderPollRetries+1, http.StatusServiceUnavailable)
		_, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", deployBaseline{after: after})
		assert.EqualError(t, err, "Service Unavailable\n"+following)
	})
	t.Run("an error that won't pass stops the wait at once", func(t *testing.T) {
		client := flaky(t, 1, http.StatusNotFound)
		_, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", deployBaseline{after: after})
		assert.EqualError(t, err, "Not Found\n"+following)
	})
	t.Run("cancel isn't the timeout", func(t *testing.T) {
		client, _ := statusServer(t, after, "BUILDING")
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(20*time.Millisecond, cancel)
		_, err := waitForBuilderDeployment(ctx, client, "p_1", "ab_1", deployBaseline{after: after})
		assert.EqualError(t, err, "stopped waiting. "+following)
	})
	t.Run("the timeout says how long it waited", func(t *testing.T) {
		client, _ := statusServer(t, after, "BUILDING")
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := waitForBuilderDeployment(ctx, client, "p_1", "ab_1", deployBaseline{after: after})
		assert.EqualError(t, err, "stopped waiting after "+buildTimeout.String()+". "+following)
	})
}

// zipOf builds a zip with the given entries: a name ending in / is a
// directory, a name ending in @ a symbolic link, and any other a file.
func zipOf(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		h := &zip.FileHeader{Name: strings.TrimSuffix(name, "@")}
		switch {
		case strings.HasSuffix(name, "/"):
			h.SetMode(fs.ModeDir | 0o755)
		case strings.HasSuffix(name, "@"):
			h.SetMode(fs.ModeSymlink | 0o777)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		if !strings.HasSuffix(name, "/") {
			_, err = w.Write([]byte("contents of " + name))
			require.NoError(t, err)
		}
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestUnzipAgentBuilderAgentCode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "code")
	n, err := unzipAgentBuilderAgentCode(zipOf(t, "src/", "src/agent.py", "pyproject.toml", "docs/a/b.md"), dir)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	got, err := os.ReadFile(filepath.Join(dir, "src", "agent.py"))
	require.NoError(t, err)
	assert.Equal(t, "contents of src/agent.py", string(got))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "docs", "a", "b.md"))
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm(), "the code holds header values")
		info, err = os.Stat(filepath.Join(dir, "docs"))
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())
	}

	for _, tc := range []struct {
		name  string
		entry string
	}{
		{"a path out of the directory", "../evil.py"},
		{"an absolute path", "/tmp/evil.py"},
		{"a symbolic link", "link@"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			_, err := unzipAgentBuilderAgentCode(zipOf(t, tc.entry), filepath.Join(parent, "code"))
			require.Error(t, err)
			_, statErr := os.Stat(filepath.Join(parent, "evil.py"))
			assert.ErrorIs(t, statErr, fs.ErrNotExist)
		})
	}
}

// TestUnzipAgentBuilderAgentCodeCleansUp checks that an unpack that fails partway
// leaves the directory as it was, so a second try isn't refused as "not
// empty".
func TestUnzipAgentBuilderAgentCodeCleansUp(t *testing.T) {
	bad := zipOf(t, "src/", "src/agent.py", "pyproject.toml", "link@")

	created := filepath.Join(t.TempDir(), "code")
	_, err := unzipAgentBuilderAgentCode(bad, created)
	require.EqualError(t, err, "unpack agent code: link is not a file or a directory")
	_, statErr := os.Stat(created)
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "a directory the unpack made is removed")

	existing := t.TempDir()
	_, err = unzipAgentBuilderAgentCode(bad, existing)
	require.Error(t, err)
	entries, err := os.ReadDir(existing)
	require.NoError(t, err)
	assert.Empty(t, entries, "a directory that was there stays, empty")
	assert.NoError(t, requireEmptyDir(existing))

	parents := filepath.Join(t.TempDir(), "a", "b", "code")
	_, err = unzipAgentBuilderAgentCode(bad, parents)
	require.Error(t, err)
	_, statErr = os.Stat(filepath.Dir(filepath.Dir(parents)))
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "parents the unpack made are removed too")
}

// TestUnzipAgentBuilderAgentCodeKeepsOthersFiles checks that an unpack that
// fails removes only what it made. Something else can write to the directory
// after download checks that it's empty, while the zip downloads.
func TestUnzipAgentBuilderAgentCodeKeepsOthersFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		wantErr string
	}{
		{"a later entry fails", []string{"src/", "src/agent.py", "pyproject.toml", "link@"},
			"unpack agent code: link is not a file or a directory"},
		{"an entry is a file that's already there", []string{"src/", "pyproject.toml", "src/theirs.py"},
			"unpack agent code: openat src/theirs.py: file exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "code")
			require.NoError(t, requireEmptyDir(dir))
			// What another process writes between the check and the unpack.
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "theirs.py"), []byte("theirs"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("theirs"), 0o644))

			_, err := unzipAgentBuilderAgentCode(zipOf(t, tc.entries...), dir)
			if runtime.GOOS == "windows" {
				require.Error(t, err)
			} else {
				require.EqualError(t, err, tc.wantErr)
			}

			for _, name := range []string{"notes.txt", filepath.Join("src", "theirs.py")} {
				got, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err, name)
				assert.Equal(t, "theirs", string(got), name)
			}
			_, statErr := os.Stat(filepath.Join(dir, "pyproject.toml"))
			assert.ErrorIs(t, statErr, fs.ErrNotExist, "the unpack's own file is removed")
		})
	}
}

func TestRequireEmptyDir(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, requireEmptyDir(dir))
	assert.NoError(t, requireEmptyDir(filepath.Join(dir, "missing")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), nil, 0o600))
	assert.EqualError(t, requireEmptyDir(dir), "--unzip "+dir+": the directory isn't empty")
}

func TestLineAndColumn(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n}")
	for _, tc := range []struct {
		offset    int64
		line, col int
	}{
		{1, 1, 1},
		{3, 2, 1},
		{int64(len(data)), 3, 1},
		{0, 1, 1},
		{100, 3, 2},
	} {
		line, col := lineAndColumn(data, tc.offset)
		assert.Equal(t, []int{tc.line, tc.col}, []int{line, col}, "offset %d", tc.offset)
	}
}

func TestWritePrivateFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "agent.zip")
	require.NoError(t, os.WriteFile(name, []byte("an older, longer zip"), 0o644))

	assert.ErrorIs(t, writePrivateFile(name, []byte("zip"), false), fs.ErrExist)
	require.NoError(t, writePrivateFile(name, []byte("zip"), true))
	got, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "zip", string(got))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(name)
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm(), "an overwritten file is made private too")
	}
}
