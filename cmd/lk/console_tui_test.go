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
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStopSpinner_ClearsLineBeforeReturning(t *testing.T) {
	// Models the caller printing an error right after stopping the spinner:
	// by the time stop returns, the spinner line must already be cleared, or
	// the error lands on the same line as the leftover "⠋ Starting agent".
	var buf bytes.Buffer
	stop := startSpinnerTo(&buf, "Starting agent")
	stop()
	out := buf.String()
	require.True(t, strings.HasSuffix(out, "\r\033[K"),
		"spinner line not cleared when stop returned; output: %q", out)
}
