package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ggwhite/go-masker"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/browser"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

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
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "RoomCompositeEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Name:     "start-track-composite-egress",
			Usage:    "Start track composite egress",
			Before:   createEgressClient,
			Action:   startTrackCompositeEgress,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackCompositeEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Name:     "start-track-egress",
			Usage:    "Start track egress",
			Before:   createEgressClient,
			Action:   startTrackEgress,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "TrackEgressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Name:     "list-egress",
			Usage:    "List all active egress",
			Before:   createEgressClient,
			Action:   listEgress,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "room",
					Usage:    "limits list to a certain room name",
					Required: false,
				},
			},
		},
		{
			Name:     "update-layout",
			Usage:    "Updates layout for a live room composite egress",
			Before:   createEgressClient,
			Action:   updateLayout,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
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
			Name:     "update-stream",
			Usage:    "Adds or removes rtmp output urls from a live stream",
			Before:   createEgressClient,
			Action:   updateStream,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
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
			Name:     "stop-egress",
			Usage:    "Stop egress",
			Before:   createEgressClient,
			Action:   stopEgress,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Egress ID",
					Required: true,
				},
			},
		},
		{
			Name:     "test-egress-template",
			Usage:    "See what your egress template will look like in a recording",
			Category: egressCategory,
			Action:   testEgressTemplate,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
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
			},
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
	url := c.String("url")
	apiKey := c.String("api-key")
	apiSecret := c.String("api-secret")

	if c.Bool("verbose") {
		fmt.Printf("creating client to %s, with api-key: %s, secret: %s\n",
			url,
			masker.ID(apiKey),
			masker.ID(apiSecret))
	}

	egressClient = lksdk.NewEgressClient(url, apiKey, apiSecret)
	return nil
}

func startRoomCompositeEgress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.RoomCompositeEgressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := egressClient.StartRoomCompositeEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackCompositeEgress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.TrackCompositeEgressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := egressClient.StartTrackCompositeEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func startTrackEgress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.TrackEgressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := egressClient.StartTrackEgress(context.Background(), req)
	if err != nil {
		return err
	}

	printInfo(info)
	return nil
}

func listEgress(c *cli.Context) error {
	res, err := egressClient.ListEgress(context.Background(), &livekit.ListEgressRequest{
		RoomName: c.String("room"),
	})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"EgressID", "Status", "Started At", "Error"})
	for _, item := range res.Items {
		var startedAt string
		if item.StartedAt != 0 {
			startedAt = fmt.Sprint(time.Unix(0, item.StartedAt))
		}

		table.Append([]string{
			item.EgressId,
			item.Status.String(),
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
	info, err := egressClient.StopEgress(context.Background(), &livekit.StopEgressRequest{
		EgressId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printInfo(info)
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

	serverURL := c.String("url")
	apiKey := c.String("api-key")
	apiSecret := c.String("api-secret")

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

		err := lt.Start(1)
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

	<-done

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
