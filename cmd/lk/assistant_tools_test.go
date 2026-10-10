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

func TestDisplayCommand(t *testing.T) {
	require.Equal(t, "lk agent init . --lang python --install",
		displayCommand([]string{"agent", "init", ".", "--lang", "python", "--install", "-y"}))
	require.Equal(t, "lk skills install --global",
		displayCommand([]string{"skills", "install", "--json", "-y", "--global"}))
}

func TestStripTerminalEscapes(t *testing.T) {
	in := "Visit \x1b]8;;https://cloud.livekit.io\x1b\\\x1b[36;4mhttps://cloud.livekit.io\x1b[0m\x1b]8;;\x1b\\ now"
	require.Equal(t, "Visit https://cloud.livekit.io now", ansiRe.ReplaceAllString(in, ""))
}
