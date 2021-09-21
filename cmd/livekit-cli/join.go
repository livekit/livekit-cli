package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	lksdk "github.com/livekit/server-sdk-go"
	"github.com/urfave/cli/v2"
)

var (
	JoinCommands = []*cli.Command{
		{
			Name:   "join-room",
			Usage:  "joins a room as a client",
			Action: joinRoom,
			Flags: []cli.Flag{
				urlFlag,
				roomFlag,
				identityFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringSliceFlag{
					Name:  "publish",
					Usage: "files to publish as tracks to room (supports .h264, .ivf, .ogg). can be used multiple times to publish multiple files",
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

	fmt.Println("connected to room", room.Name)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	room.Callback.OnDataReceived = func(data []byte, rp *lksdk.RemoteParticipant) {

	}
	files := c.StringSlice("publish")
	for _, f := range files {
		f := f
		var pub *lksdk.LocalTrackPublication
		opts := []lksdk.FileSampleProviderOption{
			lksdk.FileTrackWithOnWriteComplete(func() {
				fmt.Println("finished writing file", f)
				if pub != nil {
					_ = room.LocalParticipant.UnpublishTrack(pub.SID())
				}
			}),
		}
		ext := filepath.Ext(f)
		if ext == ".h264" || ext == ".ivf" {
			fps := c.Float64("fps")
			if fps != 0 {
				frameDuration := time.Microsecond * (1000 * 1000 * 1000 / time.Duration(fps*1000))
				opts = append(opts, lksdk.FileTrackWithFrameDuration(frameDuration))
			}
		}
		track, err := lksdk.NewLocalFileTrack(f, opts...)
		if err != nil {
			return err
		}
		if pub, err = room.LocalParticipant.PublishTrack(track, f); err != nil {
			return err
		}
	}

	<-done
	room.Disconnect()
	return nil
}
