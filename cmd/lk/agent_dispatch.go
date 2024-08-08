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
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	AgentDispatchCommands = []*cli.Command{
		{
			Name:     "agentdispatch",
			Usage:    "Manage agent dispatches for a room",
			Category: "Agents",
			Commands: []*cli.Command{
				{
					Name:      "create",
					Usage:     "Create an agent dispatches",
					UsageText: "lk agentdispatch create [OPTIONS]",
					Before:    createAgentDispatchClient,
					Action:    createAgentDispatch,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "room",
							Usage:    "`Name` of the room to create the dispatch in",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "agent-name",
							Usage:    "`Agent Name` to dispatch the job to",
							Required: false,
						},
						&cli.StringFlag{
							Name:     "metadata",
							Usage:    "`Metadata` to pass to the agent workers",
							Required: false,
						},
					},
				},
				{
					Name:      "delete",
					Usage:     "Delete an agent dispatch",
					UsageText: "lk agentdispatch delete [OPTIONS]",
					Before:    createAgentDispatchClient,
					Action:    deleteAgentDispatch,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "room",
							Usage:    "`Name` of the room to create the dispatch in",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "id",
							Usage:    "`ID` of the dispatch to delete",
							Required: true,
						},
					},
				},

				{
					Name:      "list",
					Usage:     "List all active agent dispatches",
					UsageText: "lk agentdispatch list [OPTIONS]",
					Before:    createAgentDispatchClient,
					Action:    listAgentDispatches,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "room",
							Usage:    "List agents dispatches for room `Name`",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "id",
							Usage:    "List a specific agent dispatch `ID`",
							Required: false,
						},
					},
				},
			},
		},
	}

	agentDispatchClient *lksdk.AgentDispatchClient
)

func createAgentDispatchClient(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	agentDispatchClient = lksdk.NewAgentDispatchServiceClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil
}

func listAgentDispatches(ctx context.Context, cmd *cli.Command) error {
	res, err := agentDispatchClient.ListDispatch(context.Background(), &livekit.ListAgentDispatchRequest{
		Room:       cmd.String("room"),
		DispatchId: cmd.String("id"),
	})
	if err != nil {
		return err
	}

	table := CreateTable().
		Headers("DispatchID", "AgentName", "Room")
	for _, item := range res.AgentDispatches {
		if item == nil {
			continue
		}

		table.Row(
			item.Id,
			item.AgentName,
			item.Room,
		)
	}
	fmt.Println(table)

	if cmd.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func createAgentDispatch(ctx context.Context, cmd *cli.Command) error {
	res, err := agentDispatchClient.CreateDispatch(context.Background(), &livekit.CreateAgentDispatchRequest{
		Room:      cmd.String("room"),
		AgentName: cmd.String("agent-name"),
		Metadata:  cmd.String("metadata"),
	})
	if err != nil {
		return err
	}

	printAgentDispatch(res)

	if cmd.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func deleteAgentDispatch(ctx context.Context, cmd *cli.Command) error {
	res, err := agentDispatchClient.DeleteDispatch(context.Background(), &livekit.DeleteAgentDispatchRequest{
		Room:       cmd.String("room"),
		DispatchId: cmd.String("id"),
	})
	if err != nil {
		return err
	}

	printAgentDispatch(res)

	if cmd.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func printAgentDispatch(ad *livekit.AgentDispatch) {
	var createdAt time.Time

	table := CreateTable().
		Headers("JobID", "Job Type", "Participant Identity")

	if ad.State != nil {
		createdAt = time.Unix(0, ad.State.CreatedAt)

		for _, item := range ad.State.Jobs {
			identity := ""
			if item.Participant != nil {
				identity = item.Participant.Identity
			}

			table.Row(
				item.Id,
				item.Type.String(),
				identity,
			)
		}
	}

	fmt.Printf("DispatchID: %v CreatedAt: %v\n", ad.Id, createdAt)
	fmt.Println(table)

}
