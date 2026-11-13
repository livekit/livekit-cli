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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestParseSetFlags(t *testing.T) {
	defines, err := parseSetFlags([]string{"llm=gpt-5", "prompt=a=b", "empty="})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"llm": "gpt-5", "prompt": "a=b", "empty": ""}, defines)

	defines, err = parseSetFlags(nil)
	require.NoError(t, err)
	require.Nil(t, defines)

	for _, bad := range [][]string{{"llm"}, {"=x"}, {"llm=a", "llm=b"}} {
		_, err := parseSetFlags(bad)
		require.Error(t, err, "%v", bad)
	}
}

func TestSetFlagKeepsCommas(t *testing.T) {
	for _, sub := range []*cli.Command{simulateTextCommand, simulateAudioCommand} {
		var got []string
		leaf := *sub
		leaf.Before = nil
		leaf.Action = func(_ context.Context, cmd *cli.Command) error {
			got = cmd.StringSlice("set")
			return nil
		}
		parent := *simulateCommand
		parent.Commands = []*cli.Command{&leaf}
		root := &cli.Command{Name: "lk", Commands: []*cli.Command{&parent}}
		require.NoError(t, root.Run(context.Background(), []string{"lk", "simulate", "--set", "prompt=hello,world", sub.Name, "--set", "pair=a,b=c"}))
		require.Equal(t, []string{"prompt=hello,world", "pair=a,b=c"}, got, sub.Name)
	}
}
