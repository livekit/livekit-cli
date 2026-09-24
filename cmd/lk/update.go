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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var UpdateCommands = []*cli.Command{
	{
		Name:  "update",
		Usage: "Update the CLI to the latest version",
		Description: `Detects how lk was installed (Homebrew, winget, or the Linux install script)
and runs that installer's upgrade.`,
		Action: updateCLI,
	},
}

func updateCLI(ctx context.Context, cmd *cli.Command) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	args := updateCommandFor(exe)
	if args == nil {
		return fmt.Errorf("can't tell how %s was installed; reinstall it with the instructions at https://docs.livekit.io/intro/basics/cli/start/", exe)
	}

	out.Statusf("Running [%s]", util.Accented(strings.Join(args, " ")))
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// updateCommandFor maps the resolved path of the running binary to the
// command that upgrades it in place, or nil when the install method is unknown.
// The paths are the ones each installer writes to: Homebrew's Cellar, winget's
// Packages directory, and install-cli.sh's INSTALL_PATH.
func updateCommandFor(exe string) []string {
	lower := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	switch {
	case strings.Contains(lower, "/cellar/livekit-cli/"):
		return []string{"brew", "upgrade", "livekit-cli"}
	case strings.Contains(lower, "/winget/packages/livekit.livekitcli"):
		return []string{"winget", "upgrade", "--id", "LiveKit.LiveKitCLI", "--exact"}
	case lower == "/usr/local/bin/lk":
		return []string{"bash", "-c", "curl -sSL https://get.livekit.io/cli | bash"}
	}
	return nil
}
