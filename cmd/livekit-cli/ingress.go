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
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
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
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateIngressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "update-ingress",
			Usage:    "Update an ingress",
			Before:   createIngressClient,
			Action:   updateIngress,
			Category: ingressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "UpdateIngressRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "list-ingress",
			Usage:    "List all active ingress",
			Before:   createIngressClient,
			Action:   listIngress,
			Category: ingressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "room",
					Usage:    "limits list to a certain room name ",
					Required: false,
				},
				&cli.StringFlag{
					Name:     "id",
					Usage:    "list a specific ingress id",
					Required: false,
				},
			),
		},
		{
			Name:     "delete-ingress",
			Usage:    "Delete ingress",
			Before:   createIngressClient,
			Action:   deleteIngress,
			Category: ingressCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Ingress ID",
					Required: true,
				},
			),
		},
	}

	ingressClient *lksdk.IngressClient
)

func createIngressClient(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	ingressClient = lksdk.NewIngressClient(pc.URL, pc.APIKey, pc.APISecret)
	return nil
}

func createIngress(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := os.ReadFile(reqFile)
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
	reqBytes, err := os.ReadFile(reqFile)
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
		RoomName:  c.String("room"),
		IngressId: c.String("id"),
	})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"IngressID", "Name", "Room", "StreamKey", "URL", "Status", "Error"})
	for _, item := range res.Items {
		if item == nil {
			continue
		}

		var status, errorStr string
		if item.State != nil {
			status = item.State.Status.String()
			errorStr = item.State.Error
		}

		table.Append([]string{
			item.IngressId,
			item.Name,
			item.RoomName,
			item.StreamKey,
			item.Url,
			status,
			errorStr,
		})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

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
	var status, errorStr string

	if info.State != nil {
		errorStr = info.State.Error
		status = info.State.Status.String()
	}

	if errorStr == "" {
		fmt.Printf("IngressID: %v Status: %v\n", info.IngressId, status)
		fmt.Printf("URL: %v Stream Key: %s\n", info.Url, info.StreamKey)
	} else {
		fmt.Printf("IngressID: %v Error: %v\n", info.IngressId, errorStr)
	}
}
