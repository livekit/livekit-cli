package loadtester

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"go.uber.org/atomic"

	provider2 "github.com/livekit/livekit-cli/pkg/provider"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
)

type LoadTester struct {
	params TesterParams

	lock    sync.Mutex
	room    *lksdk.Room
	running atomic.Bool
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

type CurrentUser struct {
	UID string
}

type Participants struct {
	CurrentUser CurrentUser
}

type BrightToken struct {
	Token        string
	Participants Participants
}
type TesterParams struct {
	URL            string
	APIKey         string
	APISecret      string
	Room           string
	IdentityPrefix string
	Layout         Layout
	// true to subscribe to all published tracks
	Subscribe         bool
	BrightUrl         string
	BrightSharedToken string
	BrightBotStartId  int

	name           string
	Sequence       int
	expectedTracks int
}

func NewLoadTester(params TesterParams) *LoadTester {
	return &LoadTester{
		params:         params,
		stats:          &sync.Map{},
		trackQualities: make(map[string]livekit.VideoQuality),
	}
}

func (t *LoadTester) Start(botId int) error {
	if t.IsRunning() {
		return nil
	}

	identity := "RHMuUAqow2ftRQsDKNrKi9aKaFp1"
	t.room = lksdk.CreateRoom(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: t.onTrackSubscribed,
			OnTrackSubscriptionFailed: func(sid string, rp *lksdk.RemoteParticipant) {
				fmt.Printf("track subscription failed, lp:%v, sid:%v, rp:%v/%v\n", identity, sid, rp.Identity(), rp.SID())
			},
		},
	})
	var err error
	// make up to 10 reconnect attempts
	for i := 0; i < 1; i++ {
		url := strings.ReplaceAll(t.params.BrightUrl, "{{id}}", t.params.Room)
		getTokenUrl := fmt.Sprintf("%s?testUser=%d", url, botId)
		fmt.Printf("Connecting to url: %s\n", getTokenUrl)
		req, err := http.NewRequest("GET", getTokenUrl, nil)
		if err != nil {
			//panic(err)
			fmt.Println(err)
		}
		bearerToken := fmt.Sprintf("Bearer %s", t.params.BrightSharedToken)
		req.Header.Set("Authorization", bearerToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println(err)
			//panic(err)
			return err
		}

		var bt BrightToken
		err = json.NewDecoder(resp.Body).Decode(&bt)
		if err != nil {
			fmt.Println(resp)
			fmt.Println(err)
			return err
			//panic(err)
		}

		defer resp.Body.Close()

		// room, err = lksdk.ConnectToRoomWithToken(t.params.URL, bt.Token, lksdk.WithAutoSubscribe(t.params.Subscribe))
		fmt.Println(bt.Participants.CurrentUser.UID)
		err = t.room.Join(t.params.URL, lksdk.ConnectInfo{
			APIKey:              t.params.APIKey,
			APISecret:           t.params.APISecret,
			RoomName:            t.params.Room,
			ParticipantIdentity: bt.Participants.CurrentUser.UID,
		}, lksdk.WithAutoSubscribe(t.params.Subscribe))
		if err == nil {
			break
		} else {
			fmt.Println(err)
		}
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		return err
	}

	t.running.Store(true)

	return nil
}

func (t *LoadTester) IsRunning() bool {
	return t.running.Load()
}

func (t *LoadTester) PublishAudioTrack(name string, bitrate uint32) (string, error) {
	if !t.IsRunning() {
		return "", nil
	}

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

func (t *LoadTester) PublishVideoTrack(name, resolution, codec string) (string, error) {
	if !t.IsRunning() {
		return "", nil
	}

	loopers, err := provider2.CreateLoopers(resolution, codec, false)
	if err != nil {
		return "", err
	}
	track, err := lksdk.NewLocalSampleTrack(loopers[0].Codec())
	if err != nil {
		return "", err
	}
	if err := track.StartWrite(loopers[0], nil); err != nil {
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

func (t *LoadTester) PublishSimulcastTrack(name, resolution, codec string) (string, error) {
	var tracks []*lksdk.LocalSampleTrack

	loopers, err := provider2.CreateLoopers(resolution, codec, true)
	if err != nil {
		return "", err
	}
	// for video, publish three simulcast layers
	for i, looper := range loopers {
		layer := looper.ToLayer(livekit.VideoQuality(i))

		track, err := lksdk.NewLocalSampleTrack(looper.Codec(),
			lksdk.WithSimulcast("loadtest-video", layer))
		if err != nil {
			return "", err
		}
		if err := track.StartWrite(looper, nil); err != nil {
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
		kind:    pub.Kind(),
	}
	t.stats.Store(track.ID(), s)
	fmt.Println("subscribed to track", t.room.LocalParticipant.Identity(), pub.SID(), pub.Kind(), fmt.Sprintf("%d/%d", numSubscribed, numTotal))

	// consume track
	go t.consumeTrack(track, pub, rp)

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

func (t *LoadTester) consumeTrack(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	rp.WritePLI(track.SSRC())

	defer func() {
		if e := recover(); e != nil {
			fmt.Println("caught panic in consumeTrack", e)
		}
	}()

	var dpkt rtp.Depacketizer
	if pub.Kind() == lksdk.TrackKindVideo {
		dpkt = &codecs.H264Packet{}
	} else {
		dpkt = &depacketizer{}
	}
	sb := samplebuilder.New(100, dpkt, track.Codec().ClockRate, samplebuilder.WithPacketDroppedHandler(func() {
		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)
		ts.dropped.Inc()
		rp.WritePLI(track.SSRC())
	}))
	value, _ := t.stats.Load(track.ID())
	ts := value.(*trackStats)
	ts.startedAt.Store(time.Now())
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		if pkt == nil {
			continue
		}
		sb.Push(pkt)

		for _, pkt := range sb.PopPackets() {
			value, _ := t.stats.Load(track.ID())
			ts := value.(*trackStats)
			ts.bytes.Add(int64(len(pkt.Payload)))
			ts.packets.Inc()
			if pub.Kind() == lksdk.TrackKindAudio && dpkt.IsPartitionTail(pkt.Marker, pkt.Payload) {
				sentAt := int64(binary.LittleEndian.Uint64(pkt.Payload[len(pkt.Payload)-8:]))
				latency := time.Now().UnixNano() - sentAt
				// check for correct values
				if latency < 100*1000*1000 && latency > 0 {
					ts.latency.Add(latency)
					ts.latencyCount.Inc()
				}
			}
		}
	}
}
