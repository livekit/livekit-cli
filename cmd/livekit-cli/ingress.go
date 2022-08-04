package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/ggwhite/go-masker"
	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

const ingressCategory = "Ingress"

var (
	IngressCommands = []*cli.Command{
		{
			Name:     "create-ingress",
			Usage:    "Create an ingress",
			Before:   createIngressClient,
			Action:   createIngress,
			Category: ingressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateIngressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Name:     "update-ingress",
			Usage:    "Update an ingress",
			Before:   createIngressClient,
			Action:   updateIngress,
			Category: ingressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "UpdateIngressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			},
		},
		{
			Name:     "list-ingress",
			Usage:    "List all active ingress",
			Before:   createIngressClient,
			Action:   listIngress,
			Category: ingressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "room",
					Usage:    "limits list to a certain room name ",
					Required: false,
				},
			},
		},
		{
			Name:     "delete-ingress",
			Usage:    "Delete ingress",
			Before:   createIngressClient,
			Action:   deleteIngress,
			Category: ingressCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Ingress ID",
					Required: true,
				},
			},
		},
	}

	ingressClient *lksdk.IngressClient
)

func createIngressClient(c *cli.Context) error {
	url := c.String("url")
	apiKey := c.String("api-key")
	apiSecret := c.String("api-secret")

	if c.Bool("verbose") {
		fmt.Printf("creating client to %s, with api-key: %s, secret: %s\n",
			url,
			masker.ID(apiKey),
			masker.ID(apiSecret))
	}

	ingressClient = lksdk.NewIngressClient(url, apiKey, apiSecret)
	return nil
}

func createIngress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.CreateIngressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := ingressClient.CreateIngress(context.Background(), req)
	if err != nil {
		return err
	}

	printIngressInfo(info)
	return nil
}

func updateIngress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.UpdateIngressRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := ingressClient.UpdateIngress(context.Background(), req)
	if err != nil {
		return err
	}

	printIngressInfo(info)
	return nil
}

func listIngress(c *cli.Context) error {
	res, err := ingressClient.ListIngress(context.Background(), &livekit.ListIngressRequest{
		RoomName: c.String("room"),
	})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"IngressID", "Name", "StreamKey", "URL", "Status", "Error"})
	for _, item := range res.Items {
		fmt.Println("LIST", item)
		table.Append([]string{
			item.IngressId,
			item.Name,
			item.StreamKey,
			item.Url,
			item.State.Status.String(),
			item.State.Error,
		})
	}
	table.Render()

	return nil
}

func deleteIngress(c *cli.Context) error {
	info, err := ingressClient.DeleteIngress(context.Background(), &livekit.DeleteIngressRequest{
		IngressId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printIngressInfo(info)
	return nil
}

func printIngressInfo(info *livekit.IngressInfo) {
	if info.State.Error == "" {
		fmt.Printf("IngressID: %v Status: %v\n", info.IngressId, info.State.Status)
	} else {
		fmt.Printf("IngressID: %v Error: %v\n", info.IngressId, info.State.Error)
	}
}
