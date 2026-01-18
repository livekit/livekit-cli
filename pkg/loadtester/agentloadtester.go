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
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/utils"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
)

type LoadTestRoomStats struct {
	agentDispatchedAt    time.Time
	agentJoinedAt        time.Time
	agentJoined          bool
	agentTrackSubscribed bool
	echoTrackPublished   bool
	meetLink             string
}

type LoadTestRoom struct {
	params           AgentLoadTestParams
	room             *lksdk.Room
	firstParticipant *lksdk.RemoteParticipant
	echoTrack        *lksdk.LocalTrack
	running          atomic.Bool
	stats            LoadTestRoomStats
}

type AgentLoadTester struct {
	params    AgentLoadTestParams
	testRooms map[string]*LoadTestRoom
	lock      sync.Mutex
}

func NewAgentLoadTester(params AgentLoadTestParams) *AgentLoadTester {
	return &AgentLoadTester{
		params:    params,
		testRooms: make(map[string]*LoadTestRoom),
	}
}

func NewLoadTestRoom(params AgentLoadTestParams) *LoadTestRoom {
	return &LoadTestRoom{
		params: params,
		stats:  LoadTestRoomStats{},
	}
}

func (t *AgentLoadTester) Start(ctx context.Context) error {
	group, groupCtx := errgroup.WithContext(ctx)

	for i := 0; i < t.params.Rooms; i++ {
		roomName := utils.NewGuid(fmt.Sprintf("room-%d-", i))

		loadTestRoom := &LoadTestRoom{
			params: t.params,
		}

		t.lock.Lock()
		t.testRooms[roomName] = loadTestRoom
		t.lock.Unlock()

		group.Go(func() error {
			if err := loadTestRoom.start(roomName); err != nil {
				log.Printf("Failed to connect to room %s: %v", roomName, err)
				return err
			}

			_, err := loadTestRoom.publishEchoTrack()
			if err != nil {
				log.Printf("Failed to publish echo track to room %s: %v", roomName, err)
				loadTestRoom.stop()
				return err
			}
			loadTestRoom.stats.echoTrackPublished = true

			if t.params.AgentName != "" {
				err = loadTestRoom.dispatchAgent()
				if err != nil {
					log.Printf("Failed to dispatch agent to room %s: %v", roomName, err)
					loadTestRoom.stop()
					return err
				}
			}

			<-groupCtx.Done()
			log.Printf("Context cancelled for room %s, cleaning up", roomName)
			loadTestRoom.stop()
			return nil
		})
		for !loadTestRoom.stats.agentJoined {
			select {
			case <-groupCtx.Done():
				return nil
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	log.Printf("Agent load tester started successfully, waiting for duration: %s", t.params.Duration.String())

	if err := group.Wait(); err != nil {
		return err
	}

	return nil
}

func (t *AgentLoadTester) Stop() {
	t.printStats()
	t.lock.Lock()
	defer t.lock.Unlock()

	for roomName, room := range t.testRooms {
		room.stop()
		delete(t.testRooms, roomName)
	}
}

func (r *LoadTestRoom) start(roomName string) error {
	if r.isRunning() {
		return nil
	}

	identity := "echo-participant"

	r.room = lksdk.NewRoom(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: r.onTrackSubscribed,
			OnTrackSubscriptionFailed: func(sid string, rp *lksdk.RemoteParticipant) {
				log.Printf("Track subscription failed, lp:%v, sid:%v, rp:%v/%v", identity, sid, rp.Identity(), rp.SID())
			},
		},
		OnParticipantConnected: func(rp *lksdk.RemoteParticipant) {
			if rp.Kind() == lksdk.ParticipantAgent {
				r.stats.agentJoined = true
				r.stats.agentJoinedAt = time.Now()
			}
		},
		OnParticipantDisconnected: r.onParticipantDisconnected,
	})

	var err error
	// make up to 10 reconnect attempts
	for i := 0; i < 10; i++ {
		err = r.room.Join(r.params.URL, lksdk.ConnectInfo{
			APIKey:              r.params.APIKey,
			APISecret:           r.params.APISecret,
			RoomName:            roomName,
			ParticipantIdentity: identity,
			ParticipantAttributes: r.params.ParticipantAttributes,
		})
		if err == nil {
			break
		}
		log.Printf("Failed to join room %s (attempt %d): %v", roomName, i+1, err)
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		return err
	}

	meetParticipantToken, _ := newAccessToken(r.params.APIKey, r.params.APISecret, roomName, "meet-participant")
	r.stats.meetLink = fmt.Sprintf("https://meet.livekit.io/custom?liveKitUrl=%s&token=%s", r.params.URL, meetParticipantToken)
	logger.Debugw("Inspect the room in LiveKit Meet using this url", "room", roomName, "url", r.stats.meetLink)
	r.running.Store(true)
	return nil
}

func (r *LoadTestRoom) publishEchoTrack() (string, error) {
	if !r.isRunning() {
		return "", nil
	}

	echoTrack, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus})
	if err != nil {
		return "", err
	}
	r.echoTrack = echoTrack

	p, err := r.room.LocalParticipant.PublishTrack(echoTrack, &lksdk.TrackPublicationOptions{
		Name: "echo-track",
	})
	if err != nil {
		return "", err
	}
	return p.SID(), nil
}

