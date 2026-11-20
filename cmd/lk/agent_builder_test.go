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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// statusServer answers each builder agent get with the next of statuses,
// repeating the last, each with deployedAt. reads counts the gets.
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
		fmt.Fprintf(w, `{"builderAgent":{"id":"ab_1","deployment":{"status":"DEPLOYMENT_STATUS_%s","deployedAt":%q}}}`,
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
	for _, tc := range []struct {
		name       string
		deployedAt time.Time
		after      time.Time
		statuses   []string
		wantErr    string
	}{
		{"runs after building", before, after, []string{"BUILDING", "STARTING", "RUNNING"}, ""},
		{"a previous deployment's RUNNING doesn't end the wait", before, after, []string{"RUNNING", "RUNNING", "BUILDING", "RUNNING"}, ""},
		{"RUNNING with a newer deployedAt ends the wait", later, after, []string{"RUNNING"}, ""},
		{"without a server time, only a build ends the wait", later, time.Time{}, []string{"RUNNING", "BUILDING", "RUNNING"}, ""},
		{"SLEEPING after building ends the wait", before, after, []string{"BUILDING", "STARTING", "SLEEPING"}, ""},
		{"a previous deployment's SLEEPING doesn't end the wait", before, after, []string{"SLEEPING", "BUILDING", "SLEEPING"}, ""},
		{"a build failure after building fails", before, after, []string{"BUILDING", "BUILD_FAILED"},
			"deployment of builder agent ab_1 failed: BUILD_FAILED"},
		{"a previous deployment's failure doesn't end the wait", before, after, []string{"BUILD_FAILED", "BUILDING", "RUNNING"}, ""},
		{"a start failure fails", before, after, []string{"STARTING", "FAILED"},
			"deployment of builder agent ab_1 failed: FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, reads := statusServer(t, tc.deployedAt, tc.statuses...)
			agent, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", tc.after)
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

func TestUnzipBuilderAgentCode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "code")
	n, err := unzipBuilderAgentCode(zipOf(t, "src/", "src/agent.py", "pyproject.toml", "docs/a/b.md"), dir)
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
			_, err := unzipBuilderAgentCode(zipOf(t, tc.entry), filepath.Join(parent, "code"))
			require.Error(t, err)
			_, statErr := os.Stat(filepath.Join(parent, "evil.py"))
			assert.ErrorIs(t, statErr, fs.ErrNotExist)
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
