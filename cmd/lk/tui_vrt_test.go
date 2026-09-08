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

// Visual regression tests for the two TUIs. Both models compose their frame in a
// plain render() method, so a frame can be captured from a synthetic model state
// without a terminal, a pty, or a running Bubble Tea program, and compared
// against a reference frame under testdata/vrt.
//
// The references hold the frame with ANSI stripped: layout, wrapping, windowing
// and glyphs are what these tests are for, and they are also the part that has
// to survive a terminal-library upgrade unchanged. The escape sequences
// themselves are deliberately not pinned — Lip Gloss encodes the same styling
// differently between major versions (v2 emits underlined runs one grapheme at a
// time, and truecolor where v1 emitted palette indices), so pinning them would
// fail on every upgrade for reasons that never reach the screen.
//
// Regenerate after an intentional UI change:
//
//	UPDATE_TUI_VRT=1 go test ./cmd/lk -run TestVRT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// renderSimulateFrame and renderConsoleFrame are the only two places these tests
// touch the Bubble Tea model API, which keeps the fixtures portable: pointing
// them at an older revision's View() is all it takes to diff two versions of the
// TUI against the same states.
func renderSimulateFrame(m *simulateModel) string { return m.render() }

func renderConsoleFrame(m consoleModel) string { return m.render() }

// Elapsed times are read from the wall clock at render, so they are masked. The
// patterns are anchored to Go's Duration formatting and to the one hand-rolled
// "%dm%02ds" in the run header; fixtures avoid duration-shaped prose so nothing
// else can match.
var (
	durationRe = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|ms|s)\b|\b\d+m\d+(\.\d+)?s\b`)
	// The console's "thinking" spinner picks its frame from time.Now().
	brailleRe = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)
)

// normalizeFrame reduces a rendered frame to the part that must not change:
// styling is stripped, wall-clock-derived text is masked, and trailing padding —
// which a width-setting style adds and which no terminal shows — is dropped.
func normalizeFrame(s string) string {
	s = ansi.Strip(s)
	s = durationRe.ReplaceAllString(s, "<dur>")
	s = brailleRe.ReplaceAllString(s, "<spin>")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// requireFrame compares a normalized frame against testdata/vrt/<name>.txt,
// writing the reference instead when UPDATE_TUI_VRT is set.
func requireFrame(t *testing.T, name, frame string) {
	t.Helper()
	got := normalizeFrame(frame)
	path := filepath.Join("testdata", "vrt", name+".txt")

	if os.Getenv("UPDATE_TUI_VRT") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "no reference frame for %q; record one with `make update-vrt`", name)
	require.Equal(t, string(want), got,
		"%s renders differently than its reference frame.\n"+
			"If the change is intended, re-record with `make update-vrt` and commit the diff.", name)
}