func (r *LoadTestRoom) dispatchAgent() error {
	dispatchClient := lksdk.NewAgentDispatchServiceClient(r.params.URL, r.params.APIKey, r.params.APISecret)
	req := &livekit.CreateAgentDispatchRequest{
		Room:      r.room.Name(),
		AgentName: r.params.AgentName,
	}

	_, err := dispatchClient.CreateDispatch(context.Background(), req)
	if err != nil {
		return err
	}
	r.stats.agentDispatchedAt = time.Now()
	// log.Printf("Successfully dispatched agent %s to room %s", r.params.AgentName, r.room.Name())
	return nil
}

func (r *LoadTestRoom) onTrackSubscribed(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if r.echoTrack != nil && r.firstParticipant == nil && track.Kind() == webrtc.RTPCodecTypeAudio {
		r.firstParticipant = rp
		if rp.Kind() == lksdk.ParticipantAgent {
			r.stats.agentTrackSubscribed = true
		}

		type timestampedSample struct {
			sample   media.Sample
			received time.Time
		}
		sampleChan := make(chan timestampedSample, 1000)

		go func() {
			for r.running.Load() {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					// log.Printf("Error reading RTP packet: %v", err)
					continue
				}
				sample := timestampedSample{
					sample: media.Sample{
						Data:     pkt.Payload,
						Duration: 20 * time.Millisecond,
					},
					received: time.Now(),
				}
				sampleChan <- sample
			}
			close(sampleChan)
		}()

		go func() {
			for r.running.Load() {
				ts, ok := <-sampleChan
				if !ok {
					return
				}
				// delay the sample by the echo speech delay
				delay := time.Until(ts.received.Add(r.params.EchoSpeechDelay))
				if delay > 0 {
					time.Sleep(delay)
				}
				r.echoTrack.WriteSample(ts.sample, &lksdk.SampleWriteOptions{})
			}
		}()
	}
}

func (r *LoadTestRoom) onParticipantDisconnected(rp *lksdk.RemoteParticipant) {
	log.Printf("Participant disconnected, rp:%v/%v", rp.Identity(), rp.SID())
	if rp.Identity() == r.firstParticipant.Identity() {
		r.stop()
	}
}

func (r *LoadTestRoom) stop() {
	if !r.isRunning() {
		return
	}
	r.running.Store(false)
	r.room.Disconnect()
}

func (t *LoadTestRoom) isRunning() bool {
	return t.running.Load()
}

func (t *AgentLoadTester) printStats() {
	t.lock.Lock()
	defer t.lock.Unlock()

	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // Green
	crossStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // Red

	table := util.CreateTable().
		Headers("#", "Room", "Agent Dispatched At", "Agent Joined", "Agent Join Delay", "Agent Track Subscribed", "Echo Track Published")

	rooms := make([]*LoadTestRoom, 0, len(t.testRooms))
	for _, room := range t.testRooms {
		rooms = append(rooms, room)
	}
	sort.Slice(rooms, func(i, j int) bool {
		return rooms[i].stats.agentDispatchedAt.Before(rooms[j].stats.agentDispatchedAt)
	})

	index := 1
	for _, room := range rooms {
		boolToSymbol := func(b bool) string {
			if b {
				return checkStyle.Render("✓")
			}
			return crossStyle.Render("✗")
		}
		agentJoinDelay := "-"
		if !room.stats.agentJoinedAt.IsZero() && !room.stats.agentDispatchedAt.IsZero() {
			agentJoinDelay = room.stats.agentJoinedAt.Sub(room.stats.agentDispatchedAt).String()
		}

		table.Row(
			strconv.Itoa(index),
			room.room.Name(),
			room.stats.agentDispatchedAt.Format(time.RFC3339),
			boolToSymbol(room.stats.agentJoined),
			agentJoinDelay,
			boolToSymbol(room.stats.agentTrackSubscribed),
			boolToSymbol(room.stats.echoTrackPublished),
		)
		index++
	}

	fmt.Println("\nTest Statistics:")
	fmt.Println(table)
}

func newAccessToken(apiKey, apiSecret, roomName, pID string) (string, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)
	grant := &auth.VideoGrant{
		RoomJoin: true,
		Room:     roomName,
	}
	at.SetVideoGrant(grant).
		SetIdentity(pID).
		SetName(pID)

	return at.ToJWT()
}
