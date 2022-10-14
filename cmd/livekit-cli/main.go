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
		Name:                 "livekit-cli",
		Usage:                "CLI client to LiveKit",
		Version:              livekitcli.Version,
		EnableBashCompletion: true,
		Suggest:              true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "verbose",
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "generate-fish-completion",
				Action: generateFishCompletion,
				Hidden: true,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "out",
						Aliases: []string{"o"},
					},
				},
			},
		},
	}

	logger.SetLogger(stdr.New(log.Default()), "livekit-cli")

	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, IngressCommands...)
	app.Commands = append(app.Commands, LoadTestCommands...)
	app.Commands = append(app.Commands, ProjectCommands...)

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func generateFishCompletion(c *cli.Context) error {
	fishScript, err := c.App.ToFishCompletion()
	if err != nil {
		return err
	}

	outPath := c.String("out")
	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(fishScript), 0o644); err != nil {
			return err
		}
	} else {
		fmt.Println(fishScript)
	}

	return nil
}
