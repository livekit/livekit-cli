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
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const simulationRunJSONVersion = 1

// simulationRunJSON is a wrapper only: run and summary stay verbatim protojson,
// so proto field additions reach the export without a change here.
type simulationRunJSON struct {
	Version int             `json:"version"`
	Run     json.RawMessage `json:"run"`
	Summary json.RawMessage `json:"summary,omitempty"`
}

// protojson randomizes whitespace; the outer encoder re-indents, normalizing it.
var simulationRunMarshaler = protojson.MarshalOptions{UseProtoNames: true}

func writeSimulationRunJSON(w io.Writer, run *livekit.SimulationRun) error {
	if run == nil {
		return fmt.Errorf("cannot export a nil simulation run")
	}

	export := simulationRunJSON{Version: simulationRunJSONVersion}

	if summary := decodeRunSummary(run); summary != nil {
		encoded, err := simulationRunMarshaler.Marshal(summary)
		if err != nil {
			return fmt.Errorf("encode simulation run summary: %w", err)
		}
		export.Summary = encoded
	}

	// The summary is exported decoded above; the compressed copy would only bloat stdout.
	stripped := proto.Clone(run).(*livekit.SimulationRun)
	stripped.SummaryZstd = nil
	encoded, err := simulationRunMarshaler.Marshal(stripped)
	if err != nil {
		return fmt.Errorf("encode simulation run: %w", err)
	}
	export.Run = encoded

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(export); err != nil {
		return fmt.Errorf("encode simulation run JSON: %w", err)
	}
	return nil
}

// dumpSimulationRunJSON exports an existing run in a single fetch. An
// unfinished run is an error rather than a wait, so stdout is either a
// complete export or nothing; an unknown run ID surfaces the API's not-found
// error.
func dumpSimulationRunJSON(ctx context.Context, pc *config.ProjectConfig, runID string) error {
	client := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)

	fetchCtx, cancel := context.WithTimeout(ctx, simulationAPITimeout)
	defer cancel()
	run, err := getSimulationRun(fetchCtx, client, runID)
	if err != nil {
		return err
	}

	if !isTerminalRunStatus(run.GetStatus()) {
		return fmt.Errorf(
			"simulation run %s is still in progress (%s); follow it with %s",
			runID,
			strings.TrimPrefix(run.GetStatus().String(), "STATUS_"),
			viewCommandHint(runID),
		)
	}

	return writeSimulationRunJSON(out.ResultWriter(), run)
}
