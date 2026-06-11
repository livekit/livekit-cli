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
	"io"
	"os"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
)

// simLog is the single source of the plain-text simulation output: CI writes
// it to the terminal (results on out, transient progress on info) and the
// TUI's runReporter writes the same calls to the report file.
type simLog struct {
	out        io.Writer
	info       io.Writer
	prevStatus livekit.SimulationRun_Status
	prevDone   int
}

func newSimLog(out, info io.Writer) *simLog {
	return &simLog{out: out, info: info, prevStatus: livekit.SimulationRun_Status(-1)}
}

func (l *simLog) BeginSetup()    { fmt.Fprintln(l.out, "::group::Setup") }
func (l *simLog) EndSetup()      { fmt.Fprintln(l.out, "::endgroup::") }
func (l *simLog) StartingAgent() { fmt.Fprintln(l.info, "Starting agent...") }
func (l *simLog) WaitingForRegister() {
	fmt.Fprintln(l.info, "Waiting for agent to register...")
}

func (l *simLog) AgentRegistered(d time.Duration) {
	fmt.Fprintf(l.out, "✓ Agent registered (%s)\n", d.Round(time.Millisecond))
}

func (l *simLog) AgentStartFailed(err error) {
	fmt.Fprintf(l.out, "✗ Failed to start agent: %v\n", err)
}

func (l *simLog) SetupFailed(err error) {
	fmt.Fprintf(l.out, "✗ %v\n", err)
}

func (l *simLog) SimulationCreated(d time.Duration) {
	fmt.Fprintf(l.out, "✓ Simulation created (%s)\n", d.Round(time.Millisecond))
}

func (l *simLog) SourceUploaded(d time.Duration) {
	fmt.Fprintf(l.out, "✓ Source uploaded (%s)\n", d.Round(time.Millisecond))
}

func (l *simLog) ScenariosLoaded(g *livekit.ScenarioGroup, path string) {
	name := g.GetName()
	if name == "" {
		name = "scenarios"
	}
	fmt.Fprintf(l.out, "✓ Loaded %d scenarios from %s (%q)\n", len(g.GetScenarios()), path, name)
}

func (l *simLog) RunCreated(runID, dashboardURL string) {
	fmt.Fprintln(l.out)
	fmt.Fprintf(l.out, "Run:       %s\n", runID)
	if dashboardURL != "" {
		fmt.Fprintf(l.out, "Dashboard: %s\n", dashboardURL)
	}
	fmt.Fprintln(l.out)
}

// RunUpdate logs run-status transitions and job progress from a poll result.
func (l *simLog) RunUpdate(run *livekit.SimulationRun, configuredN int32) {
	_, done, _, _ := simulationJobCounts(run)
	switch run.Status {
	case livekit.SimulationRun_STATUS_GENERATING:
		if l.prevStatus != run.Status {
			n := configuredN
			if run.GetNumSimulations() > 0 {
				n = run.GetNumSimulations()
			}
			fmt.Fprintf(l.info, "Generating %d scenarios...\n", n)
		}
	case livekit.SimulationRun_STATUS_RUNNING:
		if l.prevStatus != run.Status {
			if desc := run.GetAgentDescription(); desc != "" {
				fmt.Fprintf(l.out, "Agent: %s\n\n", desc)
			}
		}
		if done != l.prevDone {
			fmt.Fprintf(l.info, "Running simulations... %d/%d completed\n", done, len(run.Jobs))
			l.prevDone = done
		}
	case livekit.SimulationRun_STATUS_SUMMARIZING:
		if l.prevStatus != run.Status {
			fmt.Fprintln(l.info, "Summarizing...")
		}
	}
	l.prevStatus = run.Status
}

func (l *simLog) BrokenAgent() {
	fmt.Fprintln(l.info, "The agent is failing to run jobs; cancelling the run.")
}

func (l *simLog) Results(run *livekit.SimulationRun, ap *AgentProcess) {
	writeRunResults(l.out, run, ap)
}

func writeBrokenAgentNote(w io.Writer, ap *AgentProcess) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The agent failed to run the simulations. It most likely errored on job")
	fmt.Fprintln(w, "startup (missing model file, bad dependency, etc.). Recent agent output:")
	for _, line := range agentErrorContext(ap) {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// asciiWriter transliterates the output glyphs to plain ASCII, keeping the
// report file free of special characters without forking the simLog strings.
type asciiWriter struct{ w io.Writer }

var asciiGlyphs = strings.NewReplacer(
	"✓", "[ok]",
	"✗", "[x]",
	"⏺", "[~]",
	"●", "*",
	"⚠", "[!]",
)

func (a asciiWriter) Write(p []byte) (int, error) {
	_, err := io.WriteString(a.w, asciiGlyphs.Replace(string(p)))
	return len(p), err
}

// runReporter writes the simLog to a temp file so TUI runs leave the same
// record the non-TUI mode prints.
type runReporter struct {
	*simLog
	f *os.File
}

func newRunReporter() *runReporter {
	f, err := os.CreateTemp("", "lk-simulate-report-*.txt")
	if err != nil {
		return &runReporter{simLog: newSimLog(io.Discard, io.Discard)}
	}
	w := asciiWriter{f}
	return &runReporter{simLog: newSimLog(w, w), f: f}
}

// Finish appends the results and trailer, closes the file, returns its path.
func (r *runReporter) Finish(run *livekit.SimulationRun, ap *AgentProcess, brokenAgent bool, dashboardURL string) string {
	if r.f == nil {
		return ""
	}
	if run != nil {
		r.Results(run, ap)
	}
	if brokenAgent && ap != nil {
		writeBrokenAgentNote(r.info, ap)
	}
	if ap != nil && ap.LogPath != "" {
		fmt.Fprintf(r.info, "Agent logs: %s\n", ap.LogPath)
	}
	if dashboardURL != "" {
		fmt.Fprintf(r.info, "Dashboard:  %s\n", dashboardURL)
	}
	r.f.Close()
	return r.f.Name()
}
