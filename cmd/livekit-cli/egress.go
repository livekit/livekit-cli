package main

import (
	"context"
	"fmt"
	"io/ioutil"

	"github.com/ggwhite/go-masker"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

const egressCategory = "Egress"

var (
	EgressCommands = []*cli.Command{
		{
			Name:     "start-egress",
			Usage:    "Start egress",
			Before:   createEgressClient,
			Action:   startEgress,
			Category: egressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "StartEgressRequest as json file (see https://github.com/livekit/livekit-recorder#request)",
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
			Usage:    "Updates layout for a live web composite egress",
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

func startEgress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}
	req := &livekit.WebCompositeEgressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	res, err := egressClient.StartWebCompositeEgress(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Printf("Egress started. Egress ID: %s\n", res.EgressId)
	return nil
}

func listEgress(c *cli.Context) error {
	_, err := egressClient.ListEgress(context.Background(), &livekit.ListEgressRequest{
		RoomName: c.String("room"),
	})
	return err
}

func updateLayout(c *cli.Context) error {
	_, err := egressClient.UpdateLayout(context.Background(), &livekit.UpdateLayoutRequest{
		EgressId: c.String("id"),
		Layout:   c.String("layout"),
	})
	return err
}

func updateStream(c *cli.Context) error {
	_, err := egressClient.UpdateStream(context.Background(), &livekit.UpdateStreamRequest{
		EgressId:         c.String("id"),
		AddOutputUrls:    c.StringSlice("add-urls"),
		RemoveOutputUrls: c.StringSlice("remove-urls"),
	})
	return err
}

func stopEgress(c *cli.Context) error {
	_, err := egressClient.StopEgress(context.Background(), &livekit.StopEgressRequest{
		EgressId: c.String("id"),
	})
	return err
}
