package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/urfave/cli/v2"

	provider2 "github.com/livekit/livekit-cli/pkg/provider"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
)

var (
	JoinCommands = []*cli.Command{
		{
			Name:     "join-room",
			Usage:    "Joins a room as a participant",
			Action:   joinRoom,
			Category: "Simulate",
			Flags: []cli.Flag{
				urlFlag,
				roomFlag,
				identityFlag,
				apiKeyFlag,
				secretFlag,
				&cli.BoolFlag{
					Name:  "publish-demo",
					Usage: "publish demo video as a loop",
				},
				&cli.StringSliceFlag{
					Name: "publish",
					Usage: "files to publish as tracks to room (supports .h264, .ivf, .ogg). " +
						"can be used multiple times to publish multiple files. " +
						"can publish from Unix socket using the format `unix:{socket-name}`; socket name must contain one of the keywords: h264, vp8, opus",
				},
				&cli.Float64Flag{
					Name:  "fps",
					Usage: "if video files are published, indicates FPS of video",
				},
			},
		},
	}
)

func joinRoom(c *cli.Context) error {
	roomCB := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataReceived: func(data []byte, rp *lksdk.RemoteParticipant) {
				logger.Infow("received data", "bytes", len(data))
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track subscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unsubscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
			},
		},
		OnRoomMetadataChanged: func(metadata string) {
			logger.Infow("room metadata changed", "metadata", metadata)
		},
	}
	room, err := lksdk.ConnectToRoom(c.String("url"), lksdk.ConnectInfo{
		APIKey:              c.String("api-key"),
		APISecret:           c.String("api-secret"),
		RoomName:            c.String("room"),
		ParticipantIdentity: c.String("identity"),
	}, roomCB)
	if err != nil {
		return err
	}
	defer room.Disconnect()

	logger.Infow("connected to room", "room", room.Name)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if c.Bool("publish-demo") {
		if err = publishDemo(room); err != nil {
			return err
		}
	}

	if c.StringSlice("publish") != nil {
		fps := c.Float64("fps")
		for _, pub := range c.StringSlice("publish") {
			if err = handlePublish(room, pub, fps); err != nil {
				return err
			}
		}
	}

	<-done
	return nil
}

func handlePublish(room *lksdk.Room, name string, fps float64) error {
	// Handle socket (either unix domain socket )
	if strings.Contains(name, "unix:") || strings.Contains(name, "tcp:") {
		return publishSocket(room, name, fps)
	}
	// Else, handle file
	return publishFile(room, name, fps)
}

func publishDemo(room *lksdk.Room) error {
	var tracks []*lksdk.LocalSampleTrack

	loopers, err := provider2.CreateLoopers("high", "", true)
	if err != nil {
		return err
	}
	for i, looper := range loopers {
		layer := looper.ToLayer(livekit.VideoQuality(i))
		track, err := lksdk.NewLocalSampleTrack(looper.Codec(),
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

func publishFile(room *lksdk.Room, filename string, fps float64) error {
	// Configure provider
	var pub *lksdk.LocalTrackPublication
	opts := []lksdk.ReaderSampleProviderOption{
		lksdk.ReaderTrackWithOnWriteComplete(func() {
			fmt.Println("finished writing file", filename)
			if pub != nil {
				_ = room.LocalParticipant.UnpublishTrack(pub.SID())
			}
		}),
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

func publishSocket(room *lksdk.Room, name string, fps float64) error {
	// Precondition that name starts with unix: or tcp:
	var addr = ""
	var sock_type = ""

	if strings.Contains(name, "unix:") {
		addr = strings.ReplaceAll(name, "unix:", "")
		sock_type = "unix"
	} else if strings.Contains(name, "tcp:") {
		addr = strings.ReplaceAll(name, "tcp:", "")
		sock_type = "tcp"
	}

	// Dial Unix socket
	sock, err := net.Dial(sock_type, addr)
	if err != nil {
		return err
	}

	// TODO (bsirang) extract mime type from name string (forcing h264 for now)
	mime := webrtc.MimeTypeH264

	// Publish to room
	err = publishReader(room, sock, mime, fps)
	return err
}

func publishReader(room *lksdk.Room, in io.ReadCloser, mime string, fps float64) error {
	// Configure provider
	var pub *lksdk.LocalTrackPublication
	opts := []lksdk.ReaderSampleProviderOption{
		lksdk.ReaderTrackWithOnWriteComplete(func() {
			fmt.Printf("finished writing %s stream\n", mime)
			if pub != nil {
				_ = room.LocalParticipant.UnpublishTrack(pub.SID())
			}
		}),
	}

	// Set frame rate if it's a video stream and FPS is set
	if strings.EqualFold(mime, webrtc.MimeTypeVP8) ||
		strings.EqualFold(mime, webrtc.MimeTypeH264) {
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
