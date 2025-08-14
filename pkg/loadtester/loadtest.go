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

package loadtester

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/syncmap"
	"golang.org/x/time/rate"
)

type LoadTest struct {
	Params     Params
	trackNames map[string]string
	lock       sync.Mutex
}

type Params struct {
	VideoPublishers int
	AudioPublishers int
	Subscribers     int
	VideoResolution string
	VideoCodec      string
	Duration        time.Duration
	// number of seconds to spin up per second
	NumPerSecond     float64
	Simulcast        bool
	SimulateSpeakers bool

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

func (t *LoadTest) Run(ctx context.Context) error {
	parsedUrl, err := url.Parse(t.Params.URL)
	if err != nil {
		return err
	}
	if strings.HasSuffix(parsedUrl.Hostname(), ".livekit.cloud") {
		if t.Params.VideoPublishers > 50 || t.Params.Subscribers > 50 || t.Params.AudioPublishers > 50 {
			return errors.New("Unable to perform load test on LiveKit Cloud. Load testing is prohibited by our acceptable use policy: https://livekit.io/legal/acceptable-use-policy")
		}
	}

	stats, err := t.run(ctx, t.Params)
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

	testerTable := util.CreateTable().
		Headers("Tester", "Track", "Kind", "Pkts.", "Bitrate", "Pkt. Loss")

	for n, name := range names {
		testerStats := stats[name]
		summaries[name] = getTesterSummary(testerStats)
		trackStatsSlice := make([]*trackStats, 0, len(testerStats.trackStats))
		for _, ts := range testerStats.trackStats {
			trackStatsSlice = append(trackStatsSlice, ts)
		}
		sort.Slice(trackStatsSlice, func(i, j int) bool {
			return strings.Compare(
				string(trackStatsSlice[i].kind),
				string(trackStatsSlice[j].kind),
			) < 0
		})
		for i, trackStats := range trackStatsSlice {
			dropped := formatLossRate(
				trackStats.packets.Load(), trackStats.dropped.Load())

			trackName := ""
			if i == 0 {
				trackName = name
			}
			testerTable.Row(
				trackName,
				trackStats.trackID,
				string(trackStats.kind),
				strconv.FormatInt(trackStats.packets.Load(), 10),
				formatBitrate(
					trackStats.bytes.Load(),
					time.Since(trackStats.startedAt.Load()),
				),
				dropped,
			)
		}
		if n != len(names)-1 {
			testerTable.Row("", "", "", "", "", "")
		}

	}

	if len(names) > 0 {
		fmt.Println("\nTrack loading:")
		fmt.Println(testerTable)
	}

	if len(summaries) == 0 {
		return nil
	}

	// tester summary
	summaryTable := util.CreateTable().
		Headers("Tester", "Tracks", "Bitrate", "Total Pkt. Loss", "Error").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return util.FormHeaderStyle
			}
			if row == len(names) {
				return util.FormBaseStyle.Bold(true).Reverse(true)
			}
			return util.FormBaseStyle
		})
	for _, name := range names {
		s := summaries[name]
		sDropped := formatLossRate(s.packets, s.dropped)
		sBitrate := formatBitrate(s.bytes, s.elapsed)
		summaryTable.Row(name, fmt.Sprintf("%d/%d", s.tracks, s.expected), sBitrate, sDropped, s.errString)
	}
	{
		// totals row
		s := getTestSummary(summaries)
		sDropped := formatLossRate(s.packets, s.dropped)
		// avg bitrate per sub
		sBitrate := fmt.Sprintf("%s (%s avg)",
			formatBitrate(s.bytes, s.elapsed),
			formatBitrate(s.bytes/int64(len(summaries)), s.elapsed),
		)
		summaryTable.Row("Total", fmt.Sprintf("%d/%d", s.tracks, s.expected), sBitrate, sDropped, strconv.FormatInt(s.errCount, 10))
	}
	fmt.Println("\nSubscriber summaries:")
	fmt.Println(summaryTable)

	return nil
}

func (t *LoadTest) RunSuite(ctx context.Context) error {
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

	table := util.CreateTable().
		Headers("Pubs", "Subs", "Tracks", "Audio", "Video", "Pkt. Loss", "Errors")
	showTrackStats := false

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

		stats, err := t.run(ctx, caseParams)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
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
		if tracks > 0 {
			showTrackStats = true
			table.Row(
				strconv.Itoa(c.publishers),
				strconv.Itoa(c.subscribers),
				strconv.FormatInt(tracks, 10),
				"Yes",
				videoString,
				formatLossRate(packets, dropped),
				strconv.FormatInt(errCount, 10),
			)
		}
	}

	if showTrackStats {
		fmt.Println("\nSuite results:")
		fmt.Println(table)
	}
	return nil
}

func (t *LoadTest) run(ctx context.Context, params Params) (map[string]*testerStats, error) {
	if params.Room == "" {
		params.Room = fmt.Sprintf("testroom%d", rand.Int31n(1000))
	}
	if params.IdentityPrefix == "" {
		params.IdentityPrefix = randStringRunes(5)
	}

	expectedTracks := params.VideoPublishers + params.AudioPublishers

	var participantStrings []string
	if params.VideoPublishers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d video publishers", params.VideoPublishers))
	}
	if params.AudioPublishers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d audio publishers", params.AudioPublishers))
	}
	if params.Subscribers > 0 {
		participantStrings = append(participantStrings, fmt.Sprintf("%d subscribers", params.Subscribers))
	}
	fmt.Printf("Starting load test with %s, room: %s\n",
		strings.Join(participantStrings, ", "), params.Room)

	var publishers, testers []*LoadTester
	group, _ := errgroup.WithContext(ctx)
	errs := syncmap.Map{}
	maxPublishers := params.VideoPublishers
	if params.AudioPublishers > maxPublishers {
		maxPublishers = params.AudioPublishers
	}

	// throttle pace of join events
	limiter := rate.NewLimiter(rate.Limit(params.NumPerSecond), 1)
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
		if isVideoPublisher || isAudioPublisher {
			publishers = append(publishers, tester)
		}

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

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	var speakerSim *SpeakerSimulator
	if len(publishers) > 0 && t.Params.SimulateSpeakers {
		speakerSim = NewSpeakerSimulator(SpeakerSimulatorParams{
			Testers: publishers,
		})
		speakerSim.Start()
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
	case <-ctx.Done():
		// canceled
	case <-time.After(duration):
		// finished
	}

	if speakerSim != nil {
		speakerSim.Stop()
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
