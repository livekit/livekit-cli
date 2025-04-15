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
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	IngressCommands = []*cli.Command{
		{
			Name:  "ingress",
			Usage: "Import outside media sources into a LiveKit room",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Create an ingress",
					UsageText: "lk ingress create [OPTIONS] JSON",
					ArgsUsage: "JSON",
					Before:    createIngressClient,
					Action:    createIngress,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Hidden:    true, // deprecated: use ARG0
							Name:      "request",
							Usage:     "CreateIngressRequest as json file (see cmd/lk/examples)",
							TakesFile: true,
						},
					},
				},
				{
					Name:      "update",
					Usage:     "Update an ingress",
					UsageText: "lk ingress update [OPTIONS] JSON",
					ArgsUsage: "JSON",
					Before:    createIngressClient,
					Action:    updateIngress,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Hidden:    true, // deprecated: use ARG0
							Name:      "request",
							Usage:     "UpdateIngressRequest as json file (see cmd/lk/examples)",
							TakesFile: true,
						},
					},
				},
				{
					Name:      "list",
					Usage:     "List all active ingress",
					UsageText: "lk ingress list [OPTIONS]",
					Before:    createIngressClient,
					Action:    listIngress,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "room",
							Usage:    "Limits list to a certain room `NAME`",
							Required: false,
						},
						&cli.StringFlag{
							Name:     "id",
							Usage:    "List a specific ingress `ID`",
							Required: false,
						},
						jsonFlag,
					},
				},
				{
					Name:      "delete",
					Usage:     "Delete an ingress",
					UsageText: "lk ingress delete ID",
					ArgsUsage: "ID",
					Before:    createIngressClient,
					Action:    deleteIngress,
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `ingress create`
			Name:   "create-ingress",
			Usage:  "Create an ingress",
			Before: createIngressClient,
			Action: createIngress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateIngressRequest as json file (see cmd/lk/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `ingress update`
			Name:   "update-ingress",
			Usage:  "Update an ingress",
			Before: createIngressClient,
			Action: updateIngress,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "request",
					Usage:    "UpdateIngressRequest as json file (see cmd/lk/examples)",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `ingress list`
			Name:   "list-ingress",
			Usage:  "List all active ingress",
			Before: createIngressClient,
			Action: listIngress,
			Flags: []cli.Flag{
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
			},
		},
		{
			Hidden: true, // deprecated: use `ingress delete`
			Name:   "delete-ingress",
			Usage:  "Delete ingress",
			Before: createIngressClient,
			Action: deleteIngress,
			Flags: []cli.Flag{
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

func createIngressClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}

	ingressClient = lksdk.NewIngressClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil, nil
}

func createIngress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArgOrFlag[livekit.CreateIngressRequest](cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}

	info, err := ingressClient.CreateIngress(context.Background(), req)
	if err != nil {
		return err
	}

	printIngressInfo(info)
	return nil
}

func updateIngress(ctx context.Context, cmd *cli.Command) error {
	req, err := ReadRequestArgOrFlag[livekit.UpdateIngressRequest](cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}

	info, err := ingressClient.UpdateIngress(context.Background(), req)
	if err != nil {
		return err
	}

	printIngressInfo(info)
	return nil
}

func listIngress(ctx context.Context, cmd *cli.Command) error {
	res, err := ingressClient.ListIngress(context.Background(), &livekit.ListIngressRequest{
		RoomName:  cmd.String("room"),
		IngressId: cmd.String("id"),
	})
	if err != nil {
		return err
	}

	// NOTE: previously, the `verbose` flag was used to output JSON in addition to the table.
	// This is inconsistent with other commands in which verbose is used for debug info, but is
	// kept for compatibility with the previous behavior.
	if cmd.Bool("verbose") || cmd.Bool("json") {
		util.PrintJSON(res)
	} else {
		table := util.CreateTable().
			Headers("IngressID", "Name", "Room", "StreamKey", "URL", "Status", "Error")
		for _, item := range res.Items {
			if item == nil {
				continue
			}

			var status, errorStr string
			if item.State != nil {
				status = item.State.Status.String()
				errorStr = item.State.Error
			}

			table.Row(
				item.IngressId,
				item.Name,
				item.RoomName,
				item.StreamKey,
				item.Url,
				status,
				errorStr,
			)
		}
		fmt.Println(table)
	}

	return nil
}

func deleteIngress(ctx context.Context, cmd *cli.Command) error {
	id := cmd.String("id")
	if id == "" {
		id = cmd.Args().First()
	}
	info, err := ingressClient.DeleteIngress(ctx, &livekit.DeleteIngressRequest{
		IngressId: id,
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
