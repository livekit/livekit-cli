package main

import (
	"fmt"
	"log"
	"os"

	"github.com/go-logr/stdr"
	"github.com/urfave/cli/v2"

	livekitcli "github.com/livekit/livekit-cli"
	"github.com/livekit/protocol/logger"
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
		Version: livekitcli.Version,
	}

	logger.SetLogger(stdr.New(log.Default()), "livekit-cli")

	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, IngressCommands...)
	app.Commands = append(app.Commands, LoadTestCommands...)

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}
