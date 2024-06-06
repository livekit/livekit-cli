// Copyright 2023 LiveKit, Inc.
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

package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	"github.com/urfave/cli/v2"
	"go.uber.org/atomic"

	provider2 "github.com/livekit/livekit-cli/pkg/provider"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
)

var (
	JoinCommands = []*cli.Command{
		{
			Name:     "join-room",
			Usage:    "Joins a room as a participant",
			Action:   joinRoom,
			Category: "Simulate",
			Flags: withDefaultFlags(
				roomFlag,
				identityFlag,
				&cli.BoolFlag{
					Name:  "publish-demo",
					Usage: "publish demo video as a loop",
				},
				&cli.StringSliceFlag{
					Name: "publish",
					Usage: "files to publish as tracks to room (supports .h264, .ivf, .ogg). " +
						"can be used multiple times to publish multiple files. " +
						"can publish from Unix or TCP socket using the format `codec://socket_name` or `codec://host:address` respectively. Valid codecs are h264, vp8, opus",
				},
				&cli.StringFlag{
					Name: "subscribe-audio",
					Usage: "subscribe to audio tracks and write to a local TCP socket." +
						"pass the directory to place the socket file. For example --subscribe-audio /tmp/my-audio",
				},
				&cli.Float64Flag{
					Name:  "fps",
					Usage: "if video files are published, indicates FPS of video",
				},
				&cli.BoolFlag{
					Name:  "exit-after-publish",
					Usage: "when publishing, exit after file or stream is complete",
				},
			),
		},
	}
)

const mimeDelimiter = "://"

func joinRoom(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	audioPath := c.String("subscribe-audio")
	done := make(chan os.Signal, 1)
	roomCB := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataPacket: func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
				logger.Infow("received data", "data", data.ToProto(), "participant", params.SenderIdentity)
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track subscribed",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
				if pub.Kind() == lksdk.TrackKindAudio && audioPath != "" {
					go writeTrackToSocket(audioPath, participant.Identity(), pub.SID(), track)
				}
			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unsubscribed",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackUnpublished: func(pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unpublished",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackMuted: func(pub lksdk.TrackPublication, participant lksdk.Participant) {
				logger.Infow("track muted",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackUnmuted: func(pub lksdk.TrackPublication, participant lksdk.Participant) {
				logger.Infow("track unmuted",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
		},
		OnRoomMetadataChanged: func(metadata string) {
			logger.Infow("room metadata changed", "metadata", metadata)
		},
		OnReconnecting: func() {
			logger.Infow("reconnecting to room")
		},
		OnReconnected: func() {
			logger.Infow("reconnected to room")
		},
		OnDisconnected: func() {
			logger.Infow("disconnected from room")
			close(done)
		},
	}
	room, err := lksdk.ConnectToRoom(pc.URL, lksdk.ConnectInfo{
		APIKey:              pc.APIKey,
		APISecret:           pc.APISecret,
		RoomName:            c.String("room"),
		ParticipantIdentity: c.String("identity"),
	}, roomCB)
	if err != nil {
		return err
	}
	defer room.Disconnect()

	logger.Infow("connected to room", "room", room.Name())

	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if c.Bool("publish-demo") {
		if err = publishDemo(room); err != nil {
			return err
		}
	}

	if c.StringSlice("publish") != nil {
		fps := c.Float64("fps")
		for _, pub := range c.StringSlice("publish") {
			onPublishComplete := func(pub *lksdk.LocalTrackPublication) {
				if c.Bool("exit-after-publish") {
					close(done)
					return
				}
				if pub != nil {
					fmt.Printf("finished writing %s\n", pub.Name())
					_ = room.LocalParticipant.UnpublishTrack(pub.SID())
				}
			}
			if err = handlePublish(room, pub, fps, onPublishComplete); err != nil {
				return err
			}
		}
	}

	<-done
	return nil
}

func handlePublish(room *lksdk.Room,
	name string,
	fps float64,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	if isSocketFormat(name) {
		mimeType, socketType, address, err := parseSocketFromName(name)
		if err != nil {
			return err
		}
		return publishSocket(room, mimeType, socketType, address, fps, onPublishComplete)
	}
	return publishFile(room, name, fps, onPublishComplete)
}

func publishDemo(room *lksdk.Room) error {
	var tracks []*lksdk.LocalTrack

	loopers, err := provider2.CreateVideoLoopers("high", "", true)
	if err != nil {
		return err
	}
	for i, looper := range loopers {
		layer := looper.ToLayer(livekit.VideoQuality(i))
		track, err := lksdk.NewLocalTrack(looper.Codec(),
			lksdk.WithSimulcast("demo-video", layer),
		)
		if err != nil {
			return err
		}
		if err = track.StartWrite(looper, nil); err != nil {
			return err
		}
		tracks = append(tracks, track)
	}
	_, err = room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name: "demo",
	})
	return err
}

