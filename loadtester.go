package livekit_cli

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/pion/webrtc/v3"

	lksdk "github.com/livekit/livekit-sdk-go"
)

type LoadTesterParams struct {
	URL            string
	APIKey         string
	APISecret      string
	Room           string
	IdentityPrefix string
	Sequence       int
	AudioBitrate   uint32
	VideoBitrate   uint32
}

type LoadTester struct {
	params  LoadTesterParams
	room    *lksdk.Room
	running atomic.Value

	stats *Stats
}

func NewLoadTester(name string, expectedTracks int, params LoadTesterParams) *LoadTester {
	return &LoadTester{
		params: params,
		stats: &Stats{
			name:           name,
			expectedTracks: expectedTracks,
			trackStats:     &sync.Map{},
		},
	}
}

func (t *LoadTester) Start() error {
	if t.IsRunning() {
		return nil
	}
	room, err := lksdk.ConnectToRoom(t.params.URL, lksdk.ConnectInfo{
		APIKey:              t.params.APIKey,
		APISecret:           t.params.APISecret,
		RoomName:            t.params.Room,
		ParticipantIdentity: fmt.Sprintf("%s_%d", t.params.IdentityPrefix, t.params.Sequence),
	})
	if err != nil {
		return err
	}

	t.room = room
	t.running.Store(true)
	room.Callback.OnTrackSubscribed = t.onTrackSubscribed

	return nil
}

func (t *LoadTester) IsRunning() bool {
	if r, ok := t.running.Load().(bool); ok {
		return r
	}
	return false
}

func (t *LoadTester) Stop() {
	if !t.IsRunning() {
		return
	}
	t.running.Store(false)
	t.room.Disconnect()
}

func (t *LoadTester) PublishTrack(name string, kind lksdk.TrackKind, bitrate uint32) error {
	if !t.IsRunning() {
		return nil
	}
	sampleProvider, err := lksdk.NewLoadTestProvider(bitrate)
	if err != nil {
		return err
	}

	var codecCapability webrtc.RTPCodecCapability
	if kind == lksdk.TrackKindVideo {
		codecCapability = webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeVP8,
			ClockRate:   33,
			SDPFmtpLine: "",
			RTCPFeedback: []webrtc.RTCPFeedback{
				{Type: webrtc.TypeRTCPFBNACK},
				{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
			},
		}
	} else {
		codecCapability = webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   20,
			SDPFmtpLine: "",
		}
	}
	track, err := lksdk.NewLocalSampleTrack(codecCapability, sampleProvider)
	if err != nil {
		return err
	}

	_, err = t.room.LocalParticipant.PublishTrack(track, name)
	if err != nil {
		return err
	}

	return nil
}

func (t *LoadTester) ResetStats() {
	t.stats.Reset()
}

func (t *LoadTester) onTrackSubscribed(track *webrtc.TrackRemote, publication lksdk.TrackPublication, rp *lksdk.RemoteParticipant) {
	numSubscribed := 0
	numTotal := 0
	for _, p := range t.room.GetParticipants() {
		tracks := p.Tracks()
		numTotal += len(tracks)
		for _, t := range tracks {
			if t.IsSubscribed() {
				numSubscribed++
			}
		}
	}
	t.stats.AddTrack(track.ID())
	fmt.Println("subscribed to track", t.room.LocalParticipant.Identity(), publication.SID(), publication.Kind(), fmt.Sprintf("%d/%d", numSubscribed, numTotal))

	// consume track
	go t.consumeTrack(track)
}

func (t *LoadTester) consumeTrack(track *webrtc.TrackRemote) {
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		t.stats.Record(track.ID(), pkt)
	}
}

func GetSummary(testers []*LoadTester) (int64, int64, time.Duration, time.Duration, float64, float64) {
	var tracks, packets, latency, latencyCount, ooo, dropped, bytes int64
	var elapsed time.Duration
	for _, tester := range testers {
		res := tester.stats.GetSummary()

		tracks += res.Tracks
		packets += res.Packets
		latency += res.Latency
		latencyCount += res.LatencyCount
		ooo += res.OOO
		dropped += res.Dropped
		bytes += res.Bytes
		elapsed += res.Elapsed
	}

	var avgLatency time.Duration
	if latencyCount > 0 {
		avgLatency = time.Duration(latency / latencyCount)
	}

	var oooRate, dropRate float64
	total := float64(packets + dropped)
	if packets > 0 {
		oooRate = float64(ooo) / total * 100
		dropRate = float64(dropped) / total * 100
	} else {
		oooRate = 100
		dropRate = 100
	}

	return tracks, bytes, elapsed, avgLatency, oooRate, dropRate
}

func PrintResults(testers []*LoadTester) {
	for _, tester := range testers {
		tester.stats.PrintTrackStats()
	}

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprint(w, "\nSummary\t| Tester\t| Tracks\t| Latency\t| Total OOO\t| Total Dropped\n")

	var expected int
	var tracks, packets, latency, latencyCount, ooo, dropped, bytes int64
	var elapsed time.Duration
	for _, tester := range testers {
		res := tester.stats.GetSummary()

		tracks += res.Tracks
		expected += tester.stats.expectedTracks
		packets += res.Packets
		latency += res.Latency
		latencyCount += res.LatencyCount
		ooo += res.OOO
		dropped += res.Dropped
		bytes += res.Bytes
		elapsed += res.Elapsed

		sLatency, sOOO, sDropped := stringFormat(res.Packets, res.Latency, res.LatencyCount, res.OOO, res.Dropped)
		_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\t| %s\n",
			tester.stats.name, res.Tracks, tester.stats.expectedTracks, sLatency, sOOO, sDropped)
	}

	sLatency, sOOO, sDropped := stringFormat(packets, latency, latencyCount, ooo, dropped)
	_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\t| %s\n",
		"Total", tracks, expected, sLatency, sOOO, sDropped)
	_ = w.Flush()
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
