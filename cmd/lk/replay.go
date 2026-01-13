// Copyright 2024 LiveKit, Inc.
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
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"

	authutil "github.com/livekit/livekit-cli/v2/pkg/auth"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/replay"
	"github.com/livekit/server-sdk-go/v2/signalling"
)

var (
	ReplayCommands = []*cli.Command{
		{
			Name:            "replay",
			Usage:           "experimental (not yet available)",
			Hidden:          true,
			HideHelpCommand: true,
			Commands: []*cli.Command{
				{
					Name:   "list",
					Before: createReplayClient,
					Action: listReplays,
					Flags: []cli.Flag{
						jsonFlag,
						&cli.StringFlag{
							Name:     "room",
							Usage:    "Playback room name",
							Required: false,
						},
						&cli.StringFlag{
							Name:     "token",
							Usage:    "Pagination token",
							Required: false,
						},
					},
				},
				{
					Name:   "playback",
					Before: createReplayClient,
					Action: playback,
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
							Name:     "offset",
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
							Name:     "offset",
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

func createReplayClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}

	url := signalling.ToHttpURL(pc.URL)
	client := replay.NewReplayProtobufClient(url, &http.Client{}, withDefaultClientOpts(pc)...)
	replayClient = &replayServiceClient{
		Replay:    client,
		apiKey:    pc.APIKey,
		apiSecret: pc.APISecret,
	}
	return nil, nil
}

func listReplays(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.ListReplaysRequest{
		RoomName: cmd.String("room"),
	}
	if token := cmd.String("token"); token != "" {
		req.PageToken = &livekit.TokenPagination{Token: token}
	}

	res, err := replayClient.ListReplays(ctx, req)
	if err != nil {
		return err
	}

	slices.SortFunc(res.Replays, func(a, b *replay.ReplayInfo) int {
		if a.StartTime < b.StartTime {
			return 1
		}
		return -1
	})

	if cmd.Bool("json") {
		util.PrintJSON(res.Replays)
	} else {
		table := util.CreateTable().Headers("ReplayID", "RoomName", "StartTime")
		for _, info := range res.Replays {
			table.Row(info.ReplayId, info.RoomName, fmt.Sprint(time.Unix(0, info.StartTime)))
		}
		fmt.Println(table)
	}

	return nil
}

func playback(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.PlaybackRequest{
		ReplayId:     cmd.String("id"),
		PlaybackRoom: cmd.String("room"),
		SeekOffset:   int64(cmd.Int("pts")),
	}
	res, err := replayClient.Playback(ctx, req)
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

	req := &replay.SeekRequest{
		PlaybackId: cmd.String("id"),
		SeekOffset: int64(cmd.Int("pts")),
	}
	_, err = replayClient.Seek(ctx, req)
	return err
}

func closeReplay(ctx context.Context, cmd *cli.Command) error {
	ctx, err := replayClient.withAuth(ctx)
	if err != nil {
		return err
	}

	req := &replay.ClosePlaybackRequest{
		PlaybackId: cmd.String("id"),
	}
	_, err = replayClient.Close(ctx, req)
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
