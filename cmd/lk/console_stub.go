//go:build !console

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, &cli.Command{
		Name:  "console",
		Usage: "Voice chat with an agent via mic/speakers",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			msg := "console is not included in this build.\n\n"
			if isHomebrewInstall() {
				msg += "\"brew install livekit-cli\" does not include console support.\n" +
					"Install with console support:\n" +
					"  brew tap livekit/livekit && brew install lk\n"
			} else {
				msg += "Install with console support:\n" +
					"  https://docs.livekit.io/intro/basics/cli/start/\n"
			}
			msg += "\nOr build from source:\n" +
				"  go build -tags console ./cmd/lk"
			return fmt.Errorf("%s", msg)
		},
	})
}

func isHomebrewInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	return strings.Contains(resolved, "/Cellar/")
}
