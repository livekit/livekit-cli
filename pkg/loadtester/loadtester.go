package loadtester

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/webrtc/v3"

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

type Layout int

const (
	// LayoutSpeaker - one user at 1280x720, 5 at 356x200
	LayoutSpeaker Layout = 0
	// LayoutGrid3x3 - 9 participants at 400x225
	LayoutGrid3x3 Layout = 1
	// LayoutGrid4x4 - 16 participants at 320x180
	LayoutGrid4x4 Layout = 2
	// LayoutGrid5x5 - 25 participants at 256x144
	LayoutGrid5x5 Layout = 3

	highWidth    = 1280
	highHeight   = 720
	mediumWidth  = 640
	mediumHeight = 360
	lowWidth     = 320
	lowHeight    = 180
)

type TesterParams struct {
	URL            string
	APIKey         string
	APISecret      string
	Room           string
	IdentityPrefix string
	Layout         Layout

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
	})
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

	if kind == lksdk.TrackKindAudio {
		sampleProvider, err := NewLoadTestProvider(bitrate)
		if err != nil {
			return "", err
		}
		track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   20,
			SDPFmtpLine: "",
		})
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

	// video
	var tracks []*lksdk.LocalSampleTrack

	// assume 3 layers simulcast
	for q := livekit.VideoQuality_HIGH; q >= livekit.VideoQuality_LOW; q-- {
		sampleProvider, err := NewLoadTestProvider(bitrate / uint32(math.Pow(2, float64(q))))
		if err != nil {
			return "", err
		}
		layer := &livekit.VideoLayer{
			Quality: q,
		}
		switch q {
		case livekit.VideoQuality_HIGH:
			layer.Width = highWidth
			layer.Height = highHeight
		case livekit.VideoQuality_MEDIUM:
			layer.Width = mediumWidth
			layer.Height = mediumHeight
		case livekit.VideoQuality_LOW:
			layer.Width = lowWidth
			layer.Height = lowHeight
		}
		track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeVP8,
			ClockRate:   33,
			SDPFmtpLine: "",
			RTCPFeedback: []webrtc.RTCPFeedback{
				{Type: webrtc.TypeRTCPFBNACK},
				{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
			},
		}, lksdk.WithSimulcast(name, layer))
		if err != nil {
			return "", err
		}
		if err := track.StartWrite(sampleProvider, nil); err != nil {
			return "", err
		}

		tracks = append(tracks, track)
	}

	p, err := t.room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name: name,
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
		fmt.Println("setting video resolution to low")
		pub.SetVideoDimensions(lowWidth, lowHeight)
	case livekit.VideoQuality_OFF:
		pub.SetEnabled(false)
	}
}

func (t *LoadTester) consumeTrack(track *webrtc.TrackRemote, rp *lksdk.RemoteParticipant) {
	rp.WritePLI(track.SSRC())

	sb := samplebuilder.New(10, &depacketizer{}, track.Codec().ClockRate, samplebuilder.WithPacketDroppedHandler(func() {
		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)
		atomic.AddInt64(&ts.dropped, 1)
		rp.WritePLI(track.SSRC())
	}))
	dpkt := depacketizer{}
	value, _ := t.stats.Load(track.ID())
	ts := value.(*trackStats)
	ts.startedAt = time.Now()
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}

		sb.Push(pkt)

		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)
		for _, p := range sb.PopPackets() {
			atomic.AddInt64(&ts.packets, 1)
			atomic.AddInt64(&ts.bytes, int64(len(p.Payload)))
			if dpkt.IsPartitionTail(false, p.Payload) {
				sentAt := int64(binary.LittleEndian.Uint64(p.Payload[len(p.Payload)-8:]))
				latency := time.Now().UnixNano() - sentAt
				atomic.AddInt64(&ts.latency, latency)
				atomic.AddInt64(&ts.latencyCount, 1)
			}
		}
	}
}
