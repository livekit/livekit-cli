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
	"log"
	"time"

	"github.com/go-logr/logr"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/loadtester"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var AgentLoadTestCommands = []*cli.Command{
	{
		Name:   "agent-load-test",
		Usage:  "Run load tests for a running agent",
		Action: agentLoadTest,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "rooms",
				Usage: "`NUMBER` of rooms to open",
			},
			&cli.StringFlag{
				Name:  "agent-name",
				Usage: "name of the running agent to dispatch to the rooom",
			},
			&cli.DurationFlag{
				Name:  "echo-speech-delay",
				Usage: "delay between when the echo track speaks and when the agent starts speaking (e.g. 5s, 1m)",
				Value: 5 * time.Second,
			},
			&cli.DurationFlag{
				Name:  "duration",
				Usage: "`TIME` duration to run, 1m, 1h (by default will run until canceled)",
				Value: 0,
			},
		},
	},
}

func agentLoadTest(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	if !cmd.Bool("verbose") {
		lksdk.SetLogger(logger.LogRLogger(logr.Discard()))
	}
	_ = raiseULimit()

	params := loadtester.AgentLoadTestParams{
		URL:             pc.URL,
		APIKey:          pc.APIKey,
		APISecret:       pc.APISecret,
		Rooms:           int(cmd.Int("rooms")),
		AgentName:       cmd.String("agent-name"),
		EchoSpeechDelay: cmd.Duration("echo-speech-delay"),
		Duration:        cmd.Duration("duration"),
	}

	test := loadtester.NewAgentLoadTest(params)

	err = test.Run(ctx, params)
	if err != nil {
		log.Printf("Agent load test failed: %v", err)
		return err
	}
	return nil
}
