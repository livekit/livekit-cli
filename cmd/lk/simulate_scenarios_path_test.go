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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// resolveScenariosPath runs scenariosPathOrDefault with args, from a temp
// working directory holding the named files.
func resolveScenariosPath(t *testing.T, files []string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("scenarios: []\n"), 0o644))
	}
	t.Chdir(dir)

	var got string
	cmd := &cli.Command{
		Flags:  []cli.Flag{&cli.StringFlag{Name: "scenarios"}},
		Action: func(_ context.Context, cmd *cli.Command) error { got = scenariosPathOrDefault(cmd); return nil },
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"simulate"}, args...)))
	return got
}

func TestScenariosPathOrDefault(t *testing.T) {
	t.Run("picks up scenarios.yaml in the working directory", func(t *testing.T) {
		assert.Equal(t, defaultScenariosFile, resolveScenariosPath(t, []string{defaultScenariosFile}))
	})

	t.Run("empty when no scenarios file exists", func(t *testing.T) {
		assert.Equal(t, "", resolveScenariosPath(t, nil))
	})

	t.Run("explicit --scenarios wins over the default", func(t *testing.T) {
		assert.Equal(t, "other.yaml", resolveScenariosPath(t,
			[]string{defaultScenariosFile, "other.yaml"}, "--scenarios", "other.yaml"))
	})

	t.Run("explicit --scenarios is not second-guessed when missing", func(t *testing.T) {
		assert.Equal(t, "missing.yaml", resolveScenariosPath(t,
			[]string{defaultScenariosFile}, "--scenarios", "missing.yaml"))
	})
}
