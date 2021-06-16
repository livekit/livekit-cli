package main

import (
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v2"

	livekit_cli "github.com/livekit/livekit-cli"
	"github.com/livekit/livekit-cli/pkg/loadtester"
)

func main() {
	app := &cli.App{
		Name:  "livekit-cli",
		Usage: "LiveKit load tester",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "url",
				Usage:    "URL of LiveKit server",
				EnvVars:  []string{"LIVEKIT_URL"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "api-key",
				EnvVars:  []string{"LIVEKIT_API_KEY"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "api-secret",
				EnvVars:  []string{"LIVEKIT_API_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:  "room",
				Usage: "name of the room",
			},
			&cli.DurationFlag{
				Name:  "duration",
				Usage: "duration to run, 1m, 1h, 0 to keep running",
				Value: 0,
			},
			&cli.DurationFlag{
				Name:  "max-latency",
				Usage: "max number of subscribers without going above max latency (e.g. 400ms)",
			},
			&cli.IntFlag{
				Name:  "publishers",
				Usage: "number of participants to publish tracks to the room",
			},
			&cli.IntFlag{
				Name:  "subscribers",
				Usage: "number of participants to join the room",
			},
			&cli.StringFlag{
				Name:  "identity-prefix",
				Usage: "identity prefix of tester participants, defaults to a random name",
			},
			&cli.Uint64Flag{
				Name:  "video-bitrate",
				Usage: "bitrate (bps) of video track to publish, 0 to disable",
				Value: 1000000,
			},
			&cli.Uint64Flag{
				Name:  "audio-bitrate",
				Usage: "bitrate (bps) of audio track to publish, 0 to disable",
				Value: 20000,
			},
			&cli.BoolFlag{
				Name:  "run-all",
				Usage: "runs set list of load test cases",
			},
		},
		Action:  loadTest,
		Version: livekit_cli.Version,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func loadTest(c *cli.Context) error {
	if c.Bool("run-all") {
		// leave out room name and pub/sub counts
		test := loadtester.NewLoadTest(loadtester.Params{
			AudioBitrate: uint32(c.Uint64("audio-bitrate")),
			VideoBitrate: uint32(c.Uint64("video-bitrate")),
			Duration:     c.Duration("duration"),
			TesterParams: loadtester.TesterParams{
				URL:            c.String("url"),
				APIKey:         c.String("api-key"),
				APISecret:      c.String("api-secret"),
				IdentityPrefix: c.String("identity-prefix"),
			},
		})

		if test.Duration == 0 {
			test.Duration = time.Second * 10
		}

		return test.RunSuite()
	}

	test := loadtester.NewLoadTest(loadtester.Params{
		Publishers:   c.Int("publishers"),
		Subscribers:  c.Int("subscribers"),
		AudioBitrate: uint32(c.Uint64("audio-bitrate")),
		VideoBitrate: uint32(c.Uint64("video-bitrate")),
		Duration:     c.Duration("duration"),
		TesterParams: loadtester.TesterParams{
			URL:            c.String("url"),
			APIKey:         c.String("api-key"),
			APISecret:      c.String("api-secret"),
			Room:           c.String("room"),
			IdentityPrefix: c.String("identity-prefix"),
		},
	})

	if maxLatency := c.Duration("max-latency"); maxLatency > 0 {
		return test.FindMax(maxLatency)
	} else {
		return test.Run()
	}
}
