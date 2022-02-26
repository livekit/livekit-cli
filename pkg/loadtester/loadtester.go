package loadtester

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	provider2 "github.com/livekit/livekit-cli/pkg/provider"
	"github.com/livekit/protocol/livekit"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/samplebuilder"

	lksdk "github.com/livekit/server-sdk-go"
)

type LoadTester struct {
	params TesterParams

	lock    sync.Mutex
	room    *lksdk.Room
	running atomic.Value
	// participant ID => quality
	trackQualities map[string]livekit.VideoQuality

	stats *sync.Map
}

type Layout string

const (
	// LayoutSpeaker - one user at 1280x720, 5 at 356x200
	LayoutSpeaker Layout = "speaker"
	// LayoutGrid3x3 - 9 participants at 400x225
	LayoutGrid3x3 Layout = "3x3"
	// LayoutGrid4x4 - 16 participants at 320x180
	LayoutGrid4x4 Layout = "4x4"
	// LayoutGrid5x5 - 25 participants at 256x144
	LayoutGrid5x5 Layout = "5x5"

	highWidth    = 1280
	highHeight   = 720
	mediumWidth  = 640
	mediumHeight = 360
	lowWidth     = 320
	lowHeight    = 180
)

func LayoutFromString(str string) Layout {
	if str == string(LayoutGrid3x3) {
		return LayoutGrid3x3
	} else if str == string(LayoutGrid4x4) {
		return LayoutGrid4x4
	} else if str == string(LayoutGrid5x5) {
		return LayoutGrid5x5
	}
	return LayoutSpeaker
}

type TesterParams struct {
	URL            string
	APIKey         string
	APISecret      string
	Room           string
	IdentityPrefix string
	Layout         Layout
	// true to subscribe to all published tracks
	Subscribe bool

	name           string
	sequence       int
	expectedTracks int
}

func NewLoadTester(params TesterParams) *LoadTester {
	return &LoadTester{
		params:         params,
		stats:          &sync.Map{},
		trackQualities: make(map[string]livekit.VideoQuality),
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
		ParticipantIdentity: fmt.Sprintf("%s_%d", t.params.IdentityPrefix, t.params.sequence),
	}, lksdk.WithAutoSubscribe(t.params.Subscribe))
	if err != nil {
		return err
	}

	t.room = room
	t.running.Store(true)
	room.Callback.OnTrackSubscribed = t.onTrackSubscribed
	room.Callback.OnTrackSubscriptionFailed = func(sid string, rp *lksdk.RemoteParticipant) {
		fmt.Printf("track subscription failed, sid:%v, rp:%v", sid, rp.Identity())
	}

	return nil
}

func (t *LoadTester) IsRunning() bool {
	if r, ok := t.running.Load().(bool); ok {
		return r
	}
	return false
}

func (t *LoadTester) PublishTrack(name string, kind lksdk.TrackKind, bitrate uint32) (string, error) {
	if !t.IsRunning() {
		return "", nil
	}

	var track *lksdk.LocalSampleTrack
	var err error
	var sampleProvider lksdk.SampleProvider
	if kind == lksdk.TrackKindAudio {
		sampleProvider, err = NewLoadTestProvider(bitrate)
		if err != nil {
			return "", err
		}
		track, err = lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   20,
			SDPFmtpLine: "",
		})
	} else if kind == lksdk.TrackKindVideo {
		var loopProvider *provider2.H264VideoLooper
		loopProvider, err = provider2.ButterflyLooperForBitrate(bitrate)
		if err != nil {
			return "", err
		}
		sampleProvider = loopProvider
		track, err = lksdk.NewLocalSampleTrack(loopProvider.Codec())
	}
	if err != nil {
		return "", err
	}
	if err := track.StartWrite(sampleProvider, nil); err != nil {
		return "", err
	}

	p, err := t.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: name,
	})
	if err != nil {
		return "", err
	}
	return p.SID(), nil
}

func (t *LoadTester) PublishSimulcastTrack(name string, bitrate uint32) (string, error) {
	var tracks []*lksdk.LocalSampleTrack

	// for video, publish three simulcast layers
	for i := livekit.VideoQuality_LOW; i <= livekit.VideoQuality_HIGH; i++ {
		// scale by 1, 2, 4
		scaleBy := uint32(math.Pow(2, 2-float64(i)))
		sampleProvider, err := provider2.ButterflyLooperForBitrate(bitrate / (scaleBy * scaleBy))
		if err != nil {
			return "", err
		}
		layer := sampleProvider.ToLayer(i)

		track, err := lksdk.NewLocalSampleTrack(sampleProvider.Codec(),
			lksdk.WithSimulcast("loadtest-video", layer))
		if err != nil {
			return "", err
		}
		if err := track.StartWrite(sampleProvider, nil); err != nil {
			return "", err
		}
		tracks = append(tracks, track)
	}

	p, err := t.room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name:   name,
		Source: livekit.TrackSource_CAMERA,
	})
	if err != nil {
		return "", err
	}

	return p.SID(), nil
}

