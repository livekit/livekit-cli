package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	lksdk "github.com/livekit/livekit-sdk-go"
	"github.com/urfave/cli/v2"

	livekit_cli "github.com/livekit/livekit-cli"
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
			&cli.IntFlag{
				Name:  "expected-tracks",
				Usage: "total number of expected tracks in the room; use for multi-node tests",
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
	params := livekit_cli.LoadTesterParams{
		URL:            c.String("url"),
		APIKey:         c.String("api-key"),
		APISecret:      c.String("api-secret"),
		IdentityPrefix: c.String("identity-prefix"),
		Room:           c.String("room"),
		AudioBitrate:   uint32(c.Uint64("audio-bitrate")),
		VideoBitrate:   uint32(c.Uint64("video-bitrate")),
	}
	if params.IdentityPrefix == "" {
		params.IdentityPrefix = RandStringRunes(5)
	}
	if params.Room == "" {
		params.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
	}

	if c.Bool("run-all") {
		return runAll(params)
	}

	publishers := c.Int("publishers")
	if maxLatency := c.Duration("max-latency"); maxLatency > 0 {
		return findMaxSubs(publishers, maxLatency, params)
	}

	duration := c.Duration("duration")
	subscribers := c.Int("subscribers")

	var tracksPerPublisher int
	if params.AudioBitrate > 0 {
		tracksPerPublisher++
	}
	if params.VideoBitrate > 0 {
		tracksPerPublisher++
	}

	expectedTotalTracks := c.Int("expected-tracks")
	if expectedTotalTracks == 0 {
		expectedTotalTracks = tracksPerPublisher * publishers
	}

	testers := make([]*livekit_cli.LoadTester, 0, publishers+subscribers)
	for i := 0; i < publishers+subscribers; i++ {
		testerParams := params
		testerParams.Sequence = i

		var name string
		expectedTracks := expectedTotalTracks
		if i < publishers {
			expectedTracks -= tracksPerPublisher
			name = fmt.Sprintf("Pub %d", i+1)
		} else {
			name = fmt.Sprintf("Sub %d", i-publishers+1)
		}

		tester := livekit_cli.NewLoadTester(name, expectedTracks, testerParams)
		testers = append(testers, tester)
		if err := tester.Start(); err != nil {
			return err
		}

		if i < publishers {
			if params.VideoBitrate > 0 {
				err := tester.PublishTrack("video", lksdk.TrackKindVideo, params.VideoBitrate)
				if err != nil {
					return err
				}
			}

			if params.AudioBitrate > 0 {
				err := tester.PublishTrack("audio", lksdk.TrackKindAudio, params.AudioBitrate)
				if err != nil {
					return err
				}
			}
		}
	}

	fmt.Printf("started all %d clients\n", publishers+subscribers)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if duration != 0 {
		go func() {
			<-time.After(duration)
			close(done)
		}()
	}

	<-done

	for _, t := range testers {
		t.Stop()
	}

	livekit_cli.PrintResults(testers)
	return nil
}

