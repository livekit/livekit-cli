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
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

// Real log lines captured from a rate-limited `lk agent simulate` run.
const (
	quotaLineTpm = `14:20:58 WARNING livekit.agents failed to generate LLM completion: message="Error code: 429 - {'type': 'inference_quota_exceeded', 'error': 'LLM token/min rate limit exceeded, category: MaxConcurrentGatewayLLMTpm, remaining_limit: 0, current_usage: 50962, session_id: 3e685b2e, project_id: p_15lqwllkcoh, status: QuotaStatusExceeded', 'category': 'MaxConcurrentGatewayLLMTpm'}", status_code=429, retryable=True`
	quotaLineRpm = `livekit.agents._exceptions.APIStatusError: message="Error code: 429 - {'type': 'inference_quota_exceeded', 'error': 'LLM request/min rate limit exceeded, category: MaxConcurrentGatewayLLMRpm, remaining_limit: 0, current_usage: 58, status: QuotaStatusExceeded'}", status_code=429`
)

func TestDetectQuotaExceeded(t *testing.T) {
	tests := []struct {
		name     string
		logs     []string
		want     bool
		category string
	}{
		{"empty", nil, false, ""},
		{"unrelated error", []string{"ValueError: boom", "connection reset"}, false, ""},
		{"tpm line among noise", []string{"starting agent", quotaLineTpm, "shutting down"}, true, "MaxConcurrentGatewayLLMTpm"},
		{"rpm line", []string{quotaLineRpm}, true, "MaxConcurrentGatewayLLMRpm"},
		{"signal without parseable category", []string{"got inference_quota_exceeded from gateway"}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectQuotaExceeded(tt.logs)
			if !tt.want {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.category, got.category)
		})
	}
}

func TestQuotaInfoDescribe(t *testing.T) {
	require.Equal(t, "LLM tokens-per-minute rate limit", (&quotaInfo{category: "MaxConcurrentGatewayLLMTpm"}).describe())
	require.Equal(t, "LLM requests-per-minute rate limit", (&quotaInfo{category: "MaxConcurrentGatewayLLMRpm"}).describe())
	require.Equal(t, "SomeFutureCategory rate limit", (&quotaInfo{category: "SomeFutureCategory"}).describe())
	require.Equal(t, "an inference rate limit", (&quotaInfo{}).describe())
}

func TestSuggestConcurrency(t *testing.T) {
	tests := []struct {
		name        string
		configured  int32
		peakRunning int
		want        int
	}{
		{"explicit even", 10, 0, 5},
		{"explicit odd floors", 7, 0, 3},
		{"explicit 1 floors at 1", 1, 0, 1},
		{"unset uses peak", 0, 10, 5},
		{"unset odd peak floors", 0, 5, 2},
		{"nothing observed yet", 0, 0, 1},
		{"explicit wins over peak", 4, 100, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, suggestConcurrency(tt.configured, tt.peakRunning))
		})
	}
}

func TestRunningJobCount(t *testing.T) {
	run := &livekit.SimulationRun{Jobs: []*livekit.SimulationRun_Job{
		{Status: livekit.SimulationRun_Job_STATUS_RUNNING},
		{Status: livekit.SimulationRun_Job_STATUS_COMPLETED},
		{Status: livekit.SimulationRun_Job_STATUS_RUNNING},
	}}
	require.Equal(t, 2, runningJobCount(run))
	require.Equal(t, 0, runningJobCount(nil))
}