func publishFile(room *lksdk.Room,
	filename string,
	fps float64,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	// Configure provider
	opts := []lksdk.ReaderSampleProviderOption{
		lksdk.ReaderTrackWithRTCPHandler(func(packet rtcp.Packet) {
			switch packet.(type) {
			case *rtcp.PictureLossIndication:
				logger.Infow("received PLI", "filename", filename)
			}
		}),
	}
	var pub *lksdk.LocalTrackPublication
	if onPublishComplete != nil {
		opts = append(opts, lksdk.ReaderTrackWithOnWriteComplete(func() {
			onPublishComplete(pub)
		}))
	}

	// Set frame rate if it's a video stream and FPS is set
	ext := filepath.Ext(filename)
	if ext == ".h264" || ext == ".ivf" {
		if fps != 0 {
			frameDuration := time.Second / time.Duration(fps)
			opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
		}
	}

	// Create track and publish
	track, err := lksdk.NewLocalFileTrack(filename, opts...)
	if err != nil {
		return err
	}
	pub, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: filename,
	})
	return err
}

func parseSocketFromName(name string) (string, string, string, error) {
	// Extract mime type, socket type, and address
	// e.g. h264://192.168.0.1:1234 (tcp)
	// e.g. opus:///tmp/my.socket (unix domain socket)

	offset := strings.Index(name, mimeDelimiter)
	if offset == -1 {
		return "", "", "", fmt.Errorf("did not find delimiter %s in %s", mimeDelimiter, name)
	}

	mimeType := name[:offset]

	if mimeType != "h264" && mimeType != "vp8" && mimeType != "opus" {
		return "", "", "", fmt.Errorf("unsupported mime type: %s", mimeType)
	}

	address := name[offset+len(mimeDelimiter):]

	if len(address) == 0 {
		return "", "", "", fmt.Errorf("address cannot be empty. input was: %s", name)
	}

	// If the address doesn't contain a ':' we assume it's a unix socket
	if !strings.Contains(address, ":") {
		return mimeType, "unix", address, nil
	}

	return mimeType, "tcp", address, nil
}

func isSocketFormat(name string) bool {
	return strings.Contains(name, mimeDelimiter)
}

func publishSocket(room *lksdk.Room,
	mimeType string,
	socketType string,
	address string,
	fps float64,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	var mime string
	switch {
	case strings.Contains(mimeType, "h264"):
		mime = webrtc.MimeTypeH264
	case strings.Contains(mimeType, "vp8"):
		mime = webrtc.MimeTypeVP8
	case strings.Contains(mimeType, "opus"):
		mime = webrtc.MimeTypeOpus
	default:
		return lksdk.ErrUnsupportedFileType
	}

	// Dial socket
	sock, err := net.Dial(socketType, address)
	if err != nil {
		return err
	}

	// Publish to room
	err = publishReader(room, sock, mime, fps, onPublishComplete)
	return err
}

func publishReader(room *lksdk.Room,
	in io.ReadCloser,
	mime string,
	fps float64,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	// Configure provider
	var opts []lksdk.ReaderSampleProviderOption
	var pub *lksdk.LocalTrackPublication
	if onPublishComplete != nil {
		opts = append(opts, lksdk.ReaderTrackWithOnWriteComplete(func() {
			onPublishComplete(pub)
		}))
	}

	// Set frame rate if it's a video stream and FPS is set
	if strings.HasPrefix(strings.ToLower(mime), "video") {
		if fps != 0 {
			frameDuration := time.Second / time.Duration(fps)
			opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
		}
	}

	// Create track and publish
	track, err := lksdk.NewLocalReaderTrack(in, mime, opts...)
	if err != nil {
		return err
	}
	pub, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{})
	if err != nil {
		return err
	}
	return nil
}

func writeTrackToSocket(destPath, identity, trackID string, track *webrtc.TrackRemote) {
	if err := os.MkdirAll(destPath, 0755); err != nil {
		log.Fatalf("Error creating directory: %v", err)
	}
	socketFile := fmt.Sprintf("%s/%s_%s.sock", destPath, identity, trackID)

	if _, err := os.Stat(socketFile); err == nil {
		os.Remove(socketFile)
	}

	listener, err := net.Listen("unix", socketFile)
	if err != nil {
		log.Fatalf("could not listen to unix socket: %v", err)
	}
	defer listener.Close()

	connPtr := atomic.NewPointer[net.Conn](nil)
	writerPtr := atomic.NewPointer[oggwriter.OggWriter](nil)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("could not accept connection: %v", err)
		}
		connPtr.Store(&conn)
		writer, err := oggwriter.NewWith(conn, 48000, track.Codec().Channels)
		if err != nil {
			log.Fatalf("could not create Ogg writer: %v", err)
		}
		writerPtr.Store(writer)
	}()
	defer func() {
		if conn := connPtr.Load(); conn != nil {
			(*conn).Close()
		}
	}()

	// start reading from the track, but only write if TCP socket is being read
	sb := samplebuilder.New(200, &codecs.OpusPacket{}, 48000)
	var writer *oggwriter.OggWriter
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			break
		}
		if writer == nil {
			writer = writerPtr.Load()
			if writer != nil {
				fmt.Printf("socket %v connected, writing track %v", socketFile, trackID)
			}
		}

		sb.Push(packet)
		for _, p := range sb.PopPackets() {
			if writer != nil {
				writer.WriteRTP(p)
			}
		}
	}
}
