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
	"fmt"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"go.uber.org/atomic"

	provider2 "github.com/livekit/livekit-cli/v2/pkg/provider"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
)

type LoadTester struct {
	params TesterParams

	subscribedParticipants map[string]*lksdk.RemoteParticipant
	lock                   sync.Mutex
	room                   *lksdk.Room
	running                atomic.Bool
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
	Sequence       int
	expectedTracks int
}

func NewLoadTester(params TesterParams) *LoadTester {
	return &LoadTester{
		params:                 params,
		stats:                  &sync.Map{},
		trackQualities:         make(map[string]livekit.VideoQuality),
		subscribedParticipants: make(map[string]*lksdk.RemoteParticipant),
	}
}

func (t *LoadTester) Start() error {
	if t.IsRunning() {
		return nil
	}

	identity := fmt.Sprintf("%s_%d", t.params.IdentityPrefix, t.params.Sequence)
	t.room = lksdk.NewRoom(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: t.onTrackSubscribed,
			OnTrackSubscriptionFailed: func(sid string, rp *lksdk.RemoteParticipant) {
				fmt.Printf("track subscription failed, lp:%v, sid:%v, rp:%v/%v\n", identity, sid, rp.Identity(), rp.SID())
			},
			OnTrackPublished: t.onTrackPublished,
		},
	})
	var err error
	// make up to 10 reconnect attempts
	for i := 0; i < 10; i++ {
		err = t.room.Join(t.params.URL, lksdk.ConnectInfo{
			APIKey:              t.params.APIKey,
			APISecret:           t.params.APISecret,
			RoomName:            t.params.Room,
			ParticipantIdentity: identity,
		}, lksdk.WithAutoSubscribe(false))
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		return err
	}

	t.running.Store(true)
	for _, p := range t.room.GetRemoteParticipants() {
		for _, pub := range p.TrackPublications() {
			if remotePub, ok := pub.(*lksdk.RemoteTrackPublication); ok {
				t.onTrackPublished(remotePub, p)
			}
		}
	}

	return nil
}

func (t *LoadTester) IsRunning() bool {
	return t.running.Load()
}

func (t *LoadTester) PublishAudioTrack(name string) (string, error) {
	if !t.IsRunning() {
		return "", nil
	}

	fmt.Println("publishing audio track -", t.room.LocalParticipant.Identity())
	audioLooper, err := provider2.CreateAudioLooper()
	if err != nil {
		return "", err
	}
	track, err := lksdk.NewLocalTrack(audioLooper.Codec())
	if err != nil {
		return "", err
	}
	if err := track.StartWrite(audioLooper, nil); err != nil {
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

	fmt.Println("publishing video track -", t.room.LocalParticipant.Identity())
	loopers, err := provider2.CreateVideoLoopers(resolution, codec, false)
	if err != nil {
		return "", err
	}
	track, err := lksdk.NewLocalTrack(loopers[0].Codec())
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
	var tracks []*lksdk.LocalTrack

	fmt.Println("publishing simulcast video track -", t.room.LocalParticipant.Identity())
	loopers, err := provider2.CreateVideoLoopers(resolution, codec, true)
	if err != nil {
		return "", err
	}
	// for video, publish three simulcast layers
	for i, looper := range loopers {
		layer := looper.ToLayer(livekit.VideoQuality(i))

		track, err := lksdk.NewLocalTrack(looper.Codec(),
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

func (t *LoadTester) getStats() *testerStats {
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

func (t *LoadTester) numToSubscribe() int {
	if !t.params.Subscribe {
		return 0
	}
	switch t.params.Layout {
	case LayoutSpeaker:
		return 6
	case LayoutGrid3x3:
		return 9
	case LayoutGrid4x4:
		return 16
	case LayoutGrid5x5:
		return 25
	default:
		return 1
	}
}

func (t *LoadTester) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	t.lock.Lock()
	if len(t.subscribedParticipants) >= t.numToSubscribe() && t.subscribedParticipants[rp.Identity()] == nil {
		t.lock.Unlock()
		return
	}
	t.subscribedParticipants[rp.Identity()] = rp
	t.lock.Unlock()

	publication.SetSubscribed(true)
}

func (t *LoadTester) onTrackSubscribed(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	numSubscribed := 0
	numTotal := 0
	t.lock.Lock()
	for _, p := range t.subscribedParticipants {
		tracks := p.TrackPublications()
		numTotal += len(tracks)
		for _, t := range tracks {
			if t.IsSubscribed() {
				numSubscribed++
			}
		}
	}
	t.lock.Unlock()

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
	isVideo := false
	if pub.Kind() == lksdk.TrackKindVideo {
		dpkt = &codecs.H264Packet{}
		isVideo = true
	} else {
		dpkt = &codecs.OpusPacket{}
	}
	sb := samplebuilder.New(100, dpkt, track.Codec().ClockRate, samplebuilder.WithPacketDroppedHandler(func() {
		value, _ := t.stats.Load(track.ID())
		ts := value.(*trackStats)
		ts.dropped.Inc()
		if isVideo {
			rp.WritePLI(track.SSRC())
		}
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
		}
	}
}
