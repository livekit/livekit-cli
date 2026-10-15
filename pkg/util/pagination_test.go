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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNextPageCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "appends the cursor, named lk",
			args: []string{"/usr/local/bin/lk", "--experimental-auth", "analytics", "session", "transcript", "RM_1", "--limit", "20"},
			want: "lk --experimental-auth analytics session transcript RM_1 --limit 20 --cursor c2",
		},
		{
			name: "replaces a prior cursor in either form",
			args: []string{"lk", "workspace", "list", "--cursor", "c1", "--cursor=c0", "--limit", "5"},
			want: "lk workspace list --limit 5 --cursor c2",
		},
		{
			name: "drops credentials",
			args: []string{"lk", "--api-key", "K", "--api-secret=S", "-api-secret", "S2", "project", "list"},
			want: "lk project list --cursor c2",
		},
		{
			name: "quotes for a shell",
			args: []string{"lk", "analytics", "session", "list", "--room", "my room", "--tag", "it's"},
			want: `lk analytics session list --room 'my room' --tag 'it'\''s' --cursor c2`,
		},
		{
			name: "no args",
			want: "lk --cursor c2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextPageCommand(tt.args, "c2"))
		})
	}
	assert.Equal(t, "lk list --cursor 'a b'", NextPageCommand([]string{"lk", "list"}, "a b"))
}

func TestNextPageHint(t *testing.T) {
	prev := CommandArgs
	t.Cleanup(func() { CommandArgs = prev })

	CommandArgs = nil
	assert.Contains(t, NextPageHint("c2"), "--cursor c2")

	CommandArgs = []string{"lk", "user", "list"}
	assert.Contains(t, NextPageHint("c2"), "lk user list --cursor c2")
}
