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

func TestRedactArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "value after the flag",
			args: []string{"uv", "run", "python", "-m", "livekit.agents", "start", "agent.py", "--api-key", "APIkey", "--api-secret", "s3cret", "--simulation"},
			want: []string{"uv", "run", "python", "-m", "livekit.agents", "start", "agent.py", "--api-key", "APIkey", "--api-secret", "[redacted]", "--simulation"},
		},
		{
			name: "value joined with =",
			args: []string{"node", "main.js", "start", "--api-secret=s3cret"},
			want: []string{"node", "main.js", "start", "--api-secret=[redacted]"},
		},
		{
			name: "flag without a value",
			args: []string{"node", "main.js", "start", "--api-secret"},
			want: []string{"node", "main.js", "start", "--api-secret"},
		},
		{
			name: "no secret",
			args: []string{"node", "main.js", "console", "--connect-addr", "127.0.0.1:1234"},
			want: []string{"node", "main.js", "console", "--connect-addr", "127.0.0.1:1234"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, redactArgs(tc.args))
		})
	}
}
