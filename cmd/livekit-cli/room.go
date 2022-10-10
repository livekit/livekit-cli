package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

const roomCategory = "Room Server API"

var (
	RoomCommands = []*cli.Command{
		{
			Name:     "create-room",
			Before:   createRoomClient,
			Action:   createRoom,
			Category: roomCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "name",
					Usage:    "name of the room",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "room-egress-file",
					Usage:    "RoomCompositeRequest json file (see examples/room-composite-file.json)",
					Required: false,
				},
				&cli.StringFlag{
					Name:     "track-egress-file",
					Usage:    "AutoTrackEgress json file (see examples/auto-track-egress.json)",
					Required: false,
				},
			),
		},
		{
			Name:     "list-rooms",
			Before:   createRoomClient,
			Action:   listRooms,
			Category: roomCategory,
			Flags:    withDefaultFlags(),
		},
		{
			Name:     "delete-room",
			Before:   createRoomClient,
			Action:   deleteRoom,
			Category: roomCategory,
			Flags: withDefaultFlags(
				roomFlag,
			),
		},
		{
			Name:     "update-room-metadata",
			Before:   createRoomClient,
			Action:   updateRoomMetadata,
			Category: roomCategory,
			Flags: withDefaultFlags(
				roomFlag,
				&cli.StringFlag{
					Name: "metadata",
				},
			),
		},
		{
			Name:     "list-participants",
			Before:   createRoomClient,
			Action:   listParticipants,
			Category: roomCategory,
			Flags: withDefaultFlags(
				roomFlag,
			),
		},
		{
			Name:     "get-participant",
			Before:   createRoomClient,
			Action:   getParticipant,
			Category: roomCategory,
			Flags: withDefaultFlags(
				roomFlag,
				identityFlag,
			),
		},
		{
			Name:     "remove-participant",
			Before:   createRoomClient,
			Action:   removeParticipant,
			Category: roomCategory,
			Flags: withDefaultFlags(
				roomFlag,
				identityFlag,
			),
		},
		{
			Name:     "update-participant",
			Before:   createRoomClient,
			Action:   updateParticipant,
			Category: roomCategory,
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
			Name:     "mute-track",
			Before:   createRoomClient,
			Action:   muteTrack,
			Category: roomCategory,
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
			Name:     "update-subscriptions",
			Before:   createRoomClient,
			Action:   updateSubscriptions,
			Category: roomCategory,
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
	}

	roomClient *lksdk.RoomServiceClient
)

func createRoomClient(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	roomClient = lksdk.NewRoomServiceClient(pc.URL, pc.APIKey, pc.APISecret)
	return nil
}

func createRoom(c *cli.Context) error {
	req := &livekit.CreateRoomRequest{
		Name: c.String("name"),
	}

	if roomEgressFile := c.String("room-egress-file"); roomEgressFile != "" {
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

	if trackEgressFile := c.String("track-egress-file"); trackEgressFile != "" {
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

	room, err := roomClient.CreateRoom(context.Background(), req)
	if err != nil {
		return err
	}

	PrintJSON(room)
	return nil
}

func listRooms(c *cli.Context) error {
	res, err := roomClient.ListRooms(context.Background(), &livekit.ListRoomsRequest{})
	if err != nil {
		return err
	}
	if len(res.Rooms) == 0 {
		fmt.Println("there are no active rooms")
	}
	for _, rm := range res.Rooms {
		fmt.Printf("%s\t%s\t%d participants\n", rm.Sid, rm.Name, rm.NumParticipants)
	}
	return nil
}

func deleteRoom(c *cli.Context) error {
	roomId := c.String("room")
	_, err := roomClient.DeleteRoom(context.Background(), &livekit.DeleteRoomRequest{
		Room: roomId,
	})
	if err != nil {
		return err
	}

	fmt.Println("deleted room", roomId)
	return nil
}

func updateRoomMetadata(c *cli.Context) error {
	roomName := c.String("room")
	res, err := roomClient.UpdateRoomMetadata(context.Background(), &livekit.UpdateRoomMetadataRequest{
		Room:     roomName,
		Metadata: c.String("metadata"),
	})
	if err != nil {
		return err
	}

	fmt.Println("Updated room metadata")
	PrintJSON(res)
	return nil
}

func listParticipants(c *cli.Context) error {
	roomName := c.String("room")
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

func getParticipant(c *cli.Context) error {
	roomName, identity := participantInfoFromCli(c)
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

func updateParticipant(c *cli.Context) error {
	roomName, identity := participantInfoFromCli(c)
	metadata := c.String("metadata")
	permissions := c.String("permissions")
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
		participant, err := roomClient.GetParticipant(c.Context, &livekit.RoomParticipantIdentity{
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
	if _, err := roomClient.UpdateParticipant(c.Context, req); err != nil {
		return err
	}
	fmt.Println("participant updated.")

	return nil
}

func removeParticipant(c *cli.Context) error {
	roomName, identity := participantInfoFromCli(c)
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

func muteTrack(c *cli.Context) error {
	roomName, identity := participantInfoFromCli(c)
	trackSid := c.String("track")
	_, err := roomClient.MutePublishedTrack(context.Background(), &livekit.MuteRoomTrackRequest{
		Room:     roomName,
		Identity: identity,
		TrackSid: trackSid,
		Muted:    c.Bool("muted"),
	})
	if err != nil {
		return err
	}

	verb := "muted"
	if !c.Bool("muted") {
		verb = "unmuted"
	}
	fmt.Println(verb, "track: ", trackSid)
	return nil
}

func updateSubscriptions(c *cli.Context) error {
	roomName, identity := participantInfoFromCli(c)
	trackSids := c.StringSlice("track")
	_, err := roomClient.UpdateSubscriptions(context.Background(), &livekit.UpdateSubscriptionsRequest{
		Room:      roomName,
		Identity:  identity,
		TrackSids: trackSids,
		Subscribe: c.Bool("subscribe"),
	})
	if err != nil {
		return err
	}

	verb := "subscribed to"
	if !c.Bool("subscribe") {
		verb = "unsubscribed from"
	}
	fmt.Println(verb, "tracks: ", trackSids)
	return nil
}

func participantInfoFromCli(c *cli.Context) (string, string) {
	return c.String("room"), c.String("identity")
}
