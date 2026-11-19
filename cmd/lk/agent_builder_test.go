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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// statusServer answers each builder agent get with the next of statuses,
// repeating the last, each with deployedAt.
func statusServer(t *testing.T, deployedAt time.Time, statuses ...string) *public.Client {
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
	return client
}

func TestWaitForBuilderDeployment(t *testing.T) {
	prev := builderDeployPoll
	builderDeployPoll = time.Millisecond
	t.Cleanup(func() { builderDeployPoll = prev })

	started := time.Now()
	before, after := started.Add(-time.Hour), started.Add(time.Second)
	for _, tc := range []struct {
		name       string
		deployedAt time.Time
		statuses   []string
		wantErr    string
	}{
		{"runs after building", before, []string{"BUILDING", "STARTING", "RUNNING"}, ""},
		{"a previous deployment's RUNNING doesn't end the wait", before, []string{"RUNNING", "RUNNING", "BUILDING", "RUNNING"}, ""},
		{"RUNNING with a newer deployedAt ends the wait", after, []string{"RUNNING"}, ""},
		{"a build failure after building fails", before, []string{"BUILDING", "BUILD_FAILED"},
			"deployment of builder agent ab_1 failed: BUILD_FAILED"},
		{"a previous deployment's failure doesn't end the wait", before, []string{"BUILD_FAILED", "BUILDING", "RUNNING"}, ""},
		{"a start failure fails", before, []string{"STARTING", "FAILED"},
			"deployment of builder agent ab_1 failed: FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := statusServer(t, tc.deployedAt, tc.statuses...)
			agent, err := waitForBuilderDeployment(context.Background(), client, "p_1", "ab_1", started)
			if tc.wantErr != "" {
				assert.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "ab_1", *agent.Id)
		})
	}
}
