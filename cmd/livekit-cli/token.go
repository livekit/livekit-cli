package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

var (
	TokenCommands = []*cli.Command{
		{
			Name:     "create-token",
			Usage:    "creates an access token",
			Action:   createToken,
			Category: "Token",
			Flags: []cli.Flag{
				apiKeyFlag,
				secretFlag,
				&cli.BoolFlag{
					Name:  "create",
					Usage: "enable token to be used to create rooms",
				},
				&cli.BoolFlag{
					Name:  "list",
					Usage: "enable token to be used to list rooms",
				},
				&cli.BoolFlag{
					Name:  "join",
					Usage: "enable token to be used to join a room (requires --room and --identity)",
				},
				&cli.BoolFlag{
					Name:  "admin",
					Usage: "enable token to be used to manage a room (requires --room)",
				},
				&cli.BoolFlag{
					Name:  "recorder",
					Usage: "enable token to be used to record a room (requires --room)",
				},
				&cli.StringSliceFlag{
					Name:  "allow-source",
					Usage: "allow one or more sources to be published (i.e. --allow-source camera,microphone). if left blank, all sources are allowed",
				},
				&cli.BoolFlag{
					Name:  "allow-update-metadata",
					Usage: "allow participant to update their own name and metadata from the client side",
				},
				&cli.StringFlag{
					Name:    "identity",
					Aliases: []string{"i"},
					Usage:   "unique identity of the participant, used with --join",
				},
				&cli.StringFlag{
					Name:    "name",
					Aliases: []string{"n"},
					Usage:   "name of the participant, used with --join. defaults to identity",
				},
				&cli.StringFlag{
					Name:    "room",
					Aliases: []string{"r"},
					Usage:   "name of the room to join",
				},
				&cli.StringFlag{
					Name:  "metadata",
					Usage: "JSON metadata to encode in the token, will be passed to participant",
				},
				&cli.StringFlag{
					Name:  "valid-for",
					Usage: "amount of time that the token is valid for. i.e. \"5m\", \"1h10m\" (s: seconds, m: minutes, h: hours)",
					Value: "5m",
				},
				&cli.StringFlag{
					Name:  "grant",
					Usage: "additional VideoGrant fields. It'll be merged with other arguments (JSON formatted)",
				},
				projectFlag,
			},
		},
	}
)

func createToken(c *cli.Context) error {
	p := c.String("identity") // required only for join
	name := c.String("name")
	room := c.String("room")
	metadata := c.String("metadata")
	validFor := c.String("valid-for")

	grant := &auth.VideoGrant{
		Room: room,
	}
	if c.Bool("create") {
		grant.RoomCreate = true
	}
	if c.Bool("join") {
		grant.RoomJoin = true
		if p == "" {
			return errors.New("participant identity is required")
		}
		if room == "" {
			return errors.New("room is required")
		}
	}
	if c.Bool("admin") {
		grant.RoomAdmin = true
	}
	if c.Bool("list") {
		grant.RoomList = true
	}
	if c.Bool("recorder") {
		grant.RoomRecord = true
		grant.Recorder = true
		grant.Hidden = true
	}
	if c.IsSet("allow-source") {
		sourcesStr := c.StringSlice("allow-source")
		sources := make([]livekit.TrackSource, 0, len(sourcesStr))
		for _, s := range sourcesStr {
			var source livekit.TrackSource
			switch s {
			case "camera":
				source = livekit.TrackSource_CAMERA
			case "microphone":
				source = livekit.TrackSource_MICROPHONE
			case "screen_share":
				source = livekit.TrackSource_SCREEN_SHARE
			case "screen_share_audio":
				source = livekit.TrackSource_SCREEN_SHARE_AUDIO
			default:
				return fmt.Errorf("invalid source: %s", s)
			}
			sources = append(sources, source)
		}
		grant.SetCanPublishSources(sources)
	}
	if c.Bool("allow-update-metadata") {
		grant.SetCanUpdateOwnMetadata(true)
	}

	if str := c.String("grant"); str != "" {
		if err := json.Unmarshal([]byte(str), grant); err != nil {
			return err
		}
	}

	if !grant.RoomJoin && !grant.RoomCreate && !grant.RoomAdmin && !grant.RoomList && !grant.RoomRecord {
		return errors.New("at least one of --list, --join, --create, --admin, or --recorder is required. use --grant to set a custom video grant")
	}

	pc, err := loadProjectDetails(c, ignoreURL)
	if err != nil {
		return err
	}

	at := accessToken(pc.APIKey, pc.APISecret, grant, p)

	if metadata != "" {
		at.SetMetadata(metadata)
	}
	if name == "" {
		name = p
	}
	at.SetName(name)
	if validFor != "" {
		if dur, err := time.ParseDuration(validFor); err == nil {
			fmt.Println("valid for (mins): ", int(dur/time.Minute))
			at.SetValidFor(dur)
		} else {
			return err
		}
	}

	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	fmt.Println("token grants")
	PrintJSON(grant)
	fmt.Println()
	fmt.Println("access token: ", token)
	return nil
}

func accessToken(apiKey, apiSecret string, grant *auth.VideoGrant, identity string) *auth.AccessToken {
	if apiKey == "" && apiSecret == "" {
		// not provided, don't sign request
		return nil
	}
	at := auth.NewAccessToken(apiKey, apiSecret).
		AddGrant(grant).
		SetIdentity(identity)
	return at
}
