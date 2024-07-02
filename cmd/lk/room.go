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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	RoomCommands = []*cli.Command{
		{
			Name:        "room",
			Description: "The Rooms API TKTK",
			Usage:       "Create or delete rooms and manage existing room properties",
			Category:    "Core",
			Commands: []*cli.Command{
				{
					Name:   "create",
					Usage:  "Create a room",
					Before: createRoomClient,
					Action: createRoom,
					Flags: withDefaultFlags(
						&cli.StringFlag{
							Name:  "room-egress-file",
							Usage: "RoomCompositeRequest `JSON` file (see examples/room-composite-file.json)",
						},
						&cli.StringFlag{
							Name:  "participant-egress-file",
							Usage: "ParticipantEgress `JSON` file (see examples/auto-participant-egress.json)",
						},
						&cli.StringFlag{
							Name:  "track-egress-file",
							Usage: "AutoTrackEgress `JSON` file (see examples/auto-track-egress.json)",
						},
						&cli.StringFlag{
							Name:  "agents-file",
							Usage: "Agents configuration `JSON` file",
						},
						&cli.StringFlag{
							Name:  "room-configuration",
							Usage: "`NAME` of the room configuration to associate with the created room",
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
					),
					ArgsUsage: " ROOM_NAME",
				},
				{
					Name:      "list",
					Usage:     "List or search for active rooms by name",
					Before:    createRoomClient,
					Action:    listRooms,
					Flags:     withDefaultFlags(),
					ArgsUsage: " [ROOM_NAME ...]",
				},
				{
					Name:   "update",
					Usage:  "Modify properties of an active room",
					Before: createRoomClient,
					Action: updateRoomMetadata,
					Flags: withDefaultFlags(
						&cli.StringFlag{
							Name:     "metadata",
							Required: true,
						},
					),
					ArgsUsage: " ROOM_NAME",
				},
				{
					Name:      "delete",
					Usage:     "Delete a room",
					Before:    createRoomClient,
					Action:    deleteRoom,
					Flags:     withDefaultFlags(),
					ArgsUsage: " ROOM_NAME_OR_ID",
				},
				{
					Name:  "participants",
					Usage: "Manage room participants",
					Commands: []*cli.Command{
						{
							Name:      "list",
							Usage:     "List or search for active rooms by name",
							Before:    createRoomClient,
							Action:    listParticipants,
							Flags:     withDefaultFlags(),
							ArgsUsage: " [ROOM_NAME ...]",
						},
					},
				},
				{
					Name:   "list-participants",
					Before: createRoomClient,
					Action: _deprecatedListParticipants,
					Flags: withDefaultFlags(
						roomFlag,
					),
				},
				{
					Name:   "get-participant",
					Before: createRoomClient,
					Action: getParticipant,
					Flags: withDefaultFlags(
						roomFlag,
						identityFlag,
					),
				},
				{
					Name:   "remove-participant",
					Before: createRoomClient,
					Action: removeParticipant,
					Flags: withDefaultFlags(
						roomFlag,
						identityFlag,
					),
				},
				{
					Name:   "update-participant",
					Before: createRoomClient,
					Action: updateParticipant,
					Flags: withDefaultFlags(
						roomFlag,
						identityFlag,
						&cli.StringFlag{
							Name: "metadata",
						},
						&cli.StringFlag{
							Name:  "permissions",
							Usage: "JSON describing participant permissions (existing values for unset fields)",
						},
					),
				},
				{
					Name:   "mute-track",
					Before: createRoomClient,
					Action: muteTrack,
					Flags: withDefaultFlags(
						roomFlag,
						identityFlag,
						&cli.StringFlag{
							Name:     "track",
							Usage:    "track sid to mute",
							Required: true,
						},
						&cli.BoolFlag{
							Name:  "muted",
							Usage: "set to true to mute, false to unmute",
						},
					),
				},
				{
					Name:   "update-subscriptions",
					Before: createRoomClient,
					Action: updateSubscriptions,
					Flags: withDefaultFlags(
						roomFlag,
						identityFlag,
						&cli.StringSliceFlag{
							Name:     "track",
							Usage:    "track sid to subscribe/unsubscribe",
							Required: true,
						},
						&cli.BoolFlag{
							Name:  "subscribe",
							Usage: "set to true to subscribe, otherwise it'll unsubscribe",
						},
					),
				},
				{
					Name:   "send-data",
					Before: createRoomClient,
					Action: sendData,
					Flags: withDefaultFlags(
						roomFlag,
						&cli.StringFlag{
							Name:     "data",
							Usage:    "payload to send to client",
							Required: true,
						},
						&cli.StringFlag{
							Name:  "topic",
							Usage: "topic of the message",
						},
						&cli.StringSliceFlag{
							Name:  "participantID",
							Usage: "list of participantID to send the message to",
						},
					),
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `room create`
			Name:   "create-room",
			Before: createRoomClient,
			Action: createRoom,
			Flags: withDefaultFlags(
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
			),
		},
		{
			Hidden: true, // deprecated: use `room list``
			Name:   "list-rooms",
			Before: createRoomClient,
			Action: listRooms,
			Flags:  withDefaultFlags(),
		},
		{
			Hidden: true, // deprecated: use `room list`
			Name:   "list-room",
			Before: createRoomClient,
			Action: _deprecatedListRoom,
			Flags: withDefaultFlags(
				roomFlag,
			),
		},
		{
			Hidden: true, // deprecated: use `room update-metadata`
			Name:   "update-room-metadata",
			Before: createRoomClient,
			Action: _deprecatedUpdateRoomMetadata,
			Flags: withDefaultFlags(
				roomFlag,
				&cli.StringFlag{
					Name: "metadata",
				},
			),
		},
	}

	roomClient *lksdk.RoomServiceClient
)

func createRoomClient(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	roomClient = lksdk.NewRoomServiceClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil
}

func createRoom(ctx context.Context, cmd *cli.Command) error {
	name, err := extractArg(cmd)
	if err != nil {
		return err
	}

	req := &livekit.CreateRoomRequest{
		Name: *name,
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
		req.Agent = agent
	}

	if roomConfig := cmd.String("room-configuration"); roomConfig != "" {
		req.ConfigName = roomConfig
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

	room, err := roomClient.CreateRoom(context.Background(), req)
	if err != nil {
		return err
	}

	PrintJSON(room)
	return nil
}

func listRooms(ctx context.Context, cmd *cli.Command) error {
	names, _ := extractArgs(cmd)
	req := livekit.ListRoomsRequest{}
	if len(names) > 0 {
		req.Names = names
	}

	res, err := roomClient.ListRooms(context.Background(), &req)
	if err != nil {
		return err
	}
	if len(res.Rooms) == 0 {
		if len(names) > 0 {
			fmt.Printf(
				"there are no rooms matching %s",
				strings.Join(mapStrings(names, wrapWith("\"")), ", "),
			)
		} else {
			fmt.Println("there are no active rooms")
		}
	}
	for _, rm := range res.Rooms {
		fmt.Printf("%s\t%s\t%d participants\n", rm.Sid, rm.Name, rm.NumParticipants)
	}
	return nil
}

func _deprecatedListRoom(ctx context.Context, cmd *cli.Command) error {
	res, err := roomClient.ListRooms(context.Background(), &livekit.ListRoomsRequest{
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
	PrintJSON(rm)
	return nil
}

func deleteRoom(ctx context.Context, cmd *cli.Command) error {
	roomId, err := extractArg(cmd)
	if err != nil {
		return err
	}

	_, err = roomClient.DeleteRoom(context.Background(), &livekit.DeleteRoomRequest{
		Room: *roomId,
	})
	if err != nil {
		return err
	}

	fmt.Println("deleted room", roomId)
	return nil
}

func updateRoomMetadata(ctx context.Context, cmd *cli.Command) error {
	roomName, _ := extractArg(cmd)
	res, err := roomClient.UpdateRoomMetadata(context.Background(), &livekit.UpdateRoomMetadataRequest{
		Room:     *roomName,
		Metadata: cmd.String("metadata"),
	})
	if err != nil {
		return err
	}

	fmt.Println("Updated room metadata")
	PrintJSON(res)
	return nil
}

func _deprecatedUpdateRoomMetadata(ctx context.Context, cmd *cli.Command) error {
	roomName := cmd.String("room")
	res, err := roomClient.UpdateRoomMetadata(context.Background(), &livekit.UpdateRoomMetadataRequest{
		Room:     roomName,
		Metadata: cmd.String("metadata"),
	})
	if err != nil {
		return err
	}

	fmt.Println("Updated room metadata")
	PrintJSON(res)
	return nil
}

func listParticipants(ctx context.Context, cmd *cli.Command) error {
	roomName, err := extractArg(cmd)
	if err != nil {
		return err
	}

	res, err := roomClient.ListParticipants(context.Background(), &livekit.ListParticipantsRequest{
		Room: *roomName,
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
	res, err := roomClient.ListParticipants(context.Background(), &livekit.ListParticipantsRequest{
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
	roomName, identity := participantInfoFromCli(cmd)
	res, err := roomClient.GetParticipant(context.Background(), &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	if err != nil {
		return err
	}

	PrintJSON(res)

	return nil
}

func updateParticipant(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromCli(cmd)
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
	PrintJSON(req)
	if _, err := roomClient.UpdateParticipant(ctx, req); err != nil {
		return err
	}
	fmt.Println("participant updated.")

	return nil
}

func removeParticipant(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromCli(cmd)
	_, err := roomClient.RemoveParticipant(context.Background(), &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	if err != nil {
		return err
	}

	fmt.Println("successfully removed participant", identity)

	return nil
}

func muteTrack(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromCli(cmd)
	trackSid := cmd.String("track")
	_, err := roomClient.MutePublishedTrack(context.Background(), &livekit.MuteRoomTrackRequest{
		Room:     roomName,
		Identity: identity,
		TrackSid: trackSid,
		Muted:    cmd.Bool("muted"),
	})
	if err != nil {
		return err
	}

	verb := "muted"
	if !cmd.Bool("muted") {
		verb = "unmuted"
	}
	fmt.Println(verb, "track: ", trackSid)
	return nil
}

func updateSubscriptions(ctx context.Context, cmd *cli.Command) error {
	roomName, identity := participantInfoFromCli(cmd)
	trackSids := cmd.StringSlice("track")
	_, err := roomClient.UpdateSubscriptions(context.Background(), &livekit.UpdateSubscriptionsRequest{
		Room:      roomName,
		Identity:  identity,
		TrackSids: trackSids,
		Subscribe: cmd.Bool("subscribe"),
	})
	if err != nil {
		return err
	}

	verb := "subscribed to"
	if !cmd.Bool("subscribe") {
		verb = "unsubscribed from"
	}
	fmt.Println(verb, "tracks: ", trackSids)
	return nil
}

func sendData(ctx context.Context, cmd *cli.Command) error {
	roomName, _ := participantInfoFromCli(cmd)
	pIDs := cmd.StringSlice("participantID")
	data := cmd.String("data")
	topic := cmd.String("topic")
	req := &livekit.SendDataRequest{
		Room:            roomName,
		Data:            []byte(data),
		DestinationSids: pIDs,
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

func participantInfoFromCli(c *cli.Command) (string, string) {
	return c.String("room"), c.String("identity")
}
