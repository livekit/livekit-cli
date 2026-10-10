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

// discardOverlay is an overlay that drops every message.
type discardOverlay struct{}

func (discardOverlay) Send(any) error { return nil }

func TestSessionLogMarkdownBlocks(t *testing.T) {
	l := newSessionLog(discardOverlay{})
	l.Send(overlayTranscript{"transcript", "u1", "user", 0, "How do I start?"})
	l.Send(overlayTranscript{"transcript", "a2", "agent", 0, "Run this."})
	l.Send(overlaySnippet{"snippet", "a2", "c1", "Terminal", "shell", "lk agent init my-agent", false})
	l.Send(overlayTranscript{"transcript", "a2", "agent", 1, "Then run it."})
	l.Send(overlaySnippet{"snippet", "a2", "c2", "Prompt for your coding agent", "prompt", "Add this:\n```python\nprint(1)\n```", false})

	md := l.markdown("/tmp")
	// Text after a card starts its own paragraph, so the fence closes.
	require.Contains(t, md, "**LK:** Run this.\n\nTerminal:\n\n```shell\nlk agent init my-agent\n```\n\nThen run it.")
	// A fence inside the code can't close the card's fence.
	require.Contains(t, md, "````\nAdd this:\n```python\nprint(1)\n```\n````")
}
