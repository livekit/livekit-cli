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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSimulateConfigWarnings(t *testing.T) {
	// -n with a scenarios file: the flag does nothing, warn.
	warns := simulateConfigWarnings(modeScenarios, 5)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "--num-simulations has no effect")
	require.Contains(t, warns[0], "--concurrency")

	// -n while generating from source: the flag is meaningful, no warning.
	require.Empty(t, simulateConfigWarnings(modeGenerateFromSource, 5))
	// no -n at all: no warning.
	require.Empty(t, simulateConfigWarnings(modeScenarios, 0))
	// view mode ignores everything silently.
	require.Empty(t, simulateConfigWarnings(modeView, 5))
}

func TestViewCommandHintUsesArgv0(t *testing.T) {
	origArgs := os.Args
	origServerURL := serverURL
	t.Cleanup(func() {
		os.Args = origArgs
		serverURL = origServerURL
	})
	serverURL = cloudAPIServerURL

	for _, tc := range []struct {
		name  string
		argv0 string
		want  string
	}{
		{"plain lk", "lk", "lk agent simulate --view run_123"},
		{"path-qualified", "/usr/local/bin/lk", "/usr/local/bin/lk agent simulate --view run_123"},
		{"renamed binary", "lk-dev", "lk-dev agent simulate --view run_123"},
		{"empty argv0 falls back", "", "lk agent simulate --view run_123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = []string{tc.argv0}
			require.Equal(t, tc.want, viewCommandHint("run_123"))
		})
	}
}

func TestViewCommandHintCarriesServerURL(t *testing.T) {
	origArgs := os.Args
	origServerURL := serverURL
	t.Cleanup(func() {
		os.Args = origArgs
		serverURL = origServerURL
	})
	os.Args = []string{"lk"}
	serverURL = "https://cloud-api.staging.livekit.io"

	require.Equal(t,
		"lk agent simulate --view run_123 --server-url https://cloud-api.staging.livekit.io",
		viewCommandHint("run_123"))
}
