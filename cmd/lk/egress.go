// Copyright 2022-2024 LiveKit, Inc.
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
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/browser"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/protocol/egress"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/livekit/livekit-cli/v2/pkg/loadtester"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

type egressType string

const (
	EgressTypeRoomComposite  egressType = "room-composite"
	EgressTypeParticipant    egressType = "participant"
	EgressTypeTrack          egressType = "track"
	EgressTypeTrackComposite egressType = "track-composite"
	EgressTypeWeb            egressType = "web"
)

var (
	egressStartDescription = `Initiates a new egress of the chosen TYPE:
	- "room-composite" composes multiple participant tracks into a single output stream
	- "participant" captures a single participant
	- "track" captures a single track without transcoding
	- "track-composite" captures an audio and a video track
	- "web" captures any website, with a lifecycle detached from LiveKit rooms

REQUEST_JSON is one of:
	- ` + reflect.TypeFor[livekit.RoomCompositeEgressRequest]().Name() + `
	- ` + reflect.TypeFor[livekit.ParticipantEgressRequest]().Name() + `
	- ` + reflect.TypeFor[livekit.TrackEgressRequest]().Name() + `
	- ` + reflect.TypeFor[livekit.TrackCompositeEgressRequest]().Name() + `
	- ` + reflect.TypeFor[livekit.WebEgressRequest]().Name() + `
	
See cmd/livekit-cli/examples`
)

