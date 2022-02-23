package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	livekitcli "github.com/livekit/livekit-cli"
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
			&cli.Float64Flag{
				Name:  "num-per-second",
				Usage: "number of testers to start every second",
				Value: 10,
			},
			&cli.StringFlag{
				Name:  "layout",
				Usage: "layout to simulate, choose from speaker, 3x3, 4x4, 5x5",
				Value: "speaker",
			},
			&cli.BoolFlag{
				Name:  "run-all",
				Usage: "runs set list of load test cases",
			},
		},
		Action:  loadTest,
		Version: livekitcli.Version,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func loadTest(c *cli.Context) error {
	_ = raiseULimit()
	layout := loadtester.LayoutFromString(c.String("layout"))
	if c.Bool("run-all") {
		// leave out room name and pub/sub counts
		test := loadtester.NewLoadTest(loadtester.Params{
			AudioBitrate: uint32(c.Uint64("audio-bitrate")),
			VideoBitrate: uint32(c.Uint64("video-bitrate")),
			Duration:     c.Duration("duration"),
			NumPerSecond: c.Float64("num-per-second"),
			TesterParams: loadtester.TesterParams{
				URL:            c.String("url"),
				APIKey:         c.String("api-key"),
				APISecret:      c.String("api-secret"),
				IdentityPrefix: c.String("identity-prefix"),
				Layout:         layout,
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
		NumPerSecond: c.Float64("num-per-second"),
		TesterParams: loadtester.TesterParams{
			URL:            c.String("url"),
			APIKey:         c.String("api-key"),
			APISecret:      c.String("api-secret"),
			Room:           c.String("room"),
			IdentityPrefix: c.String("identity-prefix"),
			Layout:         layout,
		},
	})

	if maxLatency := c.Duration("max-latency"); maxLatency > 0 {
		return test.FindMax(maxLatency)
	} else {
		return test.Run()
	}
}

func raiseULimit() error {
	// raise ulimit if on Mac
	var rLimit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		return err
	}
	rLimit.Max = 10000
	rLimit.Cur = 10000
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
}
