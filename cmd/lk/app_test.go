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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMissingExecutable(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "go exec error",
			message: `"uv": executable file not found in $PATH`,
			want:    "uv",
		},
		{
			name:    "wrapped go exec error",
			message: `exec: "pnpm": executable file not found in $PATH`,
			want:    "pnpm",
		},
		{
			name:    "windows path spelling",
			message: `exec: "npm": executable file not found in %PATH%`,
			want:    "npm",
		},
		{
			name:    "env error",
			message: "env: node: No such file or directory",
			want:    "node",
		},
		{
			name:    "shell error",
			message: "sh: yarn: command not found",
			want:    "yarn",
		},
		{
			name:    "multiline task output",
			message: "task: [install] uv sync\n\"uv\": executable file not found in $PATH\ntask: Failed to run task \"install\"",
			want:    "uv",
		},
		{
			name:    "unrelated failure",
			message: "package installation failed with exit status 1",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, missingExecutable(tt.message))
		})
	}
}

func TestInstallFailureGuidance(t *testing.T) {
	t.Run("missing executable", func(t *testing.T) {
		got := installFailureGuidance(
			errors.New(`"uv": executable file not found in $PATH`),
			"my-agent",
		)

		assert.Equal(t,
			"`uv` is required but was not found in PATH. Install `uv`, ensure it is available in PATH, then re-run the install step manually in ./my-agent.",
			got,
		)
	})

	t.Run("other install error", func(t *testing.T) {
		got := installFailureGuidance(errors.New("exit status 1"), "my-agent")

		assert.Equal(t,
			"Fix your toolchain, then re-run the install step manually in ./my-agent.",
			got,
		)
	})
}