func findMaxSubs(pubs int, maxLatency time.Duration, params livekit_cli.LoadTesterParams) error {
	testers := make([]*livekit_cli.LoadTester, 0)

	if pubs == 0 {
		pubs = 1
	}

	for i := 0; i < pubs; i++ {
		fmt.Printf("Starting publisher %d\n", i)

		testerParams := params
		testerParams.Sequence = i
		tester := livekit_cli.NewLoadTester("Pub", 0, testerParams)
		testers = append(testers, tester)
		if err := tester.Start(); err != nil {
			return err
		}
		if params.VideoBitrate > 0 {
			err := tester.PublishTrack("video", lksdk.TrackKindVideo, params.VideoBitrate)
			if err != nil {
				return err
			}
		}
		if params.AudioBitrate > 0 {
			err := tester.PublishTrack("audio", lksdk.TrackKindAudio, params.AudioBitrate)
			if err != nil {
				return err
			}
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nTesters\t| Tracks\t| Latency\t| Total OOO\t| Total Dropped\n")

	pubTracks := pubs
	if params.VideoBitrate > 0 {
		pubTracks *= 2
	}

	// expected to handle about 10k tracks, start with 5k
	measure := 5000 / pubTracks
	for i := 0; ; i++ {
		fmt.Printf("Starting subscriber %d\n", i)
		testerParams := params
		testerParams.Sequence = i + pubs

		name := fmt.Sprintf("Sub %d", i)
		tester := livekit_cli.NewLoadTester(name, 0, testerParams)
		testers = append(testers, tester)
		if err := tester.Start(); err != nil {
			return err
		}

		if i == measure {
			// reset stats before measuring
			for _, t := range testers {
				t.ResetStats()
			}

			time.Sleep(time.Second * 30)
			tracks, latency, oooRate, dropRate := livekit_cli.GetSummary(testers)
			_, _ = fmt.Fprintf(w, "%d\t| %d\t| %v\t| %.4f%%\t| %.4f%%\n", i, tracks, latency, oooRate, dropRate)

			next := measure
			if latency > maxLatency {
				break
			} else if latency < maxLatency/4 {
				next += 1000 / pubTracks
			} else if latency < maxLatency/2 {
				next += 500 / pubTracks
			} else if latency < maxLatency*3/4 {
				next += 100 / pubTracks
			} else if latency < maxLatency*7/8 {
				next += 10 / pubTracks
			}
			if next == measure {
				next++
			}
			measure = next
		}
	}

	for _, t := range testers {
		t.Stop()
	}
	_ = w.Flush()

	return nil
}

func runAll(params livekit_cli.LoadTesterParams) error {
	cases := []*struct {
		publishers  int
		subscribers int
		video       bool

		tracks  int64
		latency time.Duration
		dropped float64
	}{
		{publishers: 1, subscribers: 1, video: false},
		{publishers: 9, subscribers: 0, video: false},
		{publishers: 9, subscribers: 0, video: true},
		{publishers: 9, subscribers: 100, video: false},
		{publishers: 9, subscribers: 100, video: true},
		{publishers: 50, subscribers: 0, video: false},
		{publishers: 9, subscribers: 500, video: false},
		{publishers: 50, subscribers: 0, video: true},
		{publishers: 9, subscribers: 500, video: true},
		{publishers: 100, subscribers: 0, video: false},
	}

	for _, c := range cases {
		params.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
		params.IdentityPrefix = RandStringRunes(5)

		tracksPerPublisher := 1
		if c.video {
			tracksPerPublisher++
		}
		expectedTotalTracks := tracksPerPublisher * c.publishers

		testers := make([]*livekit_cli.LoadTester, 0)
		for i := 0; i < c.publishers+c.subscribers; i++ {
			testerParams := params
			testerParams.Sequence = i

			var name string
			expectedTracks := expectedTotalTracks
			if i < c.publishers {
				expectedTracks -= tracksPerPublisher
				name = fmt.Sprintf("Pub %d", i+1)
			} else {
				name = fmt.Sprintf("Sub %d", i-c.publishers+1)
			}

			tester := livekit_cli.NewLoadTester(name, expectedTracks, testerParams)
			testers = append(testers, tester)
			if err := tester.Start(); err != nil {
				return err
			}

			if i < c.publishers {
				err := tester.PublishTrack("audio", lksdk.TrackKindAudio, params.AudioBitrate)
				if err != nil {
					return err
				}

				if c.video {
					err := tester.PublishTrack("video", lksdk.TrackKindVideo, params.VideoBitrate)
					if err != nil {
						return err
					}
				}
			}
		}

		time.Sleep(time.Second * 30)
		for _, t := range testers {
			t.Stop()
		}
		c.tracks, c.latency, _, c.dropped = livekit_cli.GetSummary(testers)
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nPublishers\t| Subscribers\t| Tracks\t| Audio\t| Video\t| Latency\t| Packet loss\n")
	for _, c := range cases {
		v := "No"
		if c.video {
			v = "Yes"
		}
		_, _ = fmt.Fprintf(w, "%d\t| %d\t| %d\t| Yes\t| %s\t| %v\t| %.4f%%\n",
			c.publishers, c.subscribers, c.tracks, v, c.latency.Round(time.Microsecond*100), c.dropped)
	}
	_ = w.Flush()

	return nil
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
