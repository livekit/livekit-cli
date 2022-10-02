package loadtester

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/syncmap"
)

type LoadTest struct {
	Params     Params
	trackNames map[string]string
	lock       sync.Mutex
}

type Params struct {
	Context         context.Context
	VideoPublishers int
	AudioPublishers int
	Subscribers     int
	VideoResolution string
	VideoCodec      string
	Duration        time.Duration
	// number of seconds to spin up per second
	NumPerSecond float64
	Simulcast    bool

	TesterParams
}

func NewLoadTest(params Params) *LoadTest {
	l := &LoadTest{
		Params:     params,
		trackNames: make(map[string]string),
	}
	if l.Params.NumPerSecond == 0 {
		// sane default
		l.Params.NumPerSecond = 5
	}
	if l.Params.NumPerSecond > 10 {
		l.Params.NumPerSecond = 10
	}
	if l.Params.VideoPublishers == 0 && l.Params.AudioPublishers == 0 && l.Params.Subscribers == 0 {
		l.Params.VideoPublishers = 1
		l.Params.Subscribers = 1
	}
	return l
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
		if strings.HasPrefix(name, "Pub") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		testerStats := stats[name]
		summaries[name] = getTesterSummary(testerStats)

		w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
		_, _ = fmt.Fprintf(w, "\n%s\t| Track\t| Kind\t| Pkts\t| Bitrate\t| Dropped\n", name)
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
			dropped := formatStrings(
				trackStats.packets.Load(), trackStats.dropped.Load())

			trackName := t.trackNames[trackStats.trackID]
			_, _ = fmt.Fprintf(w, "\t| %s %s\t| %s\t| %d\t| %s\t| %s\n",
				trackName, trackStats.trackID, trackStats.kind, trackStats.packets.Load(),
				formatBitrate(trackStats.bytes.Load(), time.Since(trackStats.startedAt.Load())), dropped)
		}
		_ = w.Flush()
	}

	if len(summaries) == 0 {
		return nil
	}

	// summary
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nSummary\t| Tester\t| Tracks\t| Bitrate\t| Total Dropped\t| Error\n")

	for _, name := range names {
		s := summaries[name]
		sDropped := formatStrings(s.packets, s.dropped)
		sBitrate := formatBitrate(s.bytes, s.elapsed)
		_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\t| %s\n",
			name, s.tracks, s.expected, sBitrate, sDropped, s.errString)
	}

	s := getTestSummary(summaries)
	sDropped := formatStrings(s.packets, s.dropped)
	// avg bitrate per sub
	sBitrate := fmt.Sprintf("%s (%s avg)",
		formatBitrate(s.bytes, s.elapsed),
		formatBitrate(s.bytes/int64(len(summaries)), s.elapsed),
	)
	_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\t| %d\n",
		"Total", s.tracks, s.expected, sBitrate, sDropped, s.errCount)

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
		{publishers: 10, subscribers: 10, video: false},
		{publishers: 10, subscribers: 100, video: false},
		{publishers: 10, subscribers: 500, video: false},
		{publishers: 10, subscribers: 1000, video: false},
		{publishers: 50, subscribers: 50, video: false},
		{publishers: 100, subscribers: 50, video: false},

		{publishers: 10, subscribers: 10, video: true},
		{publishers: 10, subscribers: 100, video: true},
		{publishers: 10, subscribers: 500, video: true},
		{publishers: 1, subscribers: 100, video: true},
		{publishers: 1, subscribers: 1000, video: true},
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nPubs\t| Subs\t| Tracks\t| Audio\t| Video\t| Packet loss\t| Errors\n")

	for _, c := range cases {
		caseParams := t.Params
		videoString := "Yes"
		if c.video {
			caseParams.VideoPublishers = c.publishers
		} else {
			caseParams.AudioPublishers = c.publishers
			videoString = "No"
		}
		caseParams.Subscribers = c.subscribers
		caseParams.Simulcast = true
		if caseParams.Duration == 0 {
			caseParams.Duration = 15 * time.Second
		}
		fmt.Printf("\nRunning test: %d pub, %d sub, video: %s\n", c.publishers, c.subscribers, videoString)

		stats, err := t.run(caseParams)
		if err != nil {
			return err
		}
		if t.Params.Context.Err() != nil {
			return err
		}

		var tracks, packets, dropped, errCount int64
		for _, testerStats := range stats {
			for _, trackStats := range testerStats.trackStats {
				tracks++
				packets += trackStats.packets.Load()
				dropped += trackStats.dropped.Load()
			}
			if testerStats.err != nil {
				errCount++
			}
		}
		_, _ = fmt.Fprintf(w, "%d\t| %d\t| %d\t| Yes\t| %s\t| %.3f%%| %d\t\n",
			c.publishers, c.subscribers, tracks, videoString, 100*float64(dropped)/float64(dropped+packets), errCount)
	}

	_ = w.Flush()
	return nil
}

