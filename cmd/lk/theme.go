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
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var ThemeCommands = []*cli.Command{
	{
		Name:      "set-theme",
		Usage:     "Set the CLI color theme",
		UsageText: "lk set-theme THEME",
		ArgsUsage: "THEME (one of: default, livekit)",
		Hidden:    true,
		Before:    loadProjectConfig,
		Action:    setTheme,
	},
}

func setTheme(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("theme is required (one of: default, livekit)")
	}

	// SetTheme validates the name and also applies it, so the confirmation below renders
	// in the newly selected theme.
	if err := util.SetTheme(name); err != nil {
		return err
	}

	cliConfig.Theme = name
	if err := cliConfig.PersistIfNeeded(); err != nil {
		return err
	}

	out.Statusf("Theme set to [%s]", util.Accented(name))
	return nil
}
