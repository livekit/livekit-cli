package loadtester

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	lksdk "github.com/livekit/server-sdk-go"
	"golang.org/x/sync/errgroup"
)

type LoadTest struct {
	Params
	trackNames map[string]string
}

type Params struct {
	Publishers   int
	Subscribers  int
	AudioBitrate uint32
	VideoBitrate uint32
	Duration     time.Duration

	TesterParams
}

func NewLoadTest(params Params) *LoadTest {
	return &LoadTest{
		Params:     params,
		trackNames: make(map[string]string),
	}
}

func (t *LoadTest) Run() error {
	stats, err := t.run(t.Params)
	if err != nil {
		return err
	}

	// tester results
	summaries := make(map[string]*summary)
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		testerStats := stats[name]
		summaries[name] = getTesterSummary(testerStats)

		w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
		_, _ = fmt.Fprintf(w, "\n%s\t| Track\t| Pkts\t| Bitrate\t| Latency\t| Dropped\n", name)
		trackStatsSlice := make([]*trackStats, 0, len(testerStats.trackStats))
		for _, ts := range testerStats.trackStats {
			trackStatsSlice = append(trackStatsSlice, ts)
		}
		sort.Slice(trackStatsSlice, func(i, j int) bool {
			nameI := t.trackNames[trackStatsSlice[i].trackID]
			nameJ := t.trackNames[trackStatsSlice[j].trackID]
			return strings.Compare(nameI, nameJ) < 0
		})
		for _, trackStats := range trackStatsSlice {
			latency, dropped := formatStrings(
				trackStats.packets, trackStats.latency, trackStats.latencyCount, trackStats.dropped)

			trackName := t.trackNames[trackStats.trackID]
			_, _ = fmt.Fprintf(w, "\t| %s %s\t| %d\t| %s\t| %s\t| %s\n",
				trackName, trackStats.trackID, trackStats.packets,
				formatBitrate(trackStats.bytes, time.Since(trackStats.startedAt)), latency, dropped)
		}
		_ = w.Flush()
	}

	// summary
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nSummary\t| Tester\t| Tracks\t| Latency\t| Total Dropped\n")

	for _, name := range names {
		s := summaries[name]
		sLatency, sDropped := formatStrings(s.packets, s.latency, s.latencyCount, s.dropped)
		_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\n",
			name, s.tracks, s.expected, sLatency, sDropped)
	}

	s := getTestSummary(summaries)
	sLatency, sDropped := formatStrings(s.packets, s.latency, s.latencyCount, s.dropped)
	_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\n",
		"Total", s.tracks, s.expected, sLatency, sDropped)

	_ = w.Flush()
	return nil
}

