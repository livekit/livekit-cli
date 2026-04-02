//go:build !console

package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, &cli.Command{
		Name:  "console",
		Usage: "Voice chat with an agent via mic/speakers",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return fmt.Errorf("console is not included in this build (requires -tags console).\n\n" +
				"Install with console support:\n" +
				"  https://docs.livekit.io/intro/basics/cli/start/\n\n" +
				"Or build from source:\n" +
				"  go build -tags console ./cmd/lk")
		},
	})
}
