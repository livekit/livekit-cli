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
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const (
	usageCreate   = "Ability to create or delete rooms"
	usageList     = "Ability to list rooms"
	usageJoin     = "Ability to join a room (requires --room and --identity)"
	usageAdmin    = "Ability to moderate a room (requires --room)"
	usageEgress   = "Ability to interact with Egress services"
	usageIngress  = "Ability to interact with Ingress services"
	usageMetadata = "Ability to update their own name and metadata"
)

var (
	TokenCommands = []*cli.Command{
		{
			Name:   "token",
			Usage:  "Create access tokens with granular capabilities",
			Before: loadProjectConfig,
			Commands: []*cli.Command{
				{
					Name:   "create",
					Usage:  "Creates an access token",
					Action: createToken,
					Flags: []cli.Flag{
						optional(roomFlag),
						optional(identityFlag),
						openFlag,

						&cli.BoolFlag{
							Name:  "create",
							Usage: usageCreate,
						},
						&cli.BoolFlag{
							Name:  "list",
							Usage: usageList,
						},
						&cli.BoolFlag{
							Name:  "join",
							Usage: usageJoin,
						},
						&cli.BoolFlag{
							Name:  "admin",
							Usage: usageAdmin,
						},
						&cli.BoolFlag{
							Name:  "egress",
							Usage: usageEgress,
						},
						&cli.BoolFlag{
							Name:  "ingress",
							Usage: usageIngress,
						},
						&cli.BoolFlag{
							Name:  "allow-update-metadata",
							Usage: usageMetadata,
						},
						&cli.StringSliceFlag{
							Name:  "allow-source",
							Usage: "Restrict publishing to only `SOURCE` types (e.g. --allow-source camera,microphone), defaults to all",
						},
						&TemplateStringFlag{
							Name:    "name",
							Aliases: []string{"n"},
							Usage:   "`NAME` of the participant, used with --join (defaults to identity) (supports templates)",
						},
						&cli.StringFlag{
							Name:  "metadata",
							Usage: "`JSON` metadata to encode in the token, will be passed to participant",
						},
						&cli.StringFlag{
							Name:  "valid-for",
							Usage: "`TIME` that the token is valid for, e.g. \"5m\", \"1h10m\" (s: seconds, m: minutes, h: hours)",
							Value: "5m",
						},
						&cli.StringFlag{
							Name:  "grant",
							Usage: "Additional `VIDEO_GRANT` fields. It'll be merged with other arguments (JSON formatted)",
						},
						&cli.StringFlag{
							Name:  "agent",
							Usage: "Agent to dispatch to the room (identified by agent_name)",
						},
						&cli.StringFlag{
							Name:  "job-metadata",
							Usage: "Metadata attached to job dispatched to the agent (ctx.job.metadata)",
						},
					},
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `token create`
			Name:   "create-token",
			Usage:  "Creates an access token",
			Action: createToken,
			Flags: []cli.Flag{
				optional(roomFlag),

				&cli.BoolFlag{
					Name:  "create",
					Usage: usageCreate,
				},
				&cli.BoolFlag{
					Name:  "list",
					Usage: usageList,
				},
				&cli.BoolFlag{
					Name:  "join",
					Usage: usageJoin,
				},
				&cli.BoolFlag{
					Name:  "admin",
					Usage: usageAdmin,
				},
				&cli.BoolFlag{
					Name:  "recorder",
					Usage: "UNUSED",
				},
				&cli.BoolFlag{
					Name:  "egress",
					Usage: usageEgress,
				},
				&cli.BoolFlag{
					Name:  "ingress",
					Usage: usageIngress,
				},
				&cli.BoolFlag{
					Name:  "allow-update-metadata",
					Usage: usageMetadata,
				},
				&cli.StringSliceFlag{
					Name:  "allow-source",
					Usage: "Allow one or more `SOURCE`s to be published (i.e. --allow-source camera,microphone). if left blank, all sources are allowed",
				},
				&cli.StringFlag{
					Name:    "identity",
					Aliases: []string{"i"},
					Usage:   "Unique `ID` of the participant, used with --join",
				},
				&cli.StringFlag{
					Name:    "name",
					Aliases: []string{"n"},
					Usage:   "`NAME` of the participant, used with --join. defaults to identity",
				},
				&cli.StringFlag{
					Name:  "room-configuration",
					Usage: "name of the room configuration to use when creating a room",
				},
				&cli.StringFlag{
					Name:  "metadata",
					Usage: "`JSON` metadata to encode in the token, will be passed to participant",
				},
				&cli.StringFlag{
					Name:  "valid-for",
					Usage: "Amount of `TIME` that the token is valid for. i.e. \"5m\", \"1h10m\" (s: seconds, m: minutes, h: hours)",
					Value: "5m",
				},
				&cli.StringFlag{
					Name:  "grant",
					Usage: "Additional `VIDEO_GRANT` fields. It'll be merged with other arguments (JSON formatted)",
				},
			},
		},
	}
)

func createToken(ctx context.Context, c *cli.Command) error {
	name := c.String("name")
	metadata := c.String("metadata")
	validFor := c.String("valid-for")
	roomPreset := c.String("room-preset")

	// required only for join, will be generated if not provided
	participant := c.String("identity")
	if participant == "" {
		participant = util.ExpandTemplate("participant-%x")
		fmt.Printf("Using generated participant identity [%s]\n", util.Accented(participant))
	}

	room := c.String("room")
	if room == "" {
		room = util.ExpandTemplate("room-%t")
		fmt.Printf("Using generated room name [%s]\n", util.Accented(room))
	}

	grant := &auth.VideoGrant{
		Room: room,
	}
	hasPerms := false
	if c.Bool("create") {
		grant.RoomCreate = true
		hasPerms = true
	}
	if c.Bool("join") {
		grant.RoomJoin = true
		hasPerms = true
	}
	if c.Bool("admin") {
		grant.RoomAdmin = true
		hasPerms = true
	}
	if c.Bool("list") {
		grant.RoomList = true
		hasPerms = true
	}
	// in the future, this will change to more room specific permissions
	if c.Bool("egress") {
		grant.RoomRecord = true
		hasPerms = true
	}
	if c.Bool("ingress") {
		grant.IngressAdmin = true
		hasPerms = true
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
		hasPerms = true
	}

	if !hasPerms {
		type permission uint

		const (
			pCreate permission = iota
			pList
			pJoin
			pAdmin
			pEgress
			pIngress
			pMetadata
		)

		permissions := make([]permission, 0)

		if err := huh.NewForm(
			huh.NewGroup(huh.NewMultiSelect[permission]().
				Options(
					huh.NewOption("Create", pCreate),
					huh.NewOption("List", pList),
					huh.NewOption("Join", pJoin),
					huh.NewOption("Admin", pAdmin),
					huh.NewOption("Egress", pEgress),
					huh.NewOption("Ingress", pIngress),
					huh.NewOption("Update metadata", pMetadata),
				).
				Title("Token Permissions").
				Description("See https://docs.livekit.io/home/get-started/authentication/#Video-grant").
				Value(&permissions).
				WithTheme(util.Theme))).
			Run(); err != nil || len(permissions) == 0 {
			return errors.New("no permissions were given in this grant, see --help")
		} else {
			grant.RoomCreate = slices.Contains(permissions, pCreate)
			if slices.Contains(permissions, pJoin) {
				grant.RoomJoin = true
			}
			grant.RoomAdmin = slices.Contains(permissions, pAdmin)
			grant.RoomList = slices.Contains(permissions, pList)
			if slices.Contains(permissions, pEgress) {
				grant.RoomRecord = true
			}
			grant.SetCanUpdateOwnMetadata(slices.Contains(permissions, pMetadata))
		}
	}

	_, err := requireProjectWithOpts(ctx, c, ignoreURL)
	if err != nil {
		return err
	}

	at := accessToken(project.APIKey, project.APISecret, grant, participant)

	if grant.RoomJoin {
		if agent := c.String("agent"); agent != "" {
			jobMetadata := c.String("job-metadata")
			at.SetRoomConfig(&livekit.RoomConfiguration{
				Agents: []*livekit.RoomAgentDispatch{
					{
						AgentName: agent,
						Metadata:  jobMetadata,
					},
				},
			})
		}
	}
	if metadata != "" {
		at.SetMetadata(metadata)
	}
	if roomPreset != "" {
		at.SetRoomPreset(roomPreset)
	}
	if name == "" {
		name = participant
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

	fmt.Println("Token grants:")
	util.PrintJSON(grant)
	fmt.Println()
	if project.URL != "" {
		fmt.Println("Project URL:", project.URL)
	}
	fmt.Println("Access token:", token)

	if c.IsSet("open") {
		switch c.String("open") {
		case string(util.OpenTargetMeet):
			_ = util.OpenInMeet(project.URL, token)
		}
	}

	return nil
}

func accessToken(apiKey, apiSecret string, grant *auth.VideoGrant, identity string) *auth.AccessToken {
	if apiKey == "" && apiSecret == "" {
		// not provided, don't sign request
		return nil
	}
	at := auth.NewAccessToken(apiKey, apiSecret).
		SetVideoGrant(grant).
		SetIdentity(identity)
	return at
}
