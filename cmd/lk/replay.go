package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

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
				},
				{
					Name:   "load",
					Before: createReplayClient,
					Action: loadReplay,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "replay ID",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "room",
							Usage:    "playback room name",
							Required: true,
						},
						&cli.IntFlag{
							Name:     "pts",
							Usage:    "playback start time",
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
							Usage:    "playback ID",
							Required: true,
						},
						&cli.IntFlag{
							Name:     "pts",
							Usage:    "playback start time",
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
							Usage:    "playback ID",
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
							Usage:    "replay ID",
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

func listReplays(ctx context.Context, _ *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.ListReplaysRequest{}
	res, err := replayClient.ListReplays(ctx, req)
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ReplayID"})
	for _, info := range res.Replays {
		table.Append([]string{
			info.ReplayId,
		})
	}
	table.Render()

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

	return twirp.WithHTTPRequestHeaders(ctx, newHeaderWithToken(token))
}

func newHeaderWithToken(token string) http.Header {
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	return header
}