func (t *LoadTest) RunSuite() error {
	cases := []*struct {
		publishers  int
		subscribers int
		video       bool

		tracks  int64
		latency time.Duration
		dropped float64
	}{
		{publishers: 10, subscribers: 0, video: false},
		{publishers: 10, subscribers: 100, video: false},
		{publishers: 50, subscribers: 0, video: false},
		{publishers: 10, subscribers: 500, video: false},
		{publishers: 100, subscribers: 0, video: false},
		{publishers: 10, subscribers: 1000, video: false},

		{publishers: 9, subscribers: 0, video: true},
		{publishers: 1, subscribers: 100, video: true},
		{publishers: 9, subscribers: 100, video: true},
		{publishers: 1, subscribers: 1000, video: true},
		{publishers: 9, subscribers: 500, video: true},
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nPubs\t| Subs\t| Tracks\t| Audio\t| Video\t| Latency\t| Packet loss\n")

	for _, c := range cases {
		caseParams := Params{
			Publishers:   c.publishers,
			Subscribers:  c.subscribers,
			AudioBitrate: t.AudioBitrate,
			Duration:     t.Duration,
			TesterParams: t.TesterParams,
		}
		videoString := "No"
		if c.video {
			caseParams.VideoBitrate = t.VideoBitrate
			videoString = "Yes"
		}

		stats, err := t.run(caseParams)
		if err != nil {
			return err
		}

		var tracks, packets, dropped, totalLatency, latencyCount int64
		for _, testerStats := range stats {
			for _, trackStats := range testerStats.trackStats {
				tracks++
				packets += trackStats.packets
				dropped += trackStats.dropped
				totalLatency += trackStats.latency
				latencyCount += trackStats.latencyCount
			}
		}
		latency := time.Duration(totalLatency / latencyCount)

		_, _ = fmt.Fprintf(w, "%d\t| %d\t| %d\t| Yes\t| %s\t| %v\t| %.3f%%\n",
			c.publishers, c.subscribers, tracks, videoString, latency.Round(time.Microsecond*100), 100*float64(dropped)/float64(dropped+packets))
	}

	_ = w.Flush()
	return nil
}

func (t *LoadTest) run(params Params) (map[string]*testerStats, error) {
	if params.Room == "" {
		params.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
	}
	params.IdentityPrefix = randStringRunes(5)

	expectedTracks := params.Publishers
	if params.VideoBitrate > 0 {
		expectedTracks *= 2
	}

	testers := make([]*LoadTester, 0)
	group := errgroup.Group{}
	for i := 0; i < params.Publishers+params.Subscribers; i++ {
		testerParams := params.TesterParams
		testerParams.sequence = i
		testerParams.expectedTracks = expectedTracks
		if i < params.Publishers {
			if params.VideoBitrate > 0 {
				testerParams.expectedTracks -= 2
			} else {
				testerParams.expectedTracks--
			}

			testerParams.name = fmt.Sprintf("Pub %d", i)
		} else {
			testerParams.name = fmt.Sprintf("Sub %d", i-params.Publishers)
		}

		tester := NewLoadTester(testerParams)
		testers = append(testers, tester)

		idx := i
		group.Go(func() error {
			if err := tester.Start(); err != nil {
				return err
			}

			if idx < params.Publishers {
				audio, err := tester.PublishTrack("audio", lksdk.TrackKindAudio, params.AudioBitrate)
				if err != nil {
					return err
				}
				t.trackNames[audio] = fmt.Sprintf("%dA", testerParams.sequence)

				if params.VideoBitrate > 0 {
					video, err := tester.PublishTrack("video", lksdk.TrackKindVideo, params.VideoBitrate)
					if err != nil {
						return err
					}
					t.trackNames[video] = fmt.Sprintf("%dV", testerParams.sequence)
				}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	if params.Duration != 0 {
		go func() {
			<-time.After(params.Duration)
			close(done)
		}()
	}
	<-done

	stats := make(map[string]*testerStats)
	for _, t := range testers {
		t.Stop()
		stats[t.params.name] = t.GetStats()
	}

	return stats, nil
}

func (t *LoadTest) FindMax(maxLatency time.Duration) error {
	if t.Room == "" {
		t.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
	}
	if t.IdentityPrefix == "" {
		t.IdentityPrefix = randStringRunes(5)
	}

	testers := make([]*LoadTester, 0)
	if t.Publishers == 0 {
		t.Publishers = 1
	}

	for i := 0; i < t.Publishers; i++ {
		fmt.Printf("Starting publisher %d\n", i)

		testerParams := t.TesterParams
		testerParams.sequence = i
		testerParams.name = fmt.Sprintf("Pub %d", i)

		tester := NewLoadTester(testerParams)
		testers = append(testers, tester)
		if err := tester.Start(); err != nil {
			return err
		}

		if t.AudioBitrate > 0 {
			_, err := tester.PublishTrack("audio", lksdk.TrackKindAudio, t.AudioBitrate)
			if err != nil {
				return err
			}
		}

		if t.VideoBitrate > 0 {
			_, err := tester.PublishTrack("video", lksdk.TrackKindVideo, t.VideoBitrate)
			if err != nil {
				return err
			}
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nTesters\t| Tracks\t| Latency\t| Total OOO\t| Total Dropped\n")

	pubTracks := t.Publishers
	if t.VideoBitrate > 0 {
		pubTracks *= 2
	}

	// expected to handle about 10k tracks, start with 5k
	measure := 5000 / pubTracks
	for i := 0; ; i++ {
		fmt.Printf("Starting subscriber %d\n", i)
		testerParams := t.TesterParams
		testerParams.sequence = i + t.Publishers
		testerParams.name = fmt.Sprintf("Sub %d", i)

		tester := NewLoadTester(testerParams)
		testers = append(testers, tester)
		if err := tester.Start(); err != nil {
			return err
		}

		if i == measure {
			// reset stats before running
			for _, t := range testers {
				t.Reset()
			}
			time.Sleep(time.Second * 30)

			// collect stats
			summaries := make(map[string]*summary)
			for _, t := range testers {
				summaries[testerParams.name] = getTesterSummary(t.GetStats())
			}
			summary := getTestSummary(summaries)

			latency := time.Duration(summary.latency / summary.latencyCount)
			dropRate := formatPercentage(summary.dropped, summary.dropped+summary.packets)
			_, _ = fmt.Fprintf(w, "%d\t| %d\t| %v\t| %s%%\n",
				i, summary.tracks, latency.Round(time.Microsecond*100), dropRate)

			// add more subs (or break)
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
