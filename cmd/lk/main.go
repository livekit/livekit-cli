// Copyright 2021-2024 LiveKit, Inc.
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
	"os/signal"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"

	livekitcli "github.com/livekit/livekit-cli/v2"
)

func main() {
	app := &cli.Command{
		Name:                   "lk",
		Usage:                  "CLI client to LiveKit",
		Description:            "A suite of command line utilities allowing you to access LiveKit APIs services, interact with rooms in realtime, and perform load testing simulations.",
		Version:                livekitcli.Version,
		EnableShellCompletion:  true,
		Suggest:                true,
		HideHelpCommand:        true,
		UseShortOptionHandling: true,
		Flags:                  globalFlags,
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
		Before: initLogger,
	}

	app.Commands = append(app.Commands, AppCommands...)
	app.Commands = append(app.Commands, AgentCommands...)
	app.Commands = append(app.Commands, CloudCommands...)
	app.Commands = append(app.Commands, ProjectCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, DispatchCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, IngressCommands...)
	app.Commands = append(app.Commands, SIPCommands...)
	app.Commands = append(app.Commands, PhoneNumberCommands...)
	app.Commands = append(app.Commands, ReplayCommands...)
	app.Commands = append(app.Commands, PerfCommands...)

	// Register cleanup hook for SIGINT, SIGTERM, SIGQUIT
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT,
	)
	defer stop()

	// Cleanup on hooked signals, remembering to flush stdout
	// before exit to prevent line rag in case of SIGINT
	go func() {
		<-ctx.Done()
		stop()
	}()

	checkForLegacyName()

	if err := app.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkForLegacyName() {
	if !(strings.HasSuffix(os.Args[0], "lk") || strings.HasSuffix(os.Args[0], "lk.exe")) {
		fmt.Fprintf(
			os.Stderr,
			"\n~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~ DEPRECATION NOTICE ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n"+
				"The `livekit-cli` binary has been renamed to `lk`, and some of the options and\n"+
				"commands have changed. Though legacy commands my continue to work, they have\n"+
				"been hidden from the USAGE notes and may be removed in future releases."+
				"\n~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n\n",
		)
	}
}

func initLogger(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	logConfig := &logger.Config{
		Level: "info",
	}
	if cmd.Bool("verbose") {
		logConfig.Level = "debug"
	}
	logger.InitFromConfig(logConfig, "lk")
	lksdk.SetLogger(logger.GetLogger())

	return nil, nil
}

func generateFishCompletion(ctx context.Context, cmd *cli.Command) error {
	fishScript, err := cmd.ToFishCompletion()
	if err != nil {
		return err
	}

	outPath := cmd.String("out")
	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(fishScript), 0o644); err != nil {
			return err
		}
	} else {
		fmt.Println(fishScript)
	}

	return nil
}
