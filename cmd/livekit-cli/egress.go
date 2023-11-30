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
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/pkg/browser"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/livekit-cli/pkg/loadtester"
	"github.com/livekit/protocol/egress"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

const egressCategory = "Egress"

var (
	EgressCommands = []*cli.Command{
		{
			Name:     "start-room-composite-egress",
			Usage:    "Start room composite egress",
			Before:   createEgressClient,
			Action:   startRoomCompositeEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "RoomCompositeEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "start-web-egress",
			Usage:    "Start web egress",
			Before:   createEgressClient,
			Action:   startWebEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "WebEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "start-participant-egress",
			Usage:    "Start participant egress",
			Before:   createEgressClient,
			Action:   startParticipantEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "ParticipantEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "start-track-composite-egress",
			Usage:    "Start track composite egress",
			Before:   createEgressClient,
			Action:   startTrackCompositeEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackCompositeEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "start-track-egress",
			Usage:    "Start track egress",
			Before:   createEgressClient,
			Action:   startTrackEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "list-egress",
			Usage:    "List all active egress",
			Before:   createEgressClient,
			Action:   listEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
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
			),
		},
		{
			Name:     "update-layout",
			Usage:    "Updates layout for a live room composite egress",
			Before:   createEgressClient,
			Action:   updateLayout,
			Category: egressCategory,
			Flags: withDefaultFlags(
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
			),
		},
		{
			Name:     "update-stream",
			Usage:    "Adds or removes rtmp output urls from a live stream",
			Before:   createEgressClient,
			Action:   updateStream,
			Category: egressCategory,
			Flags: withDefaultFlags(
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
			),
		},
		{
			Name:     "stop-egress",
			Usage:    "Stop egress",
			Before:   createEgressClient,
			Action:   stopEgress,
			Category: egressCategory,
			Flags: withDefaultFlags(
				&cli.StringSliceFlag{
					Name:     "id",
					Usage:    "Egress ID to stop, can be specified multiple times",
					Required: true,
				},
			),
		},
		{
			Name:     "test-egress-template",
			Usage:    "See what your egress template will look like in a recording",
			Category: egressCategory,
			Action:   testEgressTemplate,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "base-url (e.g. https://recorder.livekit.io/#)",
					Usage:    "base template url",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "layout",
					Usage: "layout name",
				},
				&cli.IntFlag{
					Name:     "publishers",
					Usage:    "number of publishers",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "room",
					Usage:    "name of the room",
					Required: false,
				},
			),
			SkipFlagParsing:        false,
			HideHelp:               false,
			HideHelpCommand:        false,
			Hidden:                 false,
			UseShortOptionHandling: false,
			HelpName:               "",
			CustomHelpTemplate:     "",
		},
	}

	egressClient *lksdk.EgressClient
)

func createEgressClient(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	egressClient = lksdk.NewEgressClient(pc.URL, pc.APIKey, pc.APISecret)
	return nil
}

func startRoomCompositeEgress(c *cli.Context) error {
	req := &livekit.RoomCompositeEgressRequest{}
	if err := unmarshalEgressRequest(c, req); err != nil {
		return err
	}

	info, err := egressClient.StartRoomCompositeEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startWebEgress(c *cli.Context) error {
	req := &livekit.WebEgressRequest{}
	if err := unmarshalEgressRequest(c, req); err != nil {
		return err
	}

	info, err := egressClient.StartWebEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startParticipantEgress(c *cli.Context) error {
	req := &livekit.ParticipantEgressRequest{}
	if err := unmarshalEgressRequest(c, req); err != nil {
		return err
	}

	info, err := egressClient.StartParticipantEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackCompositeEgress(c *cli.Context) error {
	req := &livekit.TrackCompositeEgressRequest{}
	if err := unmarshalEgressRequest(c, req); err != nil {
		return err
	}

	info, err := egressClient.StartTrackCompositeEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackEgress(c *cli.Context) error {
	req := &livekit.TrackEgressRequest{}
	if err := unmarshalEgressRequest(c, req); err != nil {
		return err
	}

	info, err := egressClient.StartTrackEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func unmarshalEgressRequest(c *cli.Context, req proto.Message) error {
	reqFile := c.String("request")
	reqBytes, err := os.ReadFile(reqFile)
	if err != nil {
		return err
	}
	if err = protojson.Unmarshal(reqBytes, req); err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}
	return nil
}

func listEgress(c *cli.Context) error {
	var items []*livekit.EgressInfo
	if c.IsSet("id") {
		for _, id := range c.StringSlice("id") {
			res, err := egressClient.ListEgress(context.Background(), &livekit.ListEgressRequest{
				EgressId: id,
			})
			if err != nil {
				return err
			}
			items = append(items, res.Items...)
		}
	} else {
		res, err := egressClient.ListEgress(context.Background(), &livekit.ListEgressRequest{
			RoomName: c.String("room"),
			Active:   c.Bool("active"),
		})
		if err != nil {
			return err
		}
		items = res.Items
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"EgressID", "Status", "Type", "Source", "Started At", "Error"})
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

		table.Append([]string{
			item.EgressId,
			item.Status.String(),
			egressType,
			egressSource,
			startedAt,
			item.Error,
		})
	}
	table.Render()

	return nil
}

func updateLayout(c *cli.Context) error {
	info, err := egressClient.UpdateLayout(context.Background(), &livekit.UpdateLayoutRequest{
		EgressId: c.String("id"),
		Layout:   c.String("layout"),
	})
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func updateStream(c *cli.Context) error {
	info, err := egressClient.UpdateStream(context.Background(), &livekit.UpdateStreamRequest{
		EgressId:         c.String("id"),
		AddOutputUrls:    c.StringSlice("add-urls"),
		RemoveOutputUrls: c.StringSlice("remove-urls"),
	})
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func stopEgress(c *cli.Context) error {
	ids := c.StringSlice("id")
	var errors []error
	for _, id := range ids {
		_, err := egressClient.StopEgress(context.Background(), &livekit.StopEgressRequest{
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

func testEgressTemplate(c *cli.Context) error {
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	numPublishers := c.Int("publishers")
	rooms := make([]*lksdk.Room, 0, numPublishers)
	defer func() {
		for _, room := range rooms {
			room.Disconnect()
		}
	}()

	roomName := c.String("room")
	if roomName == "" {
		roomName = fmt.Sprintf("layout-demo-%v", time.Now().Unix())
	}

	pc, err := loadProjectDetails(c)
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
		c.String("base-url"), url.QueryEscape(serverURL), c.String("layout"), token,
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