func (t *LoadTester) GetStats() *testerStats {
	stats := &testerStats{
		expectedTracks: t.params.expectedTracks,
		trackStats:     make(map[string]*trackStats),
	}
	t.stats.Range(func(key, value interface{}) bool {
		stats.trackStats[key.(string)] = value.(*trackStats)
		return true
	})
	return stats
}

func (t *LoadTester) Reset() {
	stats := sync.Map{}
	t.stats.Range(func(key, value interface{}) bool {
		old := value.(*trackStats)
		stats.Store(key, &trackStats{
			trackID: old.trackID,
		})
		return true
	})
	t.stats = &stats
}

func (t *LoadTester) Stop() {
	if !t.IsRunning() {
		return
	}
	t.running.Store(false)
	t.room.Disconnect()
}

func (t *LoadTester) onTrackSubscribed(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
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

	s := &trackStats{
		trackID: track.ID(),
	}
	t.stats.Store(track.ID(), s)
	fmt.Println("subscribed to track", t.room.LocalParticipant.Identity(), pub.SID(), pub.Kind(), fmt.Sprintf("%d/%d", numSubscribed, numTotal))

	// consume track
	go t.consumeTrack(track, rp)

	if pub.Kind() != lksdk.TrackKindVideo {
		return
	}

	// ensure it's using the right layer
	qualityCounts := make(map[livekit.VideoQuality]int)
	t.lock.Lock()
	for _, q := range t.trackQualities {
		if count, ok := qualityCounts[q]; ok {
			qualityCounts[q] = count + 1
		} else {
			qualityCounts[q] = 1
		}
	}

	targetQuality := livekit.VideoQuality_OFF
	switch t.params.Layout {
	case LayoutSpeaker:
		if qualityCounts[livekit.VideoQuality_HIGH] == 0 {
			targetQuality = livekit.VideoQuality_HIGH
		} else if qualityCounts[livekit.VideoQuality_LOW] < 5 {
			targetQuality = livekit.VideoQuality_LOW
		}
	case LayoutGrid3x3:
		if qualityCounts[livekit.VideoQuality_MEDIUM] < 9 {
			targetQuality = livekit.VideoQuality_MEDIUM
		}
	case LayoutGrid4x4:
		if qualityCounts[livekit.VideoQuality_LOW] < 16 {
			targetQuality = livekit.VideoQuality_LOW
		}
	case LayoutGrid5x5:
		if qualityCounts[livekit.VideoQuality_LOW] < 25 {
			targetQuality = livekit.VideoQuality_LOW
		}
	}
	t.trackQualities[rp.SID()] = targetQuality
	t.lock.Unlock()

	// switch quality and/or enable/disable
	switch targetQuality {
	case livekit.VideoQuality_HIGH:
		pub.SetVideoDimensions(highWidth, highHeight)
	case livekit.VideoQuality_MEDIUM:
		pub.SetVideoDimensions(mediumWidth, mediumHeight)
	case livekit.VideoQuality_LOW:
		pub.SetVideoDimensions(lowWidth, lowHeight)
	case livekit.VideoQuality_OFF:
		pub.SetEnabled(false)
	}
}

func (t *LoadTester) consumeTrack(track *webrtc.TrackRemote, rp *lksdk.RemoteParticipant) {
	rp.WritePLI(track.SSRC())

	sb := samplebuilder.New(10, &depacketizer{}, track.Codec().ClockRate)
	value, _ := t.stats.Load(track.ID())
	ts := value.(*trackStats)
	ts.startedAt = time.Now()
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		if pkt == nil {
			continue
		}
		sb.Push(pkt)

		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)
		sample := sb.Pop()
		if sample != nil {
			atomic.AddInt64(&ts.packets, 1)
			atomic.AddInt64(&ts.bytes, int64(len(sample.Data)))
			atomic.AddInt64(&ts.dropped, int64(sample.PrevDroppedPackets))
			if sample.PrevDroppedPackets > 0 {
				rp.WritePLI(track.SSRC())
			}
			sentAt := int64(binary.LittleEndian.Uint64(sample.Data[len(sample.Data)-8:]))
			latency := time.Now().UnixNano() - sentAt
			// check for correct values
			if latency < 100*1000*1000 {
				atomic.AddInt64(&ts.latency, latency)
				atomic.AddInt64(&ts.latencyCount, 1)
			}
		}
	}
}