func (t *LoadTest) run(params Params) (map[string]*testerStats, error) {
	if params.Room == "" {
		params.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
	}
	params.IdentityPrefix = randStringRunes(5)

	expectedTracks := params.VideoPublishers + params.AudioPublishers

	var participantStrings []string
	if t.Params.VideoPublishers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d video publishers", t.Params.VideoPublishers))
	}
	if t.Params.AudioPublishers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d audio publishers", t.Params.AudioPublishers))
	}
	if t.Params.Subscribers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d subscribers", t.Params.Subscribers))
	}
	fmt.Printf("Starting load test with %s, room: %s\n",
		strings.Join(participantStrings, ", "), t.Params.Room)

	testers := make([]*LoadTester, 0)
	group, _ := errgroup.WithContext(t.Params.Context)
	startedAt := time.Now()
	numStarted := float64(0)
	errs := syncmap.Map{}
	maxPublishers := params.VideoPublishers
	if params.AudioPublishers > maxPublishers {
		maxPublishers = params.AudioPublishers
	}
	for i := 0; i < maxPublishers+params.Subscribers; i++ {
		testerParams := params.TesterParams
		testerParams.Sequence = i
		testerParams.expectedTracks = expectedTracks
		isVideoPublisher := i < params.VideoPublishers
		isAudioPublisher := i < params.AudioPublishers
		if isVideoPublisher || isAudioPublisher {
			// publishers would not get their own tracks
			testerParams.expectedTracks = 0
			testerParams.IdentityPrefix += "_pub"
			testerParams.name = fmt.Sprintf("Pub %d", i)
		} else {
			testerParams.Subscribe = true
			testerParams.name = fmt.Sprintf("Sub %d", i-params.VideoPublishers)
		}

		tester := NewLoadTester(testerParams)
		testers = append(testers, tester)

		group.Go(func() error {
			if err := tester.Start(); err != nil {
				fmt.Println(errors.Wrapf(err, "could not connect %s", testerParams.name))
				errs.Store(testerParams.name, err)
				return nil
			}

			if isAudioPublisher {
				audio, err := tester.PublishAudioTrack("audio")
				if err != nil {
					errs.Store(testerParams.name, err)
					return nil
				}
				t.lock.Lock()
				t.trackNames[audio] = fmt.Sprintf("%dA", testerParams.Sequence)
				t.lock.Unlock()
			}
			if isVideoPublisher {
				var video string
				var err error
				if params.Simulcast {
					video, err = tester.PublishSimulcastTrack("video-simulcast", params.VideoResolution, params.VideoCodec)
				} else {
					video, err = tester.PublishVideoTrack("video", params.VideoResolution, params.VideoCodec)
				}
				if err != nil {
					errs.Store(testerParams.name, err)
					return nil
				}
				t.lock.Lock()
				t.trackNames[video] = fmt.Sprintf("%dV", testerParams.Sequence)
				t.lock.Unlock()
			}
			return nil
		})
		numStarted++

		// throttle pace of join events
		for {
			secondsElapsed := float64(time.Since(startedAt)) / float64(time.Second)
			startRate := numStarted / secondsElapsed
			if err := t.Params.Context.Err(); err != nil {
				return nil, err
			}
			if startRate > params.NumPerSecond {
				time.Sleep(time.Second)
			} else {
				break
			}
		}
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	duration := params.Duration
	if duration == 0 {
		// a really long time
		duration = 1000 * time.Hour
	}
	fmt.Printf("Finished connecting to room, waiting %s\n", duration.String())

	select {
	case <-params.Context.Done():
		// canceled
	case <-time.After(duration):
		// finished
	}

	stats := make(map[string]*testerStats)
	for _, t := range testers {
		t.Stop()
		stats[t.params.name] = t.getStats()
		if e, _ := errs.Load(t.params.name); e != nil {
			stats[t.params.name].err = e.(error)
		}

	}

	return stats, nil
}
