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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/urfave/cli/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const latestReleaseURL = "https://api.github.com/repos/livekit/livekit-cli/releases/latest"

var UpdateCommands = []*cli.Command{
	{
		Name:  "update",
		Usage: "Update the CLI to the latest version",
		Description: `Detects how lk was installed (Homebrew, winget, or the Linux install script)
and runs that installer's upgrade.`,
		Action: updateCLI,
	},
	{
		Name:   "can-update",
		Usage:  "Check whether a newer CLI version is available",
		Action: canUpdateCLI,
	},
}

// commandNotFound keeps urfave/cli's default message and exit code 3, and adds
// how to check for and install a newer lk, since the command may be newer than
// this build.
func commandNotFound(_ context.Context, cmd *cli.Command, name string) {
	msg := fmt.Sprintf("No help topic for '%v'", name)
	if cmd.Suggest {
		if suggestion := cli.SuggestCommand(cmd.Commands, name); suggestion != "" {
			msg += ". " + suggestion
		}
	}
	msg += "\n\nIf this command is new, your lk may be out of date. Run `lk can-update` to check and `lk update` to update."
	cli.HandleExitCoder(cli.Exit(msg, 3))
}

func setCommandNotFound(cmds []*cli.Command) {
	for _, c := range cmds {
		c.CommandNotFound = commandNotFound
		setCommandNotFound(c.Commands)
	}
}

func canUpdateCLI(ctx context.Context, cmd *cli.Command) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checking the latest release: %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return err
	}
	latest, err := semver.NewVersion(release.TagName)
	if err != nil {
		return err
	}
	current, err := semver.NewVersion(livekitcli.Version)
	if err != nil {
		return err
	}

	if current.LessThan(latest) {
		out.Statusf("Yes: %s is available (you have %s). Run [%s] to update.", latest, current, util.Accented("lk update"))
	} else {
		out.Statusf("No: %s is the latest version.", current)
	}
	return nil
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
