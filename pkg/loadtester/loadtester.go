package loadtester

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v3"

	lksdk "github.com/livekit/server-sdk-go"
)

type LoadTester struct {
	params TesterParams

	room    *lksdk.Room
	running atomic.Value

	stats *sync.Map
}

type TesterParams struct {
	URL            string
	APIKey         string
	APISecret      string
	Room           string
	IdentityPrefix string

	name           string
	sequence       int
	expectedTracks int
}

func NewLoadTester(params TesterParams) *LoadTester {
	return &LoadTester{
		params: params,
		stats:  &sync.Map{},
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
	sampleProvider, err := lksdk.NewLoadTestProvider(bitrate)
	if err != nil {
		return "", err
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
	track, err := lksdk.NewLocalSampleTrack(codecCapability)
	if err != nil {
		return "", err
	}
	if err := track.StartWrite(sampleProvider, nil); err != nil {
		return "", err
	}

	p, err := t.room.LocalParticipant.PublishTrack(track, name)
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
			missing: make(map[int64]bool),
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

func (t *LoadTester) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
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
		missing: make(map[int64]bool),
	}
	t.stats.Store(track.ID(), s)
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

		payload := pkt.Payload

		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)

		sentAt := int64(binary.LittleEndian.Uint64(payload[len(payload)-8:]))
		latency := time.Now().UnixNano() - sentAt
		if latency > 0 && latency < 1000000000 {
			ts.latency += time.Now().UnixNano() - sentAt
			ts.latencyCount++
		}

		if ts.max%65535 > 48000 && pkt.SequenceNumber < 16000 {
			ts.resets++
		}

		expected := ts.max + 1
		sequence := int64(pkt.SequenceNumber) + ts.resets*65536
		// correct for when sequence just reset and then a high sequence number came late
		if sequence > expected+32000 {
			sequence -= 65536
		}

		if ts.packets == 0 {
			ts.startedAt = time.Now()
		} else if sequence != expected {
			if ts.missing[sequence] {
				delete(ts.missing, sequence)
				ts.ooo++
			} else {
				for i := expected; i <= sequence; i++ {
					ts.missing[i] = true
				}
			}
		}
		ts.packets++
		ts.bytes += int64(len(payload))
		if sequence > ts.max {
			ts.max = sequence
		}
	}
}
