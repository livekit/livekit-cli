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
	"net/url"
	"strings"

	"github.com/urfave/cli/v3"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

// maxCompareRuns matches the dashboard's compare page, which drops runs past it.
const maxCompareRuns = 4

var simulateCompareCommand = &cli.Command{
	Name:            "compare",
	Usage:           "Compare runs side by side. Prints the dashboard's compare page, or the runs' data with --json. The first run is the baseline",
	ArgsUsage:       "RUN_ID RUN_ID [RUN_ID...]",
	HideHelpCommand: true,
	Before:          loadSimulateProject,
	Flags:           []cli.Flag{jsonFlagWithUsage("Print each run and its per-job chat contexts as JSON, in the order given, instead of the dashboard URL.")},
	Action:          runSimulateCompare,
}

// simulationCompareJSON holds each run as `export` prints it, baseline first.
type simulationCompareJSON struct {
	Version int                 `json:"version"`
	Runs    []simulationRunJSON `json:"runs"`
}

func runSimulateCompare(ctx context.Context, cmd *cli.Command) error {
	runIDs, err := compareRunIDs(cmd.Args().Slice())
	if err != nil {
		return err
	}
	pc := simulateProjectConfig

	if !cmd.Bool("json") {
		u := simulationCompareDashboardURL(pc.ProjectId, runIDs)
		if u == "" {
			return fmt.Errorf("no project ID to link the dashboard to; pick a project with --project, or use --json")
		}
		fmt.Fprintln(out.ResultWriter(), u)
		return nil
	}

	client := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)
	export := simulationCompareJSON{Version: simulationRunJSONVersion}
	for _, id := range runIDs {
		fetchCtx, cancel := context.WithTimeout(ctx, simulationAPITimeout)
		run, err := getSimulationRun(fetchCtx, client, id, pc.ProjectId)
		cancel()
		if err != nil {
			return err
		}
		r, err := newSimulationRunJSON(run)
		if err != nil {
			return err
		}
		export.Runs = append(export.Runs, r)
	}
	return encodeIndentedJSON(out.ResultWriter(), export)
}

func compareRunIDs(args []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, id := range args {
		if seen[id] {
			return nil, fmt.Errorf("run %s is listed more than once", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) < 2 || len(ids) > maxCompareRuns {
		return nil, fmt.Errorf("compare takes 2 to %d run IDs (got %d)", maxCompareRuns, len(ids))
	}
	return ids, nil
}

// simulationCompareDashboardURL is "" without a project ID, as
// simulationDashboardURL is.
func simulationCompareDashboardURL(projectID string, runIDs []string) string {
	if projectID == "" {
		return ""
	}
	base := dashboardBaseURL()
	escaped := make([]string, len(runIDs))
	for i, id := range runIDs {
		escaped[i] = url.QueryEscape(id)
	}
	return fmt.Sprintf("%s/projects/%s/simulations/compare?runs=%s", base, projectID, strings.Join(escaped, ","))
}
