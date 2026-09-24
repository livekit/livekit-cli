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

	"github.com/stretchr/testify/assert"
)

func TestUpdateCommandFor(t *testing.T) {
	cases := []struct {
		exe  string
		want []string
	}{
		{"/opt/homebrew/Cellar/livekit-cli/2.18.4/bin/lk", []string{"brew", "upgrade", "livekit-cli"}},
		{"/home/linuxbrew/.linuxbrew/Cellar/livekit-cli/2.18.4/bin/lk", []string{"brew", "upgrade", "livekit-cli"}},
		{`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\LiveKit.LiveKitCLI_Microsoft.Winget.Source_8wekyb3d8bbwe\lk.exe`, []string{"winget", "upgrade", "--id", "LiveKit.LiveKitCLI", "--exact"}},
		{"/usr/local/bin/lk", []string{"bash", "-c", "curl -sSL https://get.livekit.io/cli | bash"}},
		{"/Users/me/go/bin/lk", nil},
		{"/Users/me/code/livekit-cli/bin/lk", nil},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, updateCommandFor(c.exe), c.exe)
	}
}
