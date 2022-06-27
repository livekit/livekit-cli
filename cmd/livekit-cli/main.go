package main

import (
	"fmt"
	"log"
	"os"

	"github.com/go-logr/stdr"
	"github.com/urfave/cli/v2"

	"github.com/livekit/protocol/logger"

	livekit_cli "github.com/livekit/livekit-cli"
)

func main() {
	app := &cli.App{
		Name:  "livekit-cli",
		Usage: "CLI client to LiveKit",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "verbose",
			},
		},
		Version: livekit_cli.Version,
	}

	logger.SetLogger(stdr.New(log.Default()), "livekit-cli")

	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, RecordCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, LoadTestCommands...)

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}
