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
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/stretchr/testify/require"
)

func TestSimLogQuotaExceeded_NoteAndClosingLine(t *testing.T) {
	var buf bytes.Buffer
	l := newSimLog(&buf, &buf)

	l.QuotaExceeded("LLM tokens-per-minute rate limit", 5)
	require.Contains(t, buf.String(), "LLM tokens-per-minute rate limit")
	require.Contains(t, buf.String(), "--concurrency 5")

	// Results repeats the note as a closing line after the per-job output.
	buf.Reset()
	l.Results(&livekit.SimulationRun{}, nil)
	require.Contains(t, buf.String(), "--concurrency 5")
}

func TestSimLogResults_NoQuotaNoteByDefault(t *testing.T) {
	var buf bytes.Buffer
	l := newSimLog(&buf, &buf)
	l.Results(&livekit.SimulationRun{}, nil)
	require.NotContains(t, buf.String(), "--concurrency")
}
