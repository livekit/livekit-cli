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
		Flags: []cli.Flag{
			urlFlag,
			apiKeyFlag,
			secretFlag,
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
				Name:  "publishers",
				Usage: "number of participants to publish tracks to the room",
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
				Usage: "high, medium, low, or off to disable. publishes sample video in H.264 or VP8",
				Value: "high",
			},
			&cli.StringFlag{
				Name:  "video-codec",
				Usage: "h264 or vp8, both will be used when unset",
			},
			&cli.Uint64Flag{
				Name:  "audio-bitrate",
				Usage: "average bitrate (bps) of audio track to publish, 0 to disable",
				Value: 8000,
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
			&cli.StringFlag{
				Name:  "bright-url",
				Usage: "BrightLive url to hit so the bot user is added to the 'session'",
				Value: "https://us-central1-bright-live-staging.cloudfunctions.net/api/sessions/{{id}}/join-instant-test",
			},
			&cli.StringFlag{
				Name:  "bright-shared-token",
				Usage: "BrightLive shared secret token for bots",
				Value: "tBdzYbo7cQXUh7hrWb921WCIj9TUtlkO",
			},
			&cli.IntFlag{
				Name:  "bright-bot-start-id",
				Usage: "what id to start the bot at, defaults to 1",
				Value: 1,
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
			&cli.BoolFlag{
				Name: "verbose",
			},
		},
	},
}

func loadTest(c *cli.Context) error {
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
		AudioBitrate:    uint32(c.Uint64("audio-bitrate")),
		VideoResolution: c.String("video-resolution"),
		VideoCodec:      c.String("video-codec"),
		Duration:        c.Duration("duration"),
		NumPerSecond:    c.Float64("num-per-second"),
		Simulcast:       !c.Bool("no-simulcast"),
		TesterParams: loadtester.TesterParams{
			URL:               c.String("url"),
			APIKey:            c.String("api-key"),
			APISecret:         c.String("api-secret"),
			Room:              c.String("room"),
			IdentityPrefix:    c.String("identity-prefix"),
			Layout:            layout,
			BrightUrl:         c.String("bright-url"),
			BrightSharedToken: c.String("bright-shared-token"),
			BrightBotStartId:  c.Int("bright-bot-start-id"),
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

	params.Publishers = c.Int("publishers")
	params.Subscribers = c.Int("subscribers")
	test := loadtester.NewLoadTest(params)

	return test.Run()
}
