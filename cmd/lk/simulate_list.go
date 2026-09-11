// Copyright 2026 LiveKit, Inc.
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
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

var simulateListCommand = &cli.Command{
	Name:            "list",
	Usage:           "List the project's most recent simulation runs",
	HideHelpCommand: true,
	Action:          listSimulationRuns,
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "limit",
			Usage: "maximum number of runs to return. If unset, defaults to API page size",
		},
		jsonFlag,
	},
}

// listSimulationRuns prints runs newest first, without jobs. Pass/fail detail
// lives in `view`.
func listSimulationRuns(ctx context.Context, cmd *cli.Command) error {
	pc := simulateProjectConfig
	client := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	limit := cmd.Int("limit")
	var runs []*livekit.SimulationRun
	var resp *livekit.SimulationRun_List_Response
	for resp == nil || (len(runs) < limit && resp.NextPageToken.GetToken() != "") {
		req := &livekit.SimulationRun_List_Request{ProjectId: pc.ProjectId}
		if resp != nil {
			req.PageToken = &livekit.TokenPagination{Token: resp.NextPageToken.GetToken()}
		}
		pageCtx, cancel := context.WithTimeout(ctx, simulationAPITimeout)
		var err error
		resp, err = client.ListSimulationRuns(pageCtx, req)
		cancel()
		if err != nil {
			return fmt.Errorf("unable to list simulation runs: %w", err)
		}
		runs = append(runs, resp.Runs...)
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}

	if cmd.Bool("json") {
		util.PrintJSON(&livekit.SimulationRun_List_Response{Runs: runs})
		return nil
	}

	if len(runs) == 0 {
		out.Status("No simulation runs found")
		return nil
	}

	var rows [][]string
	for _, run := range runs {
		rows = append(rows, []string{
			run.GetId(),
			formatDeployedAt(run.GetCreatedAt().AsTime()),
			strings.TrimPrefix(run.GetStatus().String(), "STATUS_"),
			strings.TrimPrefix(run.GetMode().String(), "SIMULATION_MODE_"),
			run.GetAgentName(),
		})
	}

	t := util.CreateTable().
		Headers("ID", "Created At", "Status", "Mode", "Agent").
		Rows(rows...)
	out.Result(t)
	fmt.Fprintf(out.StatusWriter(), "To open a run: %s\n", viewCommandHint("<run-id>"))
	return nil
}
