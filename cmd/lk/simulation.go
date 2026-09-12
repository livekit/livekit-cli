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

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// SimulationCommands are the Public-API-only agent-simulation commands. The
// group is Hidden and requires --experimental-auth (user-based auth).
var SimulationCommands = []*cli.Command{
	{
		Name:   "simulation",
		Usage:  "Manage LiveKit Cloud agent simulation runs (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List simulation runs for a project",
				UsageText: "lk simulation list --project PROJECT [--status STATUS] --experimental-auth",
				Action:    cloudListSimulationRuns,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "status", Usage: "Filter by `STATUS` (running, completed, failed, cancelled, ...)"},
					jsonFlag,
				},
			},
			{
				Name:      "get",
				Usage:     "Get a simulation run by ID",
				UsageText: "lk simulation get RUN_ID --project PROJECT --experimental-auth",
				ArgsUsage: "RUN_ID",
				Action:    cloudGetSimulationRun,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Create a simulation run",
				UsageText: "lk simulation create --project PROJECT [--agent NAME] [--mode text|audio] --experimental-auth",
				Action:    cloudCreateSimulationRun,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "agent", Usage: "Agent `NAME` to simulate against"},
					&cli.IntFlag{Name: "num", Usage: "`NUMBER` of scenarios to generate"},
					&cli.IntFlag{Name: "concurrency", Usage: "Maximum jobs to run in `PARALLEL`"},
					&cli.StringFlag{Name: "mode", Usage: "Conversation `MODE`: text (default) or audio"},
					&cli.StringFlag{Name: "region", Usage: "`REGION` to run in"},
					jsonFlag,
				},
			},
			{
				Name:      "cancel",
				Usage:     "Cancel an in-progress simulation run",
				UsageText: "lk simulation cancel RUN_ID --project PROJECT --experimental-auth",
				ArgsUsage: "RUN_ID",
				Action:    cloudCancelSimulationRun,
				Flags:     []cli.Flag{jsonFlag},
			},
		},
	},
}

func simulationProjectID(ctx context.Context, cmd *cli.Command) (*public.Client, string, error) {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return nil, "", err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return nil, "", err
	}
	return client, projectID, nil
}

func cloudListSimulationRuns(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := simulationProjectID(ctx, cmd)
	if err != nil {
		return err
	}
	runs, err := client.ListSimulationRuns(ctx, projectID, cmd.String("status"))
	if err != nil {
		return cloudAPIError(err)
	}
	return render.SimulationRuns(out, cmd.Bool("json"), runs)
}

func cloudGetSimulationRun(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := simulationProjectID(ctx, cmd)
	if err != nil {
		return err
	}
	runID, err := argN(cmd, 0, "run ID")
	if err != nil {
		return err
	}
	run, err := client.GetSimulationRun(ctx, projectID, runID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.SimulationRun(out, cmd.Bool("json"), *run)
}

func cloudCreateSimulationRun(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := simulationProjectID(ctx, cmd)
	if err != nil {
		return err
	}
	created, err := client.CreateSimulationRun(ctx, projectID, public.CreateSimulationOptions{
		AgentName:      cmd.String("agent"),
		NumSimulations: int32(cmd.Int("num")),
		Concurrency:    int32(cmd.Int("concurrency")),
		Mode:           cmd.String("mode"),
		Region:         cmd.String("region"),
	})
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(created)
		return nil
	}
	out.Statusf("Created simulation run %s", util.Accented(util.DashString(created.SimulationRunId)))
	if created.PresignedPostRequest != nil && created.PresignedPostRequest.Url != nil {
		out.Statusf("Upload the agent bundle (POST) to: %s", *created.PresignedPostRequest.Url)
	}
	return nil
}

func cloudCancelSimulationRun(ctx context.Context, cmd *cli.Command) error {
	client, projectID, err := simulationProjectID(ctx, cmd)
	if err != nil {
		return err
	}
	runID, err := argN(cmd, 0, "run ID")
	if err != nil {
		return err
	}
	if err := client.CancelSimulationRun(ctx, projectID, runID); err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(map[string]any{"id": runID, "cancelled": true})
		return nil
	}
	out.Statusf("Cancelled simulation run %s", util.Accented(runID))
	return nil
}
