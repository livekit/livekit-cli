// Copyright 2024 LiveKit, Inc.
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

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/utils"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var (
	DispatchCommands = []*cli.Command{
		{
			Name:  "dispatch",
			Usage: "Create, list, and delete agent dispatches",
			Commands: []*cli.Command{
				{
					Name:      "list",
					Usage:     "List all agent dispatches in a room",
					Before:    createDispatchClient,
					Action:    listAgentDispatches,
					ArgsUsage: "ROOM_NAME",
				},
				{
					Name:      "get",
					Usage:     "Get an agent dispatch by room and ID",
					Before:    createDispatchClient,
					Action:    getAgentDispatch,
					ArgsUsage: "ROOM_NAME ID",
				},
				{
					Name:   "create",
					Usage:  "Create an agent dispatch",
					Before: createDispatchClient,
					Action: createAgentDispatch,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "room",
							Usage: "room name",
						},
						&cli.BoolFlag{
							Name:  "new-room",
							Usage: "when set, will generate a unique room name",
						},
						&cli.StringFlag{
							Name:  "agent-name",
							Usage: "agent to dispatch",
						},
						&cli.StringFlag{
							Name:  "metadata",
							Usage: "metadata to send to agent",
						},
					},
				},
				{
					Name:      "delete",
					Usage:     "Delete an agent dispatch",
					Before:    createDispatchClient,
					Action:    deleteAgentDispatch,
					ArgsUsage: "ROOM_NAME ID",
				},
			},
		},
	}

	dispatchClient *lksdk.AgentDispatchClient
)

func createDispatchClient(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}

	dispatchClient = lksdk.NewAgentDispatchServiceClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil, nil
}

func getAgentDispatch(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}
	roomName := cmd.Args().First()
	if roomName == "" {
		return errors.New("room name is required")
	}
	id := cmd.Args().Get(1)
	if id == "" {
		return errors.New("dispatch ID is required")
	}

	return listDispatchAndPrint(cmd, &livekit.ListAgentDispatchRequest{
		Room:       roomName,
		DispatchId: id,
	})
}

func listAgentDispatches(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}
	roomName := cmd.Args().First()
	if roomName == "" {
		return errors.New("room name is required")
	}

	return listDispatchAndPrint(cmd, &livekit.ListAgentDispatchRequest{
		Room: roomName,
	})
}

func listDispatchAndPrint(cmd *cli.Command, req *livekit.ListAgentDispatchRequest) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}
	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}
	res, err := dispatchClient.ListDispatch(context.Background(), req)
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		util.PrintJSON(res)
	} else {
		table := util.CreateTable().
			Headers("DispatchID", "Room", "AgentName", "Metadata")
		for _, item := range res.AgentDispatches {
			if item == nil {
				continue
			}

			table.Row(
				item.Id,
				item.Room,
				item.AgentName,
				item.Metadata,
			)
		}
		fmt.Println(table)
	}
	return nil
}

func createAgentDispatch(ctx context.Context, cmd *cli.Command) error {
	req := &livekit.CreateAgentDispatchRequest{
		Room:      cmd.String("room"),
		AgentName: cmd.String("agent-name"),
		Metadata:  cmd.String("metadata"),
	}
	if cmd.Bool("new-room") {
		req.Room = utils.NewGuid("room-")
	}
	if req.Room == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("room or new-room is required")
	}
	if req.AgentName == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("agent-name is required")
	}
	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}

	info, err := dispatchClient.CreateDispatch(context.Background(), req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(info)
	} else {
		fmt.Printf("Dispatch created: %v\n", info)
	}

	return nil
}

func deleteAgentDispatch(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}

	roomName := cmd.Args().First()
	if roomName == "" {
		return errors.New("room name is required")
	}
	id := cmd.Args().Get(1)
	if id == "" {
		return errors.New("dispatch ID is required")
	}

	info, err := dispatchClient.DeleteDispatch(ctx, &livekit.DeleteAgentDispatchRequest{
		Room:       roomName,
		DispatchId: id,
	})
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(info)
	} else {
		fmt.Printf("Dispatch deleted: %v\n", info)
	}
	return nil
}
