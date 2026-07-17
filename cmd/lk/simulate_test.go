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
