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

	"github.com/stretchr/testify/assert"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

func TestCloudAPIURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"wss://my-proj.livekit.cloud", "https://cloud-api.livekit.io"},
		{"wss://my-proj.staging.livekit.cloud", "https://cloud-api-server-public.ochicago1a.staging.livekit.app"},
		{"http://localhost:7880", ""},
		{"wss://example.com", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			assert.Equal(t, tc.want, cloudAPIURL(tc.url))
		})
	}
}

// A staging project's derived API host is the default for that project, so the
// printed hint must not carry it as --server-url.
func TestSimulateCommandHintOmitsDerivedServerURL(t *testing.T) {
	origPC, origURL := simulateProjectConfig, serverURL
	t.Cleanup(func() { simulateProjectConfig, serverURL = origPC, origURL })

	simulateProjectConfig = &config.ProjectConfig{Name: "stg", URL: "wss://stg.staging.livekit.cloud"}
	serverURL = stagingCloudAPIServerURL
	assert.NotContains(t, simulateCommandHint("view", "SR_1"), "--server-url")

	serverURL = "http://localhost:17770"
	assert.Contains(t, simulateCommandHint("view", "SR_1"), "--server-url http://localhost:17770")
}
