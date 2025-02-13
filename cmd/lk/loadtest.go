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
	"time"

	"github.com/go-logr/logr"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/loadtester"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var LoadTestCommands = []*cli.Command{
	{
		Name:   "load-test",
		Usage:  "Run load tests against LiveKit with simulated publishers & subscribers",
		Action: loadTest,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "room",
				Usage: "`NAME` of the room (default to random name)",
			},
			&cli.DurationFlag{
				Name:  "duration",
				Usage: "`TIME` duration to run, 1m, 1h (by default will run until canceled)",
				Value: 0,
			},
			&cli.IntFlag{
				Name:    "video-publishers",
				Aliases: []string{"publishers"},
				Usage:   "`NUMBER` of participants that would publish video tracks",
			},
			&cli.IntFlag{
				Name:  "audio-publishers",
				Usage: "`NUMBER` of participants that would publish audio tracks",
			},
			&cli.IntFlag{
				Name:  "subscribers",
				Usage: "`NUMBER` of participants that would subscribe to tracks",
			},
			&cli.StringFlag{
				Name:  "identity-prefix",
				Usage: "Identity `PREFIX` of tester participants (defaults to a random prefix)",
			},
			&cli.StringFlag{
				Name:  "video-resolution",
				Usage: "Resolution `QUALITY` of video to publish (\"high\", \"medium\", or \"low\")",
				Value: "high",
			},
			&cli.StringFlag{
				Name:  "video-codec",
				Usage: "`CODEC` \"h264\" or \"vp8\", both will be used when unset",
			},
			&cli.FloatFlag{
				Name:  "num-per-second",
				Usage: "`NUMBER` of testers to start every second",
				Value: 5,
			},
			&cli.StringFlag{
				Name:  "layout",
				Usage: "`LAYOUT` to simulate, choose from \"speaker\", \"3x3\", \"4x4\", \"5x5\"",
				Value: "speaker",
			},
			&cli.BoolFlag{
				Name:  "no-simulcast",
				Usage: "Disables simulcast publishing (simulcast is enabled by default)",
			},
			&cli.BoolFlag{
				Name:  "simulate-speakers",
				Usage: "Fire random speaker events to simulate speaker changes",
			},
			&cli.BoolFlag{
				Name:   "run-all",
				Usage:  "Runs set list of load test cases",
				Hidden: true,
			},
		},
	},
}

func loadTest(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	if !cmd.Bool("verbose") {
		lksdk.SetLogger(logger.LogRLogger(logr.Discard()))
	}
	_ = raiseULimit()

	params := loadtester.Params{
		VideoResolution:  cmd.String("video-resolution"),
		VideoCodec:       cmd.String("video-codec"),
		Duration:         cmd.Duration("duration"),
		NumPerSecond:     cmd.Float("num-per-second"),
		Simulcast:        !cmd.Bool("no-simulcast"),
		SimulateSpeakers: cmd.Bool("simulate-speakers"),
		TesterParams: loadtester.TesterParams{
			URL:            pc.URL,
			APIKey:         pc.APIKey,
			APISecret:      pc.APISecret,
			Room:           cmd.String("room"),
			IdentityPrefix: cmd.String("identity-prefix"),
			Layout:         loadtester.LayoutFromString(cmd.String("layout")),
		},
	}

	if cmd.Bool("run-all") {
		// leave out room name and pub/sub counts
		if params.Duration == 0 {
			params.Duration = time.Second * 15
		}
		test := loadtester.NewLoadTest(params)
		return test.RunSuite(ctx)
	}

	params.VideoPublishers = int(cmd.Int("video-publishers"))
	params.AudioPublishers = int(cmd.Int("audio-publishers"))
	params.Subscribers = int(cmd.Int("subscribers"))

	test := loadtester.NewLoadTest(params)
	return test.Run(ctx)
}
