//go:build console

// Copyright 2025 LiveKit, Inc.
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
	"fmt"
	"os"

	"github.com/livekit/protocol/livekit"
)

// runReporter records to a temp file the same plain-text log a non-TUI run
// prints as it progresses (setup steps, status transitions, job progress,
// then the results), so TUI runs always leave the same record.
type runReporter struct {
	f          *os.File
	prevStatus livekit.SimulationRun_Status
	prevDone   int
}

func newRunReporter() *runReporter {
	f, err := os.CreateTemp("", "lk-simulate-report-*.txt")
	if err != nil {
		return &runReporter{}
	}
	return &runReporter{f: f, prevStatus: livekit.SimulationRun_Status(-1)}
}

func (r *runReporter) Printf(format string, args ...any) {
	if r.f == nil {
		return
	}
	fmt.Fprintf(r.f, format+"\n", args...)
}

// Update logs run-status transitions and job progress, mirroring the non-TUI
// poll loop.
func (r *runReporter) Update(run *livekit.SimulationRun) {
	if r.f == nil || run == nil {
		return
	}
	_, done, _, _ := simulationJobCounts(run)
	switch run.Status {
	case livekit.SimulationRun_STATUS_GENERATING:
		if r.prevStatus != run.Status {
			r.Printf("Generating %d scenarios...", run.GetNumSimulations())
		}
	case livekit.SimulationRun_STATUS_RUNNING:
		if r.prevStatus != run.Status {
			if desc := run.GetAgentDescription(); desc != "" {
				r.Printf("Agent: %s", desc)
			}
		}
		if done != r.prevDone {
			r.Printf("Running simulations... %d/%d completed", done, len(run.Jobs))
			r.prevDone = done
		}
	case livekit.SimulationRun_STATUS_SUMMARIZING:
		if r.prevStatus != run.Status {
			r.Printf("Summarizing...")
		}
	}
	r.prevStatus = run.Status
}

// Finish appends the results section, closes the file, and returns its path.
func (r *runReporter) Finish(run *livekit.SimulationRun, ap *AgentProcess) string {
	if r.f == nil {
		return ""
	}
	if run != nil {
		fmt.Fprintln(r.f)
		writeRunResults(r.f, run, ap, false)
	}
	if ap != nil && ap.LogPath != "" {
		fmt.Fprintln(r.f)
		fmt.Fprintf(r.f, "Agent logs: %s\n", ap.LogPath)
	}
	r.f.Close()
	return r.f.Name()
}
