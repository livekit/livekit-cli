package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/livekit/protocol/auth"
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
			},
		},
	}
)

func createToken(c *cli.Context) error {
	if !c.IsSet("api-key") || !c.IsSet("api-secret") {
		return fmt.Errorf("api-key and api-secret are required")
	}
	p := c.String("identity") // required only for join
	name := c.String("name")
	room := c.String("room")
	metadata := c.String("metadata")
	validFor := c.String("valid-for")

	grant := &auth.VideoGrant{}
	if c.Bool("create") {
		grant.RoomCreate = true
	}
	if c.Bool("join") {
		grant.RoomJoin = true
		grant.Room = room
		if p == "" {
			return fmt.Errorf("participant identity is required")
		}
	}
	if c.Bool("admin") {
		grant.RoomAdmin = true
		grant.Room = room
	}
	if c.Bool("list") {
		grant.RoomList = true
	}

	if str := c.String("grant"); str != "" {
		if err := json.Unmarshal([]byte(str), grant); err != nil {
			return err
		}
	}

	if !grant.RoomJoin && !grant.RoomCreate && !grant.RoomAdmin && !grant.RoomList {
		return fmt.Errorf("at least one of --list, --join, --create, or --admin is required")
	}

	at := accessToken(c, grant, p)

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

func accessToken(c *cli.Context, grant *auth.VideoGrant, identity string) *auth.AccessToken {
	apiKey := c.String("api-key")
	apiSecret := c.String("api-secret")
	if apiKey == "" && apiSecret == "" {
		// not provided, don't sign request
		return nil
	}
	at := auth.NewAccessToken(apiKey, apiSecret).
		AddGrant(grant).
		SetIdentity(identity)
	return at
}
