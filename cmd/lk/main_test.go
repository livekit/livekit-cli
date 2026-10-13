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
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	livekitcli "github.com/livekit/livekit-cli/v2"
)

// runLK runs the lk entry function in-process against a throwaway home
// directory and returns its exit code, stdout and stderr.
func runLK(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"lk"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := runLK(t, "--version")
	assert.Equal(t, 0, code)
	assert.Equal(t, "lk version "+livekitcli.Version+"\n", stdout)
	assert.Empty(t, stderr)
}

func TestRunUnknownCommand(t *testing.T) {
	// The help action reports an unknown topic as a cli.Exit error with code 3,
	// printed plain rather than in the error style.
	code, stdout, stderr := runLK(t, "nosuchcmd")
	assert.Equal(t, 3, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "No help topic for 'nosuchcmd'. docs\n", stderr)
}

func TestRunError(t *testing.T) {
	// Any other error exits 1 and is printed after cli/v3's own usage report.
	// stderr isn't a terminal, so the error style is stripped.
	code, stdout, stderr := runLK(t, "--nosuchflag")
	assert.Equal(t, 1, code)
	_, help, _ := runLK(t, "--help")
	assert.Equal(t, help, stdout)
	assert.Equal(t, "Incorrect Usage: flag provided but not defined: -nosuchflag\n\n"+
		"Did you mean \"--config\"?\n\n"+
		"flag provided but not defined: -nosuchflag\n", stderr)
}

func TestRunTwiceRendersSameHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"agent", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, first, _ := runLK(t, args...)
			require.Equal(t, 0, code)
			code, second, _ := runLK(t, args...)
			require.Equal(t, 0, code)
			assert.Equal(t, first, second)
		})
	}

	// Each agent category is listed once, not once per run.
	_, help, _ := runLK(t, "agent", "--help")
	for _, category := range []string{agentCategoryLocal, agentCategoryCloud} {
		assert.Equal(t, 1, strings.Count(help, category+":"), "category %q in:\n%s", category, help)
	}
}
