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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveAppTargetNamed(t *testing.T) {
	name, dir, err := resolveAppTarget("my-agent")
	require.NoError(t, err)
	require.Equal(t, "my-agent", name)
	require.Equal(t, "my-agent", dir)
}

func TestResolveAppTargetCurrentDir(t *testing.T) {
	root := t.TempDir()

	empty := filepath.Join(root, "voice-app")
	require.NoError(t, os.Mkdir(empty, 0o755))
	t.Chdir(empty)
	name, dir, err := resolveAppTarget(".")
	require.NoError(t, err)
	require.Equal(t, "voice-app", name)
	require.Equal(t, ".", dir)

	busy := filepath.Join(root, "busy")
	require.NoError(t, os.Mkdir(busy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(busy, "notes.txt"), nil, 0o644))
	t.Chdir(busy)
	_, _, err = resolveAppTarget(".")
	require.ErrorContains(t, err, "isn't empty")

	badName := filepath.Join(root, "has space")
	require.NoError(t, os.Mkdir(badName, 0o755))
	t.Chdir(badName)
	_, _, err = resolveAppTarget(".")
	require.ErrorContains(t, err, "can't be used as the app name")
}
