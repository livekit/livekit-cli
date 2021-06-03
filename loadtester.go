package livekit_cli

import (
	"fmt"
	"sync/atomic"

	lksdk "github.com/livekit/livekit-sdk-go"
	"github.com/pion/webrtc/v3"
)

type LoadTesterParams struct {
	URL          string
	APIKey       string
	APISecret    string
	Room         string
	AudioBitrate uint64
	VideoBitrate uint64
	Sequence     int
}

type LoadTester struct {
	params  LoadTesterParams
	room    *lksdk.Room
	running atomic.Value
}

func NewLoadTester(params LoadTesterParams) *LoadTester {
	return &LoadTester{
		params: params,
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
		ParticipantIdentity: fmt.Sprintf("tester_%d", t.params.Sequence),
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
	sampleProvider := lksdk.NewNullSampleProvider(bitrate)
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
	for _, p := range t.room.Participants {
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
	for {
		_, _, err := track.ReadRTP()
		if err != nil {
			return
		}
	}
}
