// Copyright 2024 LiveKit, Inc.
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

package bootstrap

var (
	DefaultPythonBootstrapComponent = &BootstrapConfig{
		Target:   TargetPython,
		Requires: []string{"python3", "pip3"},
		Install: []string{
			"python3 -m venv .venv",
			"bash -c \"source .venv/bin/activate\"",
			"pip3 install -r requirements.txt",
		},
		InstallWin: []string{
			"python3 -m venv .venv",
			"powershell .\\.venv\\bin\\Activate.ps1",
			"pip3 install -r requirements.txt",
		},
		Dev: []string{"python3 agent.py start"},
	}
)
