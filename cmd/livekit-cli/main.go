// Copyright 2023 LiveKit, Inc.
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
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	livekitcli "github.com/livekit/livekit-cli"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
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
		Before: func(c *cli.Context) error {
			logConfig := &logger.Config{
				Level: "info",
			}
			if c.Bool("verbose") {
				logConfig.Level = "debug"
			}
			logger.InitFromConfig(logConfig, "livekit-cli")
			lksdk.SetLogger(logger.GetLogger())

			return nil
		},
	}

	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, IngressCommands...)
	app.Commands = append(app.Commands, LoadTestCommands...)
	app.Commands = append(app.Commands, ProjectCommands...)
	app.Commands = append(app.Commands, SIPCommands...)
	app.Commands = append(app.Commands, ObfuscatedCommands...)

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
