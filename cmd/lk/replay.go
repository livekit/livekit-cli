package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	authutil "github.com/livekit/livekit-cli/pkg/auth"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/replay"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	ReplayCommands = []*cli.Command{
		{
			Name:            "replay",
			Usage:           "experimental (not yet available)",
			Category:        "I/O",
			Hidden:          true,
			HideHelpCommand: true,
			Commands: []*cli.Command{
				{
					Name:   "list",
					Before: createReplayClient,
					Action: listReplays,
					Flags:  []cli.Flag{jsonFlag},
				},
				{
					Name:   "load",
					Before: createReplayClient,
					Action: loadReplay,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Replay `ID`",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "room",
							Usage:    "Playback room name",
							Required: true,
						},
						&cli.IntFlag{
							Name:     "pts",
							Usage:    "Playback start time",
							Required: false,
						},
					},
				},
				{
					Name:   "seek",
					Before: createReplayClient,
					Action: seek,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Playback `ID`",
							Required: true,
						},
						&cli.IntFlag{
							Name:     "pts",
							Usage:    "Playback start time",
							Required: true,
						},
					},
				},
				{
					Name:   "close",
					Before: createReplayClient,
					Action: closeReplay,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Playback `ID`",
							Required: true,
						},
					},
				},
				{
					Name:   "delete",
					Before: createReplayClient,
					Action: deleteReplay,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Replay `ID`",
							Required: true,
						},
					},
				},
			},
		},
	}

	replayClient *replayServiceClient
)

func createReplayClient(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	url := lksdk.ToHttpURL(pc.URL)
	client := replay.NewReplayProtobufClient(url, &http.Client{}, withDefaultClientOpts(pc)...)
	replayClient = &replayServiceClient{
		Replay:    client,
		apiKey:    pc.APIKey,
		apiSecret: pc.APISecret,
	}
	return nil
}

func listReplays(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.ListReplaysRequest{}
	res, err := replayClient.ListReplays(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		PrintJSON(res.Replays)
	} else {
		table := CreateTable().Headers("ReplayID")
		for _, info := range res.Replays {
			table.Row(info.ReplayId)
		}
		fmt.Println(table)
	}

	return nil
}

func loadReplay(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.LoadReplayRequest{
		ReplayId:    cmd.String("id"),
		RoomName:    cmd.String("room"),
		StartingPts: cmd.Int("pts"),
	}
	res, err := replayClient.LoadReplay(ctx, req)
	if err != nil {
		return err
	}
	fmt.Println("PlaybackID:", res.PlaybackId)

	return nil
}

func seek(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.RoomSeekRequest{
		PlaybackId: cmd.String("id"),
		Pts:        cmd.Int("pts"),
	}
	_, err = replayClient.SeekForRoom(ctx, req)
	return err
}

func closeReplay(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.CloseReplayRequest{
		PlaybackId: cmd.String("id"),
	}
	_, err = replayClient.CloseReplay(ctx, req)
	return err
}

func deleteReplay(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.DeleteReplayRequest{
		ReplayId: cmd.String("id"),
	}
	_, err = replayClient.DeleteReplay(ctx, req)
	return err
}

// temporary replay service client - will eventually move to go SDK
type replayServiceClient struct {
	replay.Replay
	apiKey    string
	apiSecret string
}

func (c *replayServiceClient) withAuth(ctx context.Context) (context.Context, error) {
	at := auth.NewAccessToken(c.apiKey, c.apiSecret)
	token, err := at.ToJWT()
	if err != nil {
		return nil, err
	}

	return twirp.WithHTTPRequestHeaders(ctx, authutil.NewHeaderWithToken(token))
}
