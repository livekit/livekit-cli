package livekit_cli

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
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
	AudioBitrate   uint64
	VideoBitrate   uint64
	Sequence       int
}

type LoadTester struct {
	params  LoadTesterParams
	room    *lksdk.Room
	running atomic.Value

	stats    *Stats
	children sync.Map
}

func NewLoadTester(name string, expectedTracks int, params LoadTesterParams) *LoadTester {
	return &LoadTester{
		params: params,
		stats: &Stats{
			name:           name,
			expectedTracks: expectedTracks,
		},
		children: sync.Map{},
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

	fmt.Println("subscribed to track", t.room.LocalParticipant.Identity(), publication.SID(), publication.Kind(), fmt.Sprintf("%d/%d", numSubscribed, numTotal))
	// consume track
	go t.consumeTrack(track)
}

func (t *LoadTester) consumeTrack(track *webrtc.TrackRemote) {
	stats := &Stats{
		Tracks:  1,
		trackID: track.ID(),
		missing: make(map[int64]bool),
	}
	t.children.Store(track.ID(), stats)

	var max, resets int64
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}

		payload := pkt.Payload
		c := int64(payload[len(payload)-1])
		sentAt := int64(binary.LittleEndian.Uint64(payload[:8]))
		stats.Latency += time.Now().UnixNano() - sentAt

		// stats will break if dropping massive amounts
		if max%256 > 200 && c < 100 {
			resets++
		}
		num := c + resets*256
		if num > max+200 {
			// handle OOO packets just after reset
			num -= 256
		}
		if num != max+1 && stats.Packets > 0 {
			if stats.missing[num] {
				delete(stats.missing, num)
				stats.OOO++
			}
			stats.missing[max+1] = true
		}
		stats.Packets++
		if num > max {
			max = num
		}
	}
}

func (t *LoadTester) collectStats() *Stats {
	t.children.Range(func(key, value interface{}) bool {
		s := value.(*Stats)
		s.Dropped = int64(len(s.missing))
		t.stats.AddChild(s)
		return true
	})
	return t.stats
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
