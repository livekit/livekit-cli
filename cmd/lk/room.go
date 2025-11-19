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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/pion/webrtc/v4"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	// simulcastURLRegex matches h264 or h265 simulcast URLs in format <codec>://<host:port>/<width>x<height> or <codec>://<socket_path>/<width>x<height>
	simulcastURLRegex = regexp.MustCompile(`^(h264|h265)://(.+)/(\d+)x(\d+)$`)

	RoomCommands = []*cli.Command{
		{
			Name:  "room",
			Usage: "Create or delete rooms and manage existing room properties",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Create a room",
					ArgsUsage: "ROOM_NAME",
					Before:    createRoomClient,
					Action:    createRoom,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:   "name",
							Hidden: true,
						},
						&cli.StringFlag{
							Name:      "room-egress-file",
							Usage:     "RoomCompositeRequest `JSON` file (see examples/room-composite-file.json)",
							TakesFile: true,
						},
						&cli.StringFlag{
							Name:      "participant-egress-file",
							Usage:     "ParticipantEgress `JSON` file (see examples/auto-participant-egress.json)",
							TakesFile: true,
						},
						&cli.StringFlag{
							Name:      "track-egress-file",
							Usage:     "AutoTrackEgress `JSON` file (see examples/auto-track-egress.json)",
							TakesFile: true,
						},
						&cli.StringFlag{
							Name:      "agents-file",
							Usage:     "Agents configuration `JSON` file",
							TakesFile: true,
						},
						&cli.StringFlag{
							Name:  "room-preset",
							Usage: "`NAME` of the room configuration preset to associate with the created room",
						},
						&cli.UintFlag{
							Name:  "min-playout-delay",
							Usage: "Minimum playout delay for video (in `MS`)",
						},
						&cli.UintFlag{
							Name:  "max-playout-delay",
							Usage: "Maximum playout delay for video (in `MS`)",
						},
						&cli.BoolFlag{
							Name:  "sync-streams",
							Usage: "Improve A/V sync by placing them in the same stream. when enabled, transceivers will not be reused",
						},
						&cli.UintFlag{
							Name:  "empty-timeout",
							Usage: "Number of `SECS` to keep the room open before any participant joins",
						},
						&cli.UintFlag{
							Name:  "departure-timeout",
							Usage: "Number of `SECS` to keep the room open after the last participant leaves",
						},
						&cli.BoolFlag{
							Name:   "replay-enabled",
							Usage:  "experimental (not yet available)",
							Hidden: true,
						},
					},
				},
				{
					Name:      "list",
					Usage:     "List or search for active rooms by name",
					Before:    createRoomClient,
					Action:    listRooms,
					ArgsUsage: "[ROOM_NAME ...]",
					Flags:     []cli.Flag{jsonFlag},
				},
				{
					Name:   "update",
					Usage:  "Modify properties of an active room",
					Before: createRoomClient,
					Action: updateRoomMetadata,
					Flags: []cli.Flag{
						hidden(optional(roomFlag)),
						&cli.StringFlag{
							Name:     "metadata",
							Required: true,
						},
					},
					ArgsUsage: "ROOM_NAME",
				},
				{
					Name:      "delete",
					Usage:     "Delete a room",
					UsageText: "lk room delete [OPTIONS] ROOM_NAME",
					Before:    createRoomClient,
					Action:    deleteRoom,
					ArgsUsage: "ROOM_NAME_OR_ID",
				},
				{
					Name:      "join",
					Usage:     "Joins a room as a participant",
					UsageText: "lk room join [OPTIONS] ROOM_NAME",
					Action:    joinRoom,
					ArgsUsage: "ROOM_NAME",
					Flags: []cli.Flag{
						optional(identityFlag),
						optional(roomFlag),
						openFlag,
						&cli.BoolFlag{
							Name:  "publish-demo",
							Usage: "Publish demo video as a loop",
						},
						&cli.StringSliceFlag{
							Name:      "publish",
							TakesFile: true,
							Usage: "`FILES` to publish as tracks to room (supports .h264, .ivf, .ogg). " +
								"Can be used multiple times to publish multiple files. " +
								"Can publish from Unix or TCP socket using the format '<codec>:///<socket_path>' or '<codec>://<host:port>' respectively. Valid codecs are \"h264\", \"h265\", \"vp8\", \"opus\". " +
								"For simulcast: use 2-3 h264:// or h265:// URLs with format '<codec>://<host:port>/<width>x<height>' or '<codec>:///path/to/<socket_path>/<width>x<height>' (all layers must use the same codec; quality determined by width order)",
						},
						&cli.StringFlag{
							Name:  "publish-data",
							Usage: "Publish user data to the room.",
						},
						&cli.StringFlag{
							Name:  "publish-dtmf",
							Usage: "Publish DTMF digits to the room. Character 'w' adds 0.5 sec delay.",
						},
						&cli.FloatFlag{
							Name:  "fps",
							Usage: "If video files are published, indicates `FPS` of video",
						},
						&cli.StringFlag{
							Name:  "h26x-streaming-format",
							Usage: "Format to use when reading H.264 from file or socket, \"annex-b\" OR \"length-prefixed\"",
							Value: "annex-b",
						},
						&cli.BoolFlag{
							Name:  "exit-after-publish",
							Usage: "When publishing, exit after file or stream is complete",
						},
						&cli.StringSliceFlag{
							Name:  "attribute",
							Usage: "set attributes in key=value format, can be used multiple times",
						},
						&cli.StringFlag{
							Name:      "attribute-file",
							Usage:     "read attributes from a `JSON` file",
							TakesFile: true,
						},
						&cli.BoolFlag{
							Name:  "auto-subscribe",
							Usage: "Automatically subscribe to published tracks.",
							Value: false,
						},
						&cli.StringFlag{
							Name:  "metadata",
							Usage: "`JSON` metadata which will be passed to participant",
						},
					},
				},
				{
					Name:   "participants",
					Usage:  "Manage room participants",
					Before: createRoomClient,
					Commands: []*cli.Command{
						{
							Name:      "list",
							Usage:     "List or search for active rooms by name",
							Action:    listParticipants,
							ArgsUsage: "ROOM_NAME",
						},
						{
							Name:      "get",
							Usage:     "Fetch metadata of a room participant",
							ArgsUsage: "ID",
							Before:    createRoomClient,
							Action:    getParticipant,
							Flags: []cli.Flag{
								roomFlag,
								optional(identityFlag),
							},
						},
						{
							Name:      "remove",
							Usage:     "Remove a participant from a room",
							ArgsUsage: "ID",
							Before:    createRoomClient,
							Action:    removeParticipant,
							Flags: []cli.Flag{
								roomFlag,
								optional(identityFlag),
							},
						},
						{
							Name:   "forward",
							Usage:  "Forward a participant to a different room",
							Before: createRoomClient,
							Action: forwardParticipant,
							Flags: []cli.Flag{
								roomFlag,
								identityFlag,
								&cli.StringFlag{
									Name:  "destination-room",
									Usage: "`NAME` of the destination room",
								},
							},
						},
						{
							Name:   "move",
							Usage:  "Move a participant to a different room",
							Before: createRoomClient,
							Action: moveParticipant,
							Flags: []cli.Flag{
								roomFlag,
								identityFlag,
								&cli.StringFlag{
									Name:  "destination-room",
									Usage: "`NAME` of the destination room",
								},
							},
						},
						{
							Name:      "update",
							Usage:     "Change the metadata and permissions for a room participant",
							ArgsUsage: "ID",
							Before:    createRoomClient,
							Action:    updateParticipant,
							Flags: []cli.Flag{
								roomFlag,
								optional(identityFlag),
								&cli.StringFlag{
									Name:  "metadata",
									Usage: "JSON describing participant metadata (existing values for unset fields)",
								},
								&cli.StringFlag{
									Name:  "permissions",
									Usage: "JSON describing participant permissions (existing values for unset fields)",
								},
							},
						},
					},
				},
				{
					Name:      "mute-track",
					Usage:     "Mute or unmute a track",
					UsageText: "lk room mute-track OPTIONS TRACK_SID",
					ArgsUsage: "TRACK_SID",
					Before:    createRoomClient,
					Action:    muteTrack,
					MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
						Flags: [][]cli.Flag{
							{
								&cli.BoolFlag{
									Name:    "mute",
									Aliases: []string{"m"},
									Usage:   "Mute the track",
								},
								&cli.BoolFlag{
									Name:    "unmute",
									Aliases: []string{"u"},
									Usage:   "Unmute the track",
								},
							},
						},
					}},
					Flags: []cli.Flag{
						roomFlag,
						identityFlag,
						&cli.StringFlag{
							Hidden: true, // deprecated: use ARG0
							Name:   "track",
							Usage:  "Track `SID` to mute",
						},
					},
				},
				{
					Name:      "update-subscriptions",
					Usage:     "Subscribe or unsubscribe from a track",
					UsageText: "lk room update-subscriptions OPTIONS TRACK_SID",
					ArgsUsage: "TRACK_SID",
					Before:    createRoomClient,
					Action:    updateSubscriptions,
					Flags: []cli.Flag{
						roomFlag,
						identityFlag,
						&cli.StringSliceFlag{
							Hidden: true, // deprecated: use ARG0
							Name:   "track",
							Usage:  "Track `SID` to subscribe/unsubscribe",
						},
					},
					MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
						Flags: [][]cli.Flag{{
							&cli.BoolFlag{
								Name:    "subscribe",
								Aliases: []string{"s"},
								Usage:   "Subscribe to the track",
							},
							&cli.BoolFlag{
								Name:    "unsubscribe",
								Aliases: []string{"S"},
								Usage:   "Unsubscribe to the track",
							},
						}},
					}},
				},
				{
					Name:      "send-data",
					Before:    createRoomClient,
					Action:    sendData,
					Usage:     "Send arbitrary JSON data to client",
					UsageText: "lk room send-data [OPTIONS] DATA",
					ArgsUsage: "JSON",
					Flags: []cli.Flag{
						roomFlag,
						&cli.StringFlag{
							Hidden: true, // deprecated: use ARG0
							Name:   "data",
							Usage:  "`JSON` payload to send to client",
						},
						&cli.StringFlag{
							Name:  "topic",
							Usage: "`TOPIC` of the message",
						},
						&cli.StringSliceFlag{
							Name:  "identity",
							Usage: "One or more participant identities to send the message to. When empty, broadcasts to the entire room",
						},
					},
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `room create`
			Name:   "create-room",
			Before: createRoomClient,
			Action: createRoom,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "name",
					Usage:    "name of the room",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "room-egress-file",
					Usage: "RoomCompositeRequest json file (see examples/room-composite-file.json)",
				},
				&cli.StringFlag{
					Name:  "participant-egress-file",
					Usage: "ParticipantEgress json file (see examples/auto-participant-egress.json)",
				},
				&cli.StringFlag{
					Name:  "track-egress-file",
					Usage: "AutoTrackEgress json file (see examples/auto-track-egress.json)",
				},
				&cli.StringFlag{
					Name:  "agents-file",
					Usage: "Agents configuration json file",
				},
				&cli.StringFlag{
					Name:  "room-configuration",
					Usage: "Name of the room configuration to associate with the created room",
				},
				&cli.UintFlag{
					Name:  "min-playout-delay",
					Usage: "minimum playout delay for video (in ms)",
				},
				&cli.UintFlag{
					Name:  "max-playout-delay",
					Usage: "maximum playout delay for video (in ms)",
				},
				&cli.BoolFlag{
					Name:  "sync-streams",
					Usage: "improve A/V sync by placing them in the same stream. when enabled, transceivers will not be reused",
				},
				&cli.UintFlag{
					Name:  "empty-timeout",
					Usage: "number of seconds to keep the room open before any participant joins",
				},
				&cli.UintFlag{
					Name:  "departure-timeout",
					Usage: "number of seconds to keep the room open after the last participant leaves",
				},
				&cli.BoolFlag{
					Name:   "replay-enabled",
					Usage:  "experimental (not yet available)",
					Hidden: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `room list``
			Name:   "list-rooms",
			Before: createRoomClient,
			Action: listRooms,
		},
		{
			Hidden: true, // deprecated: use `room list`
			Name:   "list-room",
			Before: createRoomClient,
			Action: _deprecatedListRoom,
			Flags: []cli.Flag{
				roomFlag,
			},
		},
		{
			Hidden: true, // deprecated: use `room update-metadata`
			Name:   "update-room-metadata",
			Before: createRoomClient,
			Action: _deprecatedUpdateRoomMetadata,
			Flags: []cli.Flag{
				roomFlag,
				&cli.StringFlag{
					Name: "metadata",
				},
			},
		},
		{
			Hidden: true, // deprecated: use `room participants list`
			Name:   "list-participants",
			Before: createRoomClient,
			Action: _deprecatedListParticipants,
			Flags: []cli.Flag{
				roomFlag,
			},
		},
		{
			Hidden: true, // deprecated: use `room participants get`
			Name:   "get-participant",
			Before: createRoomClient,
			Action: getParticipant,
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
			},
		},
		{
			Hidden: true, // deprecated: use `room participants remove`
			Name:   "remove-participant",
			Before: createRoomClient,
			Action: removeParticipant,
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
			},
		},
		{
			Hidden: true, // deprecated: use `room participants update`
			Name:   "update-participant",
			Before: createRoomClient,
			Action: updateParticipant,
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
				&cli.StringFlag{
					Name:  "metadata",
					Usage: "`JSON` describing participant metadata",
				},
				&cli.StringFlag{
					Name:  "permissions",
					Usage: "`JSON` describing participant permissions (existing values for unset fields)",
				},
			},
		},
		{
			Hidden:    true, // deprecated: use `room mute-track`
			Name:      "mute-track",
			Usage:     "Mute or unmute a track",
			UsageText: "lk room mute-track OPTIONS TRACK_SID",
			ArgsUsage: "TRACK_SID",
			Before:    createRoomClient,
			Action:    muteTrack,
			MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{
				Flags: [][]cli.Flag{
					{
						&cli.BoolFlag{
							Name:    "m",
							Aliases: []string{"mute", "muted"},
							Usage:   "Mute the track",
						},
						&cli.BoolFlag{
							Name:    "u",
							Aliases: []string{"unmute"},
							Usage:   "Unmute the track",
						},
					},
				},
			}},
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
				&cli.StringFlag{
					Hidden: true, // deprecated: use ARG0
					Name:   "track",
					Usage:  "Track `SID` to mute",
				},
			},
		},
		{
			Hidden:    true, // deprecated: use `room update-subscriptions`
			Name:      "update-subscriptions",
			Usage:     "Subscribe or unsubscribe from a track",
			UsageText: "lk room update-subscriptions OPTIONS TRACK_SID",
			ArgsUsage: "TRACK_SID",
			Before:    createRoomClient,
			Action:    updateSubscriptions,
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
				&cli.StringSliceFlag{
					Hidden: true, // deprecated: use ARG0
					Name:   "track",
					Usage:  "Track `SID` to subscribe/unsubscribe",
				},
				&cli.BoolFlag{
					Name:  "subscribe",
					Usage: "Set to true to subscribe, otherwise it'll unsubscribe",
				},
			},
		},
		{
			Hidden:    true, // deprecated: use `room send-data`
			Name:      "send-data",
			Before:    createRoomClient,
			Action:    sendData,
			Usage:     "Send arbitrary JSON data to client",
			UsageText: "lk room send-data [OPTIONS] DATA",
			ArgsUsage: "JSON",
			Flags: []cli.Flag{
				roomFlag,
				&cli.StringFlag{
					Hidden: true, // deprecated: use ARG0
					Name:   "data",
					Usage:  "`JSON` payload to send to client",
				},
				&cli.StringFlag{
					Name:  "topic",
					Usage: "`TOPIC` of the message",
				},
				&cli.StringSliceFlag{
					Hidden: true, // deprecated: use `--participant-ids`
					Name:   "participantID",
					Usage:  "list of participantID to send the message to",
				},
			},
		},
	}

	roomClient *lksdk.RoomServiceClient
)

func createRoomClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return nil, err
	}

	roomClient = lksdk.NewRoomServiceClient(project.URL, project.APIKey, project.APISecret, withDefaultClientOpts(project)...)
	return nil, nil
}

func createRoom(ctx context.Context, cmd *cli.Command) error {
	name, err := extractFlagOrArg(cmd, "name")
	if err != nil {
		return err
	}

	req := &livekit.CreateRoomRequest{
		Name: name,
	}

	if roomEgressFile := cmd.String("room-egress-file"); roomEgressFile != "" {
		roomEgress := &livekit.RoomCompositeEgressRequest{}
		b, err := os.ReadFile(roomEgressFile)
		if err != nil {
			return err
		}
		if err = protojson.Unmarshal(b, roomEgress); err != nil {
			return err
		}
		req.Egress = &livekit.RoomEgress{Room: roomEgress}
	}

	if participantEgressFile := cmd.String("participant-egress-file"); participantEgressFile != "" {
		participantEgress := &livekit.AutoParticipantEgress{}
		b, err := os.ReadFile(participantEgressFile)
		if err != nil {
			return err
		}
		if err = protojson.Unmarshal(b, participantEgress); err != nil {
			return err
		}
		if req.Egress == nil {
			req.Egress = &livekit.RoomEgress{}
		}
		req.Egress.Participant = participantEgress
	}

	if trackEgressFile := cmd.String("track-egress-file"); trackEgressFile != "" {
		trackEgress := &livekit.AutoTrackEgress{}
		b, err := os.ReadFile(trackEgressFile)
		if err != nil {
			return err
		}
		if err = protojson.Unmarshal(b, trackEgress); err != nil {
			return err
		}
		if req.Egress == nil {
			req.Egress = &livekit.RoomEgress{}
		}
		req.Egress.Tracks = trackEgress
	}

	if agentsFile := cmd.String("agents-file"); agentsFile != "" {
		agent := &livekit.RoomAgent{}
		b, err := os.ReadFile(agentsFile)
		if err != nil {
			return err
		}
		if err = protojson.Unmarshal(b, agent); err != nil {
			return err
		}
		req.Agents = agent.Dispatches
	}

	if roomPreset := cmd.String("room-preset"); roomPreset != "" {
		req.RoomPreset = roomPreset
	}

	if cmd.Uint("min-playout-delay") != 0 {
		fmt.Printf("setting min playout delay: %d\n", cmd.Uint("min-playout-delay"))
		req.MinPlayoutDelay = uint32(cmd.Uint("min-playout-delay"))
	}

	if maxPlayoutDelay := cmd.Uint("max-playout-delay"); maxPlayoutDelay != 0 {
		fmt.Printf("setting max playout delay: %d\n", maxPlayoutDelay)
		req.MaxPlayoutDelay = uint32(maxPlayoutDelay)
	}

	if syncStreams := cmd.Bool("sync-streams"); syncStreams {
		fmt.Printf("setting sync streams: %t\n", syncStreams)
		req.SyncStreams = syncStreams
	}

	if emptyTimeout := cmd.Uint("empty-timeout"); emptyTimeout != 0 {
		fmt.Printf("setting empty timeout: %d\n", emptyTimeout)
		req.EmptyTimeout = uint32(emptyTimeout)
	}

	if departureTimeout := cmd.Uint("departure-timeout"); departureTimeout != 0 {
		fmt.Printf("setting departure timeout: %d\n", departureTimeout)
		req.DepartureTimeout = uint32(departureTimeout)
	}

	if replayEnabled := cmd.Bool("replay-enabled"); replayEnabled {
		fmt.Printf("setting replay enabled: %t\n", replayEnabled)
		req.ReplayEnabled = replayEnabled
	}

	room, err := roomClient.CreateRoom(ctx, req)
	if err != nil {
		return err
	}

	util.PrintJSON(room)
	return nil
}

func listRooms(ctx context.Context, cmd *cli.Command) error {
	names, _ := extractArgs(cmd)
	if cmd.Bool("verbose") && len(names) > 0 {
		fmt.Printf(
			"Querying rooms matching %s",
			strings.Join(util.MapStrings(names, util.WrapWith("\"")), ", "),
		)
	}

	req := livekit.ListRoomsRequest{}
	if len(names) > 0 {
		req.Names = names
	}

	res, err := roomClient.ListRooms(ctx, &req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(res)
	} else {
		table := util.CreateTable().Headers("RoomID", "Name", "Participants", "Publishers")
		for _, rm := range res.Rooms {
			table.Row(
				rm.Sid,
				rm.Name,
				fmt.Sprintf("%d", rm.NumParticipants),
				fmt.Sprintf("%d", rm.NumPublishers),
			)
		}
		fmt.Println(table)
	}

	return nil
}

func _deprecatedListRoom(ctx context.Context, cmd *cli.Command) error {
	res, err := roomClient.ListRooms(ctx, &livekit.ListRoomsRequest{
		Names: []string{cmd.String("room")},
	})
	if err != nil {
		return err
	}
	if len(res.Rooms) == 0 {
		fmt.Printf("there is no matching room with name: %s\n", cmd.String("room"))
		return nil
	}
	rm := res.Rooms[0]
	util.PrintJSON(rm)
	return nil
}

func deleteRoom(ctx context.Context, cmd *cli.Command) error {
	roomId, err := extractArg(cmd)
	if err != nil {
		return err
	}

	_, err = roomClient.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: roomId,
	})
	if err != nil {
		return err
	}

	fmt.Println("deleted room", roomId)
	return nil
}

func updateRoomMetadata(ctx context.Context, cmd *cli.Command) error {
	roomName, _ := extractArg(cmd)
	res, err := roomClient.UpdateRoomMetadata(ctx, &livekit.UpdateRoomMetadataRequest{
		Room:     roomName,
		Metadata: cmd.String("metadata"),
	})
	if err != nil {
		return err
	}

	fmt.Println("Updated room metadata")
	util.PrintJSON(res)
	return nil
}

func _deprecatedUpdateRoomMetadata(ctx context.Context, cmd *cli.Command) error {
	roomName := cmd.String("room")
	res, err := roomClient.UpdateRoomMetadata(ctx, &livekit.UpdateRoomMetadataRequest{
		Room:     roomName,
		Metadata: cmd.String("metadata"),
	})
	if err != nil {
		return err
	}

	fmt.Println("Updated room metadata")
	util.PrintJSON(res)
	return nil
}

func joinRoom(ctx context.Context, cmd *cli.Command) error {
	publishUrls := cmd.StringSlice("publish")

	// Determine simulcast mode by checking if any URL has simulcast format
	simulcastMode := false
	for _, url := range publishUrls {
		if simulcastURLRegex.MatchString(url) {
			simulcastMode = true
			break
		}
	}

	// Validate publish flags
	if len(publishUrls) > 3 {
		return fmt.Errorf("no more than 3 --publish flags can be specified, got %d", len(publishUrls))
	}

	// If simulcast mode, validate all URLs are h264 or h265 format with dimensions
	if simulcastMode {
		if len(publishUrls) == 1 {
			return fmt.Errorf("simulcast mode requires 2-3 streams, but only 1 was provided")
		}
		var firstCodec string
		for i, url := range publishUrls {
			parts, err := parseSimulcastURL(url)
			if err != nil {
				return fmt.Errorf("publish flag %d: %w", i+1, err)
			}
			if i == 0 {
				firstCodec = parts.codec
			} else if parts.codec != firstCodec {
				return fmt.Errorf("publish flag %d: simulcast layers must use the same codec; expected %s://, got %s://", i+1, firstCodec, parts.codec)
			}
		}
	}

	_, err := requireProject(ctx, cmd)
	if err != nil {
		return err
	}

	roomName, err := extractFlagOrArg(cmd, "room")
	if err != nil {
		return err
	}

	participantIdentity := cmd.String("identity")
	if participantIdentity == "" {
		participantIdentity = util.ExpandTemplate("participant-%x")
		fmt.Printf("Using generated participant identity [%s]\n", util.Accented(participantIdentity))
	}

	autoSubscribe := cmd.Bool("auto-subscribe")

	done := make(chan os.Signal, 1)
	roomCB := &lksdk.RoomCallback{
		OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
			logger.Infow("participant connected",
				"kind", p.Kind(),
				"pID", p.SID(),
				"participant", p.Identity(),
			)
		},
		OnParticipantDisconnected: func(p *lksdk.RemoteParticipant) {
			logger.Infow("participant disconnected",
				"kind", p.Kind(),
				"pID", p.SID(),
				"participant", p.Identity(),
			)
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataPacket: func(p lksdk.DataPacket, params lksdk.DataReceiveParams) {
				identity := params.SenderIdentity
				switch p := p.(type) {
				case *lksdk.UserDataPacket:
					logger.Infow("received data", "data", p.Payload, "participant", identity)
				case *livekit.SipDTMF:
					logger.Infow("received dtmf", "digits", p.Digit, "participant", identity)
				default:
					logger.Infow("received unsupported data", "data", p, "participant", identity)
				}
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

	participantAttributes, err := parseKeyValuePairs(cmd, "attribute")
	if err != nil {
		return fmt.Errorf("failed to parse participant attributes: %w", err)
	}

	// Read attributes from JSON file if specified
	if attrFile := cmd.String("attribute-file"); attrFile != "" {
		fileData, err := os.ReadFile(attrFile)
		if err != nil {
			return fmt.Errorf("failed to read attribute file: %w", err)
		}

		var fileAttrs map[string]string
		if err := json.Unmarshal(fileData, &fileAttrs); err != nil {
			return fmt.Errorf("failed to parse attribute file as JSON: %w", err)
		}

		// Add attributes from file to the existing ones
		maps.Copy(participantAttributes, fileAttrs)
	}

	room, err := lksdk.ConnectToRoom(project.URL, lksdk.ConnectInfo{
		APIKey:                project.APIKey,
		APISecret:             project.APISecret,
		RoomName:              roomName,
		ParticipantIdentity:   participantIdentity,
		ParticipantAttributes: participantAttributes,
		ParticipantMetadata:   cmd.String("metadata"),
	}, roomCB, lksdk.WithAutoSubscribe(autoSubscribe))
	if err != nil {
		return err
	}
	defer room.Disconnect()

	logger.Infow("connected to room", "room", room.Name())

	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if cmd.Bool("publish-demo") {
		if err = publishDemo(room); err != nil {
			return err
		}
	}

	exitAfterPublish := cmd.Bool("exit-after-publish")

	// Handle publishing
	if len(publishUrls) > 0 {
		if simulcastMode {
			// Handle simulcast publishing
			fps := cmd.Float("fps")
			h26xStreamingFormat := cmd.String("h26x-streaming-format")
			onPublishComplete := func(pub *lksdk.LocalTrackPublication) {
				if exitAfterPublish {
					close(done)
					return
				}
				if pub != nil {
					fmt.Printf("finished simulcast stream %s\n", pub.Name())
					_ = room.LocalParticipant.UnpublishTrack(pub.SID())
				}
			}

			if err = handleSimulcastPublish(room, publishUrls, fps, h26xStreamingFormat, onPublishComplete); err != nil {
				return err
			}
		} else {
			// Handle single publish
			fps := cmd.Float("fps")
			h26xStreamingFormat := cmd.String("h26x-streaming-format")
			for _, pub := range publishUrls {
				onPublishComplete := func(pub *lksdk.LocalTrackPublication) {
					if exitAfterPublish {
						close(done)
						return
					}
					if pub != nil {
						fmt.Printf("finished writing %s\n", pub.Name())
						_ = room.LocalParticipant.UnpublishTrack(pub.SID())
					}
				}
				if err = handlePublish(room, pub, fps, h26xStreamingFormat, onPublishComplete); err != nil {
					return err
				}
			}
		}
	}

	publishPacket := func(p lksdk.DataPacket) error {
		if err = room.LocalParticipant.PublishDataPacket(p, lksdk.WithDataPublishReliable(true)); err != nil {
			return err
		}
		if exitAfterPublish {
			close(done)
		}
		return nil
	}
	if data := cmd.String("publish-data"); data != "" {
		if err = publishPacket(&lksdk.UserDataPacket{Payload: []byte(data)}); err != nil {
			return err
		}
	}
	if dtmf := cmd.String("publish-dtmf"); dtmf != "" {
		if err = publishPacket(&livekit.SipDTMF{Digit: dtmf}); err != nil {
			return err
		}
	}

	if cmd.IsSet("open") {
		switch cmd.String("open") {
		case string(util.OpenTargetMeet):
			at := auth.NewAccessToken(project.APIKey, project.APISecret).
				SetIdentity(participantIdentity + "_observer").
				SetVideoGrant(&auth.VideoGrant{
					Room:     roomName,
					RoomJoin: true,
				})
			token, _ := at.ToJWT()
			_ = util.OpenInMeet(project.URL, token)
		}
	}

	<-done
	return nil
}

func listParticipants(ctx context.Context, cmd *cli.Command) error {
	roomName, err := extractArg(cmd)
	if err != nil {
		return err
	}

	res, err := roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: roomName,
	})
	if err != nil {
		return err
	}

	for _, p := range res.Participants {
		fmt.Printf("%s (%s)\t tracks: %d\n", p.Identity, p.State.String(), len(p.Tracks))
	}
	return nil
}

func _deprecatedListParticipants(ctx context.Context, cmd *cli.Command) error {
	roomName := cmd.String("room")
	res, err := roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: roomName,
	})
	if err != nil {
		return err
	}

	for _, p := range res.Participants {
		fmt.Printf("%s (%s)\t tracks: %d\n", p.Identity, p.State.String(), len(p.Tracks))
	}
	return nil
}

func getParticipant(ctx context.Context, cmd *cli.Command) error {
	_ = ctx
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	res, err := roomClient.GetParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	if err != nil {
		return err
	}

	util.PrintJSON(res)

	return nil
}

func updateParticipant(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	metadata := cmd.String("metadata")
	permissions := cmd.String("permissions")
	if metadata == "" && permissions == "" {
		return fmt.Errorf("either metadata or permissions must be set")
	}

	req := &livekit.UpdateParticipantRequest{
		Room:     roomName,
		Identity: identity,
		Metadata: metadata,
	}
	if permissions != "" {
		// load existing participant
		participant, err := roomClient.GetParticipant(ctx, &livekit.RoomParticipantIdentity{
			Room:     roomName,
			Identity: identity,
		})
		if err != nil {
			return err
		}

		req.Permission = participant.Permission
		if req.Permission != nil {
			if err = json.Unmarshal([]byte(permissions), req.Permission); err != nil {
				return err
			}
		}
	}

	fmt.Println("updating participant...")
	util.PrintJSON(req)
	if _, err := roomClient.UpdateParticipant(ctx, req); err != nil {
		return err
	}
	fmt.Println("participant updated.")

	return nil
}

func removeParticipant(ctx context.Context, cmd *cli.Command) error {
	_ = ctx
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	_, err := roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	if err != nil {
		return err
	}

	fmt.Println("successfully removed participant", identity)

	return nil
}

func forwardParticipant(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	destinationRoomName := cmd.String("destination-room")
	if destinationRoomName == "" {
		return fmt.Errorf("destination-room is required")
	}

	_, err := roomClient.ForwardParticipant(ctx, &livekit.ForwardParticipantRequest{
		Room:            roomName,
		Identity:        identity,
		DestinationRoom: destinationRoomName,
	})
	if err != nil {
		return err
	}

	fmt.Println("successfully forwarded participant", identity, "from", roomName, "to", destinationRoomName)

	return nil
}

func moveParticipant(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	destinationRoomName := cmd.String("destination-room")
	if destinationRoomName == "" {
		return fmt.Errorf("destination-room is required")
	}

	_, err := roomClient.MoveParticipant(ctx, &livekit.MoveParticipantRequest{
		Room:            roomName,
		Identity:        identity,
		DestinationRoom: destinationRoomName,
	})
	if err != nil {
		return err
	}

	fmt.Println("successfully moved participant", identity, "from", roomName, "to", destinationRoomName)
	return nil
}

func muteTrack(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromFlags(cmd)
	muted := cmd.Bool("mute") || !cmd.Bool("unmute")
	trackSid := cmd.String("track")
	if trackSid == "" {
		trackSid = cmd.Args().First()
	}
	_, err := roomClient.MutePublishedTrack(ctx, &livekit.MuteRoomTrackRequest{
		Room:     roomName,
		Identity: identity,
		TrackSid: trackSid,
		Muted:    muted,
	})
	if err != nil {
		return err
	}

	verb := "Muted"
	if !muted {
		verb = "Unmuted"
	}
	fmt.Printf("%s track [%s]\n", verb, trackSid)
	return nil
}

func updateSubscriptions(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromFlags(cmd)
	trackSids := cmd.StringSlice("track")
	if cmd.Args().Len() > 0 {
		trackSids = append(trackSids, cmd.Args().Slice()...)
	}
	subscribe := cmd.Bool("subscribe") || !cmd.Bool("unsubscribe")
	_, err := roomClient.UpdateSubscriptions(ctx, &livekit.UpdateSubscriptionsRequest{
		Room:      roomName,
		Identity:  identity,
		TrackSids: trackSids,
		Subscribe: subscribe,
	})
	if err != nil {
		return err
	}

	verb := "Subscribed to"
	if !subscribe {
		verb = "Unsubscribed from"
	}
	fmt.Printf("%s tracks %v\n", verb, trackSids)
	return nil
}

func sendData(ctx context.Context, cmd *cli.Command) error {
	roomName, _ := participantInfoFromFlags(cmd)
	identities := cmd.StringSlice("identity")
	data := cmd.String("data")
	if data == "" {
		data = cmd.Args().First()
	}
	topic := cmd.String("topic")
	req := &livekit.SendDataRequest{
		Room:                  roomName,
		Data:                  []byte(data),
		DestinationIdentities: identities,
		// deprecated
		DestinationSids: cmd.StringSlice("participantID"),
	}
	if topic != "" {
		req.Topic = &topic
	}
	_, err := roomClient.SendData(ctx, req)
	if err != nil {
		return err
	}

	fmt.Println("successfully sent data to room", roomName)
	return nil
}

func participantInfoFromFlags(c *cli.Command) (string, string) {
	return c.String("room"), c.String("identity")
}

func participantInfoFromArgOrFlags(c *cli.Command) (string, string) {
	room := c.String("room")
	id := c.String("identity")
	if id == "" {
		id = c.Args().First()
	}
	return room, id
}
