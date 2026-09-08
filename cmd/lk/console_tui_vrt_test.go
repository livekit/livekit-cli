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

	"github.com/livekit/livekit-cli/v2/pkg/console"
)

// A zero-value pipeline is all the view needs: it reads the FFT bands, the mute
// flag and the playing flag, and each of those is a plain guarded field.
func consoleFixture(textMode bool) consoleModel {
	m := newConsoleModel(&console.AudioPipeline{}, func() {}, nil, "MacBook Pro Microphone", "MacBook Pro Speakers", textMode)
	m.width = 100
	return m
}

func TestVRTConsoleFrames(t *testing.T) {
	tests := []struct {
		name  string
		model func() consoleModel
	}{
		{"console_audio_idle", func() consoleModel { return consoleFixture(false) }},
		{"console_audio_shortcuts", func() consoleModel {
			m := consoleFixture(false)
			m.showShortcuts = true
			return m
		}},
		{"console_audio_partial_transcript", func() consoleModel {
			m := consoleFixture(false)
			m.partialTranscript = "I'd like a table for four"
			return m
		}},
		{"console_audio_metrics", func() consoleModel {
			m := consoleFixture(false)
			m.metricsText = "ttft 320ms"
			return m
		}},
		{"console_text_input", func() consoleModel { return consoleFixture(true) }},
		{"console_text_shortcuts", func() consoleModel {
			m := consoleFixture(true)
			m.showShortcuts = true
			return m
		}},
		{"console_text_waiting", func() consoleModel {
			m := consoleFixture(true)
			m.waitingForAgent = true
			return m
		}},
		{"console_text_audio_error", func() consoleModel {
			m := consoleFixture(true)
			m.audioError = "no input device available"
			return m
		}},
		{"console_shutting_down", func() consoleModel {
			m := consoleFixture(false)
			m.shuttingDown = true
			return m
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireFrame(t, tc.name, renderConsoleFrame(tc.model()))
		})
	}
}