var (
	EgressCommands = []*cli.Command{
		{
			Name:  "egress",
			Usage: "Record or stream media from LiveKit to elsewhere",
			Commands: []*cli.Command{
				{
					Name:        "start",
					Usage:       "Start egresses of various types",
					Description: egressStartDescription,
					Before:      createEgressClient,
					Action:      handleEgressStart,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "type",
							Usage: "Specify `TYPE` of egress (see above)",
							Value: string(EgressTypeRoomComposite),
						},
					},
					ArgsUsage: "REQUEST_JSON",
				},
				{
					Name:   "list",
					Usage:  "List and search active egresses",
					Before: createEgressClient,
					Action: listEgress,
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:  "id",
							Usage: "List a specific egress `ID`, can be used multiple times",
						},
						&cli.StringFlag{
							Name:  "room",
							Usage: "Limits list to a certain room `NAME`",
						},
						&cli.BoolFlag{
							Name:    "active",
							Aliases: []string{"a"},
							Usage:   "Lists only active egresses",
						},
						jsonFlag,
					},
				},
				{
					Name:   "stop",
					Usage:  "Stop an active egress",
					Before: createEgressClient,
					Action: stopEgress,
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:     "id",
							Usage:    "Egress ID to stop, can be specified multiple times",
							Required: true,
						},
					},
				},
				{
					Name:   "test-template",
					Usage:  "See what your egress template will look like in a recording",
					Action: testEgressTemplate,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "base-url (e.g. https://recorder.livekit.io/#)",
							Usage:    "Base template `URL`",
							Required: true,
						},
						&cli.StringFlag{
							Name:  "layout",
							Usage: "Layout `TYPE`",
						},
						&cli.IntFlag{
							Name:     "publishers",
							Usage:    "`NUMBER` of publishers",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "room",
							Usage:    "`NAME` of the room",
							Required: false,
						},
					},
				},
				{
					Name:      "update-layout",
					Usage:     "Updates layout for a live room composite egress",
					ArgsUsage: "ID",
					Before:    createEgressClient,
					Action:    updateLayout,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Egress ID",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "layout",
							Usage:    "new web layout",
							Required: true,
						},
					},
				},
				{
					Name:   "update-stream",
					Usage:  "Adds or removes RTMP output urls from a live stream",
					Before: createEgressClient,
					Action: updateStream,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "id",
							Usage:    "Egress ID",
							Required: true,
						},
						&cli.StringSliceFlag{
							Name:     "add-urls",
							Usage:    "urls to add",
							Required: false,
						},
						&cli.StringSliceFlag{
							Name:     "remove-urls",
							Usage:    "urls to remove",
							Required: false,
						},
					},
				},
			},
			HideHelpCommand: true,
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `egress start --room-composite`
			Name:   "start-room-composite-egress",
			Usage:  "Start room composite egress",
			Before: createEgressClient,
			Action: _deprecatedStartRoomCompositeEgress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    RequestDesc[livekit.RoomCompositeEgressRequest](),
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress start --web`
			Name:   "start-web-egress",
			Usage:  "Start web egress",
			Before: createEgressClient,
			Action: _deprecatedStartWebEgress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "WebEgressRequest as json file (see cmd/livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress start --participant`
			Name:   "start-participant-egress",
			Usage:  "Start participant egress",
			Before: createEgressClient,
			Action: _deprecatedStartParticipantEgress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "ParticipantEgressRequest as json file (see cmd/livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress start --track-composite`
			Name:   "start-track-composite-egress",
			Usage:  "Start track composite egress",
			Before: createEgressClient,
			Action: _deprecatedStartTrackCompositeEgress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackCompositeEgressRequest as json file (see cmd/livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress start --track`
			Name:   "start-track-egress",
			Usage:  "Start track egress",
			Before: createEgressClient,
			Action: _deprecatedStartTrackEgress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackEgressRequest as json file (see cmd/livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress list`
			Name:   "list-egress",
			Usage:  "List all active egress",
			Before: createEgressClient,
			Action: listEgress,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:  "id",
					Usage: "list a specific egress id, can be used multiple times",
				},
				&cli.StringFlag{
					Name:  "room",
					Usage: "limits list to a certain room name",
				},
				&cli.BoolFlag{
					Name:  "active",
					Usage: "lists only active egresses",
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress update-layout`
			Name:   "update-layout",
			Usage:  "Updates layout for a live room composite egress",
			Before: createEgressClient,
			Action: updateLayout,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Egress ID",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "layout",
					Usage:    "new web layout",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress update-stream`
			Name:   "update-stream",
			Usage:  "Adds or removes rtmp output urls from a live stream",
			Before: createEgressClient,
			Action: updateStream,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Egress ID",
					Required: true,
				},
				&cli.StringSliceFlag{
					Name:     "add-urls",
					Usage:    "urls to add",
					Required: false,
				},
				&cli.StringSliceFlag{
					Name:     "remove-urls",
					Usage:    "urls to remove",
					Required: false,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress stop`
			Name:   "stop-egress",
			Usage:  "Stop egress",
			Before: createEgressClient,
			Action: stopEgress,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:     "id",
					Usage:    "Egress ID to stop, can be specified multiple times",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `egress test-template`
			Name:   "test-egress-template",
			Usage:  "See what your egress template will look like in a recording",
			Action: testEgressTemplate,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "base-url (e.g. https://recorder.livekit.io/#)",
					Usage:    "Base template `URL`",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "layout",
					Usage: "Layout `TYPE`",
				},
				&cli.IntFlag{
					Name:     "publishers",
					Usage:    "`NUMBER` of publishers",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "room",
					Usage:    "`NAME` of the room",
					Required: false,
				},
			},
			SkipFlagParsing:        false,
			HideHelp:               false,
			HideHelpCommand:        false,
			UseShortOptionHandling: false,
			CustomHelpTemplate:     "",
		},
	}

	egressClient *lksdk.EgressClient
)

func createEgressClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}

	egressClient = lksdk.NewEgressClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil, nil
}

func handleEgressStart(ctx context.Context, cmd *cli.Command) error {
	switch cmd.String("type") {
	case string(EgressTypeRoomComposite):
		return startRoomCompositeEgress(ctx, cmd)
	case string(EgressTypeWeb):
		return startWebEgress(ctx, cmd)
	case string(EgressTypeParticipant):
		return startParticipantEgress(ctx, cmd)
	case string(EgressTypeTrack):
		return startTrackEgress(ctx, cmd)
	case string(EgressTypeTrackComposite):
		return startTrackCompositeEgress(ctx, cmd)
	default:
		return errors.New("unrecognized egress type " + util.WrapWith("\"")(cmd.String("type")))
	}
}

func startRoomCompositeEgress(ctx context.Context, cmd *cli.Command) error {
	_ = ctx
	req, err := ReadRequestArg[livekit.RoomCompositeEgressRequest](cmd)
	if err != nil {
		return err
	}

	info, err := egressClient.StartRoomCompositeEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func _deprecatedStartRoomCompositeEgress(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.RoomCompositeEgressRequest{}
	if err := unmarshalEgressRequest(cmd, req); err != nil {
		return err
	}

	info, err := egressClient.StartRoomCompositeEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startWebEgress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArg[livekit.WebEgressRequest](cmd)
	if err != nil {
		return err
	}

	info, err := egressClient.StartWebEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func _deprecatedStartWebEgress(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.WebEgressRequest{}
	if err := unmarshalEgressRequest(cmd, req); err != nil {
		return err
	}

	info, err := egressClient.StartWebEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startParticipantEgress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArg[livekit.ParticipantEgressRequest](cmd)
	if err != nil {
		return err
	}

	info, err := egressClient.StartParticipantEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func _deprecatedStartParticipantEgress(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.ParticipantEgressRequest{}
	if err := unmarshalEgressRequest(cmd, req); err != nil {
		return err
	}

	info, err := egressClient.StartParticipantEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackCompositeEgress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArg[livekit.TrackCompositeEgressRequest](cmd)
	if err != nil {
		return err
	}

	info, err := egressClient.StartTrackCompositeEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func _deprecatedStartTrackCompositeEgress(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.TrackCompositeEgressRequest{}
	if err := unmarshalEgressRequest(cmd, req); err != nil {
		return err
	}

	info, err := egressClient.StartTrackCompositeEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackEgress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArg[livekit.TrackEgressRequest](cmd)
	if err != nil {
		return err
	}

	info, err := egressClient.StartTrackEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func _deprecatedStartTrackEgress(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.TrackEgressRequest{}
	if err := unmarshalEgressRequest(cmd, req); err != nil {
		return err
	}

	info, err := egressClient.StartTrackEgress(ctx, req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func unmarshalEgressRequest(cmd *cli.Command, req proto.Message) error {
	reqBytes, err := os.ReadFile(cmd.String("request"))
	if err != nil {
		return err
	}
	if err = protojson.Unmarshal(reqBytes, req); err != nil {
		return err
	}

	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}
	return nil
}

func listEgress(ctx context.Context, cmd *cli.Command) error {
	var items []*livekit.EgressInfo
	if cmd.IsSet("id") {
		for _, id := range cmd.StringSlice("id") {
			res, err := egressClient.ListEgress(ctx, &livekit.ListEgressRequest{
				EgressId: id,
			})
			if err != nil {
				return err
			}
			items = append(items, res.Items...)
		}
	} else {
		res, err := egressClient.ListEgress(ctx, &livekit.ListEgressRequest{
			RoomName: cmd.String("room"),
			Active:   cmd.Bool("active"),
		})
		if err != nil {
			return err
		}
		items = res.Items
	}

	if cmd.Bool("json") {
		util.PrintJSON(items)
	} else {
		table := util.CreateTable().
			Headers("EgressID", "Status", "Type", "Source", "Started At", "Error")
		for _, item := range items {
			var startedAt string
			if item.StartedAt != 0 {
				startedAt = fmt.Sprint(time.Unix(0, item.StartedAt))
			}
			var egressType, egressSource string
			switch req := item.Request.(type) {
			case *livekit.EgressInfo_RoomComposite:
				egressType = "room_composite"
				egressSource = req.RoomComposite.RoomName
			case *livekit.EgressInfo_Web:
				egressType = "web"
				egressSource = req.Web.Url
			case *livekit.EgressInfo_Participant:
				egressType = "participant"
				egressSource = fmt.Sprintf("%s/%s", req.Participant.RoomName, req.Participant.Identity)
			case *livekit.EgressInfo_TrackComposite:
				egressType = "track_composite"
				trackIDs := make([]string, 0)
				if req.TrackComposite.VideoTrackId != "" {
					trackIDs = append(trackIDs, req.TrackComposite.VideoTrackId)
				}
				if req.TrackComposite.AudioTrackId != "" {
					trackIDs = append(trackIDs, req.TrackComposite.AudioTrackId)
				}
				egressSource = fmt.Sprintf("%s/%s", req.TrackComposite.RoomName, strings.Join(trackIDs, ","))
			case *livekit.EgressInfo_Track:
				egressType = "track"
				egressSource = fmt.Sprintf("%s/%s", req.Track.RoomName, req.Track.TrackId)
			}
			table.Row(
				item.EgressId,
				item.Status.String(),
				egressType,
				egressSource,
				startedAt,
				item.Error,
			)
		}
		fmt.Println(table)
	}

	return nil
}

func updateLayout(ctx context.Context, cmd *cli.Command) error {
	egressId := cmd.String("id")
	if egressId == "" {
		egressId = cmd.Args().First()
	}
	info, err := egressClient.UpdateLayout(ctx, &livekit.UpdateLayoutRequest{
		EgressId: egressId,
		Layout:   cmd.String("layout"),
	})
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func updateStream(ctx context.Context, cmd *cli.Command) error {
	egressId := cmd.String("id")
	if egressId == "" {
		egressId = cmd.Args().First()
	}
	info, err := egressClient.UpdateStream(ctx, &livekit.UpdateStreamRequest{
		EgressId:         egressId,
		AddOutputUrls:    cmd.StringSlice("add-urls"),
		RemoveOutputUrls: cmd.StringSlice("remove-urls"),
	})
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func stopEgress(ctx context.Context, cmd *cli.Command) error {
	ids := cmd.StringSlice("id")
	var errors []error
	for _, id := range ids {
		_, err := egressClient.StopEgress(ctx, &livekit.StopEgressRequest{
			EgressId: id,
		})
		if err != nil {
			errors = append(errors, err)
			fmt.Println("Error stopping Egress", id, err)
		} else {
			fmt.Println("Stopping Egress", id)
		}
	}
	if len(errors) != 0 {
		return errors[0]
	}
	return nil
}

func testEgressTemplate(ctx context.Context, cmd *cli.Command) error {
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	numPublishers := int(cmd.Int("publishers"))
	rooms := make([]*lksdk.Room, 0, numPublishers)
	defer func() {
		for _, room := range rooms {
			room.Disconnect()
		}
	}()

	roomName := cmd.String("room")
	if roomName == "" {
		roomName = fmt.Sprintf("layout-demo-%v", time.Now().Unix())
	}

	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	serverURL := pc.URL
	apiKey := pc.APIKey
	apiSecret := pc.APISecret

	var testers []*loadtester.LoadTester
	for i := 0; i < numPublishers; i++ {
		lt := loadtester.NewLoadTester(loadtester.TesterParams{
			URL:            serverURL,
			APIKey:         apiKey,
			APISecret:      apiSecret,
			Room:           roomName,
			IdentityPrefix: "demo-publisher",
			Sequence:       i,
		})

		err := lt.Start()
		if err != nil {
			return err
		}

		testers = append(testers, lt)
		if _, err = lt.PublishSimulcastTrack("demo-video", "high", ""); err != nil {
			return err
		}
	}

	token, err := egress.BuildEgressToken("template_test", apiKey, apiSecret, roomName)
	if err != nil {
		return err
	}

	templateURL := fmt.Sprintf(
		"%s/?url=%s&layout=%s&token=%s",
		cmd.String("base-url"), url.QueryEscape(serverURL), cmd.String("layout"), token,
	)
	if err := browser.OpenURL(templateURL); err != nil {
		return err
	}

	sim := loadtester.NewSpeakerSimulator(loadtester.SpeakerSimulatorParams{
		Testers: testers,
	})
	sim.Start()
	fmt.Println("simulating speakers...")

	<-done

	sim.Stop()
	for _, lt := range testers {
		lt.Stop()
	}
	return nil
}

func printInfo(info *livekit.EgressInfo) {
	if info.Error == "" {
		fmt.Printf("EgressID: %v Status: %v\n", info.EgressId, info.Status)
	} else {
		fmt.Printf("EgressID: %v Error: %v\n", info.EgressId, info.Error)
	}
}
