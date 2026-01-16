package main

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/livekit/livekit-cli/v2/pkg/loadtester"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/urfave/cli/v3"
)

var (
	PerfCommands = []*cli.Command{
		{
			Name:        "perf",
			Usage:       "Performance testing commands",
			Description: "Commands for running various performance tests",
			Commands: []*cli.Command{
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
						&cli.StringSliceFlag{
							Name:  "attribute",
							Usage: "set attributes in key=value format, can be used multiple times",
						},
						&cli.StringFlag{
							Name:      "attribute-file",
							Usage:     "read attributes from a `JSON` file",
							TakesFile: true,
						},
					},
				},
			},
		},
		{
			Name:   "load-test",
			Usage:  "Run load tests against LiveKit with simulated publishers & subscribers",
			Action: loadTest,
			Hidden: true,
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
)

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

func agentLoadTest(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	if !cmd.Bool("verbose") {
		lksdk.SetLogger(logger.LogRLogger(logr.Discard()))
	}
	_ = raiseULimit()

	participantAttributes, err := parseKeyValuePairs(cmd, "attribute")
	if err != nil {
		log.Printf("failed to parse participant attributes: %v", err)
		return err
	}

	// Read attributes from JSON file if specified
	if attrFile := cmd.String("attribute-file"); attrFile != "" {
		fileData, err := os.ReadFile(attrFile)
		if err != nil {
			log.Printf("failed to read attribute file: %v", err)
			return err
		}

		var fileAttrs map[string]string
		if err := json.Unmarshal(fileData, &fileAttrs); err != nil {
			log.Printf("failed to parse attribute file as JSON: %v", err)
			return err
		}

		// Add attributes from file to the existing ones
		if participantAttributes == nil {
			participantAttributes = make(map[string]string)
		}
		maps.Copy(participantAttributes, fileAttrs)
	}

	params := loadtester.AgentLoadTestParams{
		URL:             pc.URL,
		APIKey:          pc.APIKey,
		APISecret:       pc.APISecret,
		Rooms:           int(cmd.Int("rooms")),
		AgentName:       cmd.String("agent-name"),
		EchoSpeechDelay: cmd.Duration("echo-speech-delay"),
		Duration:        cmd.Duration("duration"),
		ParticipantAttributes: participantAttributes,
	}

	test := loadtester.NewAgentLoadTest(params)

	err = test.Run(ctx, params)
	if err != nil {
		log.Printf("Agent load test failed: %v", err)
		return err
	}
	return nil
}
