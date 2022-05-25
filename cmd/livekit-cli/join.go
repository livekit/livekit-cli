package main

import (
	"fmt"
	"io"
	"math"
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
			Usage:    "joins a room as a participant",
			Action:   joinRoom,
			Category: "Participant",
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
					Name:  "publish-file",
					Usage: "files to publish as tracks to room (supports .h264, .ivf, .ogg). can be used multiple times to publish multiple files",
				},
				&cli.StringFlag{
					Name:  "publish-stream",
					Usage: "can only be one of the following (must be lower-case): video/h264, video/vp8, audio/opus",
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
	room, err := lksdk.ConnectToRoom(c.String("url"), lksdk.ConnectInfo{
		APIKey:              c.String("api-key"),
		APISecret:           c.String("api-secret"),
		RoomName:            c.String("room"),
		ParticipantIdentity: c.String("identity"),
	})
	if err != nil {
		return err
	}

	logger.Infow("connected to room", "room", room.Name)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	room.Callback.OnDataReceived = func(data []byte, rp *lksdk.RemoteParticipant) {
		logger.Infow("received data", "bytes", len(data))
	}
	room.Callback.OnConnectionQualityChanged = func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
		logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
	}
	room.Callback.OnRoomMetadataChanged = func(metadata string) {
		logger.Infow("room metadata changed", "metadata", metadata)
	}
	room.Callback.OnTrackSubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		logger.Infow("track subscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
	}
	room.Callback.OnTrackUnsubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		logger.Infow("track unsubscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
	}

	if c.Bool("publish-demo") {
		if err = publishDemo(room); err != nil {
			return err
		}
	}
	if c.StringSlice("publish-file") != nil {
		files := c.StringSlice("publish-file")
		fps := c.Float64("fps")
		if err = publishFiles(room, files, fps); err != nil {
			return err
		}
	}
	if c.String("publish-stream") != "" {
		codec := c.String("publish-stream")
		fps := c.Float64("fps")
		// Extract data stream from stdin, which is an io.ReadCloser
		if err = publishStream(room, os.Stdin, codec, fps); err != nil {
			return err
		}
	}

	<-done
	room.Disconnect()
	return nil
}

func publishDemo(room *lksdk.Room) error {
	var tracks []*lksdk.LocalSampleTrack
	for q := livekit.VideoQuality_LOW; q <= livekit.VideoQuality_HIGH; q++ {
		height := 180 * int(math.Pow(2, float64(q)))
		provider, err := provider2.ButterflyLooper(height)
		if err != nil {
			return err
		}
		track, err := lksdk.NewLocalSampleTrack(provider.Codec(),
			lksdk.WithSimulcast("demo-video", provider.ToLayer(q)),
		)
		fmt.Println("simulcast layer", provider.ToLayer(q))
		if err != nil {
			return err
		}
		if err = track.StartWrite(provider, nil); err != nil {
			return err
		}
		tracks = append(tracks, track)
	}

	_, err := room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name: "demo",
	})
	return err
}

func publishFiles(room *lksdk.Room, files []string, fps float64) error {
	for _, f := range files {
		f := f

		// Configure provider
		var pub *lksdk.LocalTrackPublication
		opts := []lksdk.ReaderSampleProviderOption{
			lksdk.ReaderTrackWithOnWriteComplete(func() {
				fmt.Println("finished writing file", f)
				if pub != nil {
					_ = room.LocalParticipant.UnpublishTrack(pub.SID())
				}
			}),
		}

		// Set frame rate if it's a video stream and FPS is set
		ext := filepath.Ext(f)
		if ext == ".h264" || ext == ".ivf" {
			if fps != 0 {
				frameDuration := time.Second / time.Duration(fps)
				opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
			}
		}

		// Create track and publish
		track, err := lksdk.NewLocalFileTrack(f, opts...)
		if err != nil {
			return err
		}
		if pub, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
			Name: f,
		}); err != nil {
			return err
		}
	}
	return nil
}

func publishStream(room *lksdk.Room, in io.ReadCloser, mime string, fps float64) error {
	// Configure provider
	var pub *lksdk.LocalTrackPublication
	opts := []lksdk.ReaderSampleProviderOption{
		lksdk.ReaderTrackWithOnWriteComplete(func() {
			fmt.Println("finished writing stream")
			if pub != nil {
				_ = room.LocalParticipant.UnpublishTrack(pub.SID())
			}
		}),
	}

	// Map CLI mime type (lower-case) to webrtc's mime type (upper-case)
	switch mime {
	case "video/h264":
		mime = webrtc.MimeTypeH264
	case "video/vp8":
		mime = webrtc.MimeTypeVP8
	case "audio/opus":
		mime = webrtc.MimeTypeOpus
	default:
		return lksdk.ErrUnsupportedFileType
	}

	// Set frame rate if it's a video stream and FPS is set
	if strings.EqualFold(mime, webrtc.MimeTypeVP8) || strings.EqualFold(mime, webrtc.MimeTypeH264) {
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
