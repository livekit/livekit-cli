package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/urfave/cli/v2"

	"github.com/livekit/livekit-cli/pkg/loadtester"
	lksdk "github.com/livekit/server-sdk-go"
)

var LoadTestCommands = []*cli.Command{
	{
		Name:     "load-test",
		Usage:    "Run load tests against LiveKit with simulated publishers & subscribers",
		Category: "Simulate",
		Action:   loadTest,
		Flags: withDefaultFlags(
			&cli.StringFlag{
				Name:  "room",
				Usage: "name of the room (default to random name)",
			},
			&cli.DurationFlag{
				Name:  "duration",
				Usage: "duration to run, 1m, 1h (by default will run until canceled)",
				Value: 0,
			},
			&cli.IntFlag{
				Name:    "video-publishers",
				Aliases: []string{"publishers"},
				Usage:   "number of participants that would publish video tracks",
			},
			&cli.IntFlag{
				Name:  "audio-publishers",
				Usage: "number of participants that would publish audio tracks",
			},
			&cli.IntFlag{
				Name:  "subscribers",
				Usage: "number of participants that would subscribe to tracks",
			},
			&cli.StringFlag{
				Name:  "identity-prefix",
				Usage: "identity prefix of tester participants (defaults to a random prefix)",
			},
			&cli.StringFlag{
				Name:  "video-resolution",
				Usage: "resolution of video to publish. valid values are: high, medium, or low",
				Value: "high",
			},
			&cli.StringFlag{
				Name:  "video-codec",
				Usage: "h264 or vp8, both will be used when unset",
			},
			&cli.Float64Flag{
				Name:  "num-per-second",
				Usage: "number of testers to start every second",
				Value: 5,
			},
			&cli.StringFlag{
				Name:  "layout",
				Usage: "layout to simulate, choose from speaker, 3x3, 4x4, 5x5",
				Value: "speaker",
			},
			&cli.BoolFlag{
				Name:  "no-simulcast",
				Usage: "disables simulcast publishing (simulcast is enabled by default)",
			},
			&cli.BoolFlag{
				Name:   "run-all",
				Usage:  "runs set list of load test cases",
				Hidden: true,
			},
		),
	},
}

func loadTest(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	if !c.Bool("verbose") {
		lksdk.SetLogger(logr.Discard())
	}
	_ = raiseULimit()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-done
		cancel()
	}()

	layout := loadtester.LayoutFromString(c.String("layout"))
	params := loadtester.Params{
		Context:         ctx,
		VideoResolution: c.String("video-resolution"),
		VideoCodec:      c.String("video-codec"),
		Duration:        c.Duration("duration"),
		NumPerSecond:    c.Float64("num-per-second"),
		Simulcast:       !c.Bool("no-simulcast"),
		TesterParams: loadtester.TesterParams{
			URL:            pc.URL,
			APIKey:         pc.APIKey,
			APISecret:      pc.APISecret,
			Room:           c.String("room"),
			IdentityPrefix: c.String("identity-prefix"),
			Layout:         layout,
		},
	}

	if c.Bool("run-all") {
		// leave out room name and pub/sub counts
		test := loadtester.NewLoadTest(params)
		if test.Params.Duration == 0 {
			test.Params.Duration = time.Second * 15
		}
		return test.RunSuite()
	}

	params.VideoPublishers = c.Int("video-publishers")
	params.AudioPublishers = c.Int("audio-publishers")
	params.Subscribers = c.Int("subscribers")
	test := loadtester.NewLoadTest(params)

	return test.Run()
}
