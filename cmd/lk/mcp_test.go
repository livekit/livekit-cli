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
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestSummarize(t *testing.T) {
	require.Equal(t, "", summarize(""))
	require.Equal(t, "Read a file.", summarize("  Read a file.\n\nArgs:\n  path: the file\n"))

	long := strings.Repeat("é", actionSummaryMaxRunes+5)
	got := summarize(long)
	require.Equal(t, actionSummaryMaxRunes, utf8.RuneCountInString(got))
	require.True(t, strings.HasSuffix(got, "…"))
}

func TestToolResultValue(t *testing.T) {
	text := func(s string) mcp.Content { return &mcp.TextContent{Text: s} }

	structured := map[string]any{"ok": true}
	require.Equal(t, structured, toolResultValue(&mcp.CallToolResult{
		Content:           []mcp.Content{text(`{"ok":true}`)},
		StructuredContent: structured,
	}))

	require.Equal(t, "a\nb", toolResultValue(&mcp.CallToolResult{
		Content: []mcp.Content{text("a"), text("b")},
	}))

	mixed := []mcp.Content{text("caption"), &mcp.ImageContent{Data: []byte{1}, MIMEType: "image/png"}}
	require.Equal(t, mixed, toolResultValue(&mcp.CallToolResult{Content: mixed}))
}
