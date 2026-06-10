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
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/livekit"
	agent "github.com/livekit/protocol/livekit/agent"
)

type toggleWriter struct {
	w       io.Writer
	enabled atomic.Bool
}

func (tw *toggleWriter) Write(p []byte) (int, error) {
	if tw.enabled.Load() {
		return tw.w.Write(p)
	}
	return len(p), nil
}

func runSimulateCI(ctx context.Context, config *simulateConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	var agent *AgentProcess
	var runID string
	var runFinished bool
	var run *livekit.SimulationRun

	cleanup := func() {
		if agent != nil {
			agent.Kill()
			if agent.LogPath != "" {
				fmt.Fprintf(os.Stderr, "Agent logs: %s\n", agent.LogPath)
			}
		}
		if config.mode == modeGenerateFromSource && run != nil {
			if path, err := writeGeneratedScenariosTemp(run); err == nil && path != "" {
				fmt.Fprintf(os.Stderr, "Generated scenarios: %s\n", path)
			}
		}
		if runID != "" && !runFinished {
			cancelSimulationRun(config.client, runID)
		}
	}
	defer cleanup()

	// --- Setup ---

	fmt.Fprintln(os.Stdout, "::group::Setup")

	fmt.Fprintf(os.Stderr, "Starting agent...\n")
	start := time.Now()
	logFwd := &toggleWriter{w: os.Stderr}
	logFwd.enabled.Store(true)
	var err error
	agent, err = startSimulationAgent(config, logFwd)
	if err != nil {
		fmt.Fprintf(os.Stdout, "✗ Failed to start agent: %v\n", err)
		fmt.Fprintln(os.Stdout, "::endgroup::")
		return fmt.Errorf("failed to start agent: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for agent to register...\n")
	timeout := time.NewTimer(agentRegisterTimeout)
	defer timeout.Stop()
	select {
	case <-agent.Ready():
		logFwd.enabled.Store(false)
		fmt.Fprintf(os.Stdout, "✓ Agent registered (%s)\n", time.Since(start).Round(time.Millisecond))
	case <-agent.Done():
		fmt.Fprintln(os.Stdout, "::endgroup::")
		return fmt.Errorf("the agent exited before registering.\n\n%s", agentExitDetail(agent))
	case <-timeout.C:
		fmt.Fprintln(os.Stdout, "::endgroup::")
		return fmt.Errorf("timed out after %s waiting for the agent to register.\n\n%s", agentRegisterTimeout, agentExitDetail(agent))
	case <-ctx.Done():
		fmt.Fprintln(os.Stdout, "::endgroup::")
		return ctx.Err()
	}

	start = time.Now()
	var presigned *livekit.PresignedPostRequest
	runID, presigned, err = createSimulationRun(ctx, config)
	if err != nil {
		fmt.Fprintf(os.Stdout, "✗ %v\n", err)
		fmt.Fprintln(os.Stdout, "::endgroup::")
		return err
	}
	fmt.Fprintf(os.Stdout, "✓ Simulation created (%s)\n", time.Since(start).Round(time.Millisecond))

	if config.mode == modeGenerateFromSource {
		start = time.Now()
		if err := uploadSource(ctx, config.client, runID, presigned, config.projectDir, config.entrypoint); err != nil {
			fmt.Fprintf(os.Stdout, "✗ %v\n", err)
			fmt.Fprintln(os.Stdout, "::endgroup::")
			return err
		}
		fmt.Fprintf(os.Stdout, "✓ Source uploaded (%s)\n", time.Since(start).Round(time.Millisecond))
	} else if g := config.scenarioGroup; g != nil {
		name := g.GetName()
		if name == "" {
			name = "scenarios"
		}
		fmt.Fprintf(os.Stdout, "✓ Loaded %d scenarios from %s (%q)\n", len(g.GetScenarios()), config.scenariosPath, name)
	}

	fmt.Fprintln(os.Stdout, "::endgroup::")
	fmt.Fprintln(os.Stdout)

	fmt.Fprintf(os.Stdout, "Run:       %s\n", runID)
	if url := simulationDashboardURL(config.pc.ProjectId, runID); url != "" {
		fmt.Fprintf(os.Stdout, "Dashboard: %s\n", url)
	}
	fmt.Fprintln(os.Stdout)

	// --- Poll until terminal ---

	prevDone := 0
	prevStatus := livekit.SimulationRun_STATUS_GENERATING
	brokenAgent := false
	ticker := time.NewTicker(simulationPollInterval)
	defer ticker.Stop()

	for {
		pollCtx, pollCancel := context.WithTimeout(ctx, simulationAPITimeout)
		run, err = getSimulationRun(pollCtx, config.client, runID)
		pollCancel()

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "Warning: poll failed: %v\n", err)
		} else {
			_, done, _, _ := simulationJobCounts(run)
			total := len(run.Jobs)

			// The worker is failing systemically (crashing on job startup, never
			// joining), not failing a scenario. Stop early and surface its log.
			if !brokenAgent && agentBroken(run, agent) {
				brokenAgent = true
				fmt.Fprintf(os.Stderr, "The agent is failing to run jobs; cancelling the run.\n")
				cancelSimulationRun(config.client, runID)
				runFinished = true
				break
			}

			switch run.Status {
			case livekit.SimulationRun_STATUS_GENERATING:
				if prevStatus != run.Status {
					n := config.numSimulations
					if run.GetNumSimulations() > 0 {
						n = run.GetNumSimulations()
					}
					fmt.Fprintf(os.Stderr, "Generating %d scenarios...\n", n)
				}
			case livekit.SimulationRun_STATUS_RUNNING:
				if prevStatus == livekit.SimulationRun_STATUS_GENERATING {
					if desc := run.GetAgentDescription(); desc != "" {
						fmt.Fprintf(os.Stdout, "Agent: %s\n\n", desc)
					}
				}
				if done != prevDone {
					fmt.Fprintf(os.Stderr, "Running simulations... %d/%d completed\n", done, total)
					prevDone = done
				}
			case livekit.SimulationRun_STATUS_SUMMARIZING:
				if prevStatus != run.Status {
					fmt.Fprintf(os.Stderr, "Summarizing...\n")
				}
			}
			prevStatus = run.Status

			if isTerminalRunStatus(run.Status) {
				runFinished = true
				break
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// --- Results ---

	printCIResults(run, agent)

	if brokenAgent && agent != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "The agent failed to run the simulations. It most likely errored on job")
		fmt.Fprintln(os.Stderr, "startup (missing model file, bad dependency, etc.). Recent agent output:")
		for _, line := range agentErrorContext(agent) {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	}

	if agent != nil && agent.LogPath != "" {
		fmt.Fprintf(os.Stderr, "Agent logs: %s\n", agent.LogPath)
	}
	if url := simulationDashboardURL(config.pc.ProjectId, runID); url != "" {
		fmt.Fprintf(os.Stderr, "Dashboard:  %s\n", url)
	}

	_, _, _, failed := simulationJobCounts(run)
	if failed > 0 || run.Status == livekit.SimulationRun_STATUS_FAILED {
		if isGitHubActions() {
			if failed > 0 {
				fmt.Fprintf(os.Stdout, "::error::%d simulation(s) failed\n", failed)
			} else {
				fmt.Fprintf(os.Stdout, "::error::Simulation run failed: %s\n", run.Error)
			}
		}
		if run.Status == livekit.SimulationRun_STATUS_FAILED && len(run.Jobs) == 0 {
			return fmt.Errorf("simulation failed: %s", run.Error)
		}
		return fmt.Errorf("%d of %d simulations failed", failed, len(run.Jobs))
	}

	return nil
}

func printCIResults(run *livekit.SimulationRun, agent *AgentProcess) {
	if run == nil {
		return
	}

	if run.Status == livekit.SimulationRun_STATUS_FAILED && len(run.Jobs) == 0 {
		fmt.Fprintf(os.Stdout, "✗ Simulation failed: %s\n", run.Error)
		return
	}

	for i, job := range run.Jobs {
		icon := "⏺"
		switch job.Status {
		case livekit.SimulationRun_Job_STATUS_COMPLETED:
			icon = "✓"
		case livekit.SimulationRun_Job_STATUS_FAILED:
			icon = "✗"
		}

		label := job.Label
		if label == "" {
			label = fmt.Sprintf("Job %d", i+1)
		}

		fmt.Fprintf(os.Stdout, "::group::%s %s\n", icon, label)

		if job.Instructions != "" {
			fmt.Fprintln(os.Stdout, "Instructions:")
			for _, line := range strings.Split(job.Instructions, "\n") {
				fmt.Fprintf(os.Stdout, "  %s\n", line)
			}
		}

		if job.AgentExpectations != "" {
			fmt.Fprintln(os.Stdout, "Expected:")
			for _, line := range strings.Split(job.AgentExpectations, "\n") {
				fmt.Fprintf(os.Stdout, "  %s\n", line)
			}
		}

		if job.Error != "" {
			if job.Status == livekit.SimulationRun_Job_STATUS_COMPLETED {
				fmt.Fprintf(os.Stdout, "Result: %s\n", job.Error)
			} else {
				fmt.Fprintf(os.Stdout, "Error: %s\n", job.Error)
			}
		}

		if run.Summary != nil && run.Summary.ChatHistory != nil {
			printCIChatHistory(run.Summary.ChatHistory[job.Id])
		}

		if agent != nil && job.RoomName != "" {
			logs := agent.RecentRoomLogs(0, job.RoomName)
			if len(logs) > 0 {
				fmt.Fprintln(os.Stdout, "Logs:")
				for _, line := range logs {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}
		}

		fmt.Fprintln(os.Stdout, "::endgroup::")

		if job.Status == livekit.SimulationRun_Job_STATUS_FAILED && isGitHubActions() {
			firstLine := strings.SplitN(job.Error, "\n", 2)[0]
			fmt.Fprintf(os.Stdout, "::error::Job %d failed: %s\n", i+1, firstLine)
		}
	}

	if run.Summary != nil {
		printCISummary(run)
	} else {
		msg := "The summary for this run is not available"
		if run.Error != "" {
			msg = run.Error
		}
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "⚠ "+msg)
	}
}

func printCISummary(run *livekit.SimulationRun) {
	summary := run.Summary
	total, _, passed, failed := simulationJobCounts(run)

	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "::group::Summary")
	fmt.Fprintf(os.Stdout, "%d total, %d passed, %d failed\n", total, passed, failed)

	if summary.GoingWell != "" {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Going well:")
		for _, line := range strings.Split(summary.GoingWell, "\n") {
			fmt.Fprintf(os.Stdout, "  %s\n", line)
		}
	}

	if summary.ToImprove != "" {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "To improve:")
		for _, line := range strings.Split(summary.ToImprove, "\n") {
			fmt.Fprintf(os.Stdout, "  %s\n", line)
		}
	}

	if len(summary.Issues) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Issues:")
		for i, issue := range summary.Issues {
			fmt.Fprintf(os.Stdout, "  %d. %s\n", i+1, issue.Description)
			if issue.Suggestion != "" {
				fmt.Fprintf(os.Stdout, "     Suggestion: %s\n", issue.Suggestion)
			}
		}
	}

	fmt.Fprintln(os.Stdout, "::endgroup::")
}

func printCIChatHistory(chatCtx *agent.ChatContext) {
	if chatCtx == nil || len(chatCtx.Items) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, "Transcript:")
	for _, item := range chatCtx.Items {
		switch v := item.Item.(type) {
		case *agent.ChatContext_ChatItem_Message:
			msg := v.Message
			text := chatMessageText(msg)
			if text == "" {
				continue
			}
			switch msg.Role {
			case agent.ChatRole_USER:
				fmt.Fprintf(os.Stdout, "  ● You\n")
			case agent.ChatRole_ASSISTANT:
				fmt.Fprintf(os.Stdout, "  ● Agent\n")
			default:
				fmt.Fprintf(os.Stdout, "  ● %s\n", msg.Role)
			}
			for _, tl := range strings.Split(text, "\n") {
				fmt.Fprintf(os.Stdout, "    %s\n", tl)
			}
		case *agent.ChatContext_ChatItem_FunctionCall:
			fc := v.FunctionCall
			args := fc.Arguments
			if len(args) > 80 {
				args = args[:80] + "..."
			}
			fmt.Fprintf(os.Stdout, "  [call] %s(%s)\n", fc.Name, args)
		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			fco := v.FunctionCallOutput
			output := fco.Output
			if len(output) > 80 {
				output = output[:80] + "..."
			}
			label := "output"
			if fco.IsError {
				label = "error"
			}
			fmt.Fprintf(os.Stdout, "  [%s] %s -> %s\n", label, fco.Name, output)
		case *agent.ChatContext_ChatItem_AgentHandoff:
			h := v.AgentHandoff
			fmt.Fprintf(os.Stdout, "  [handoff] -> %s\n", h.NewAgentId)
		}
	}
}

// maxAgentNotJoined is how many "agent never joined" timeouts to tolerate before
// treating the worker as broken. The cloud paces connections to the worker under
// load, so a single timeout can be transient rather than a real failure.
const maxAgentNotJoined = 1

// agentFatalMarkers are framework log lines that mean the worker hit a fatal,
// non-transient error (a job crashed, a process failed to initialize, or a
// session gave up) rather than a scenario failing.
var agentFatalMarkers = []string{
	"unhandled exception while running the job task",
	"error initializing process",
	"closing due to unrecoverable error",
}

// agentBroken reports whether the worker is failing systemically, so the run
// should be cancelled and its log surfaced instead of waited out. If any scenario
// completed, the agent demonstrably works and we never treat it as broken (the
// no-joins are pacing). Otherwise it triggers on a fatal error in the worker log,
// or on more than one "agent never joined" timeout.
func agentBroken(run *livekit.SimulationRun, ap *AgentProcess) bool {
	completed, notJoined := 0, 0
	for _, job := range run.GetJobs() {
		switch job.GetStatus() {
		case livekit.SimulationRun_Job_STATUS_COMPLETED:
			completed++
		case livekit.SimulationRun_Job_STATUS_FAILED:
			if strings.Contains(job.GetError(), "No agent joined room") {
				notJoined++
			}
		}
	}
	if completed > 0 {
		return false
	}
	if ap != nil && lastFatalMarker(ap.RecentLogs(0)) >= 0 {
		return true
	}
	return notJoined > maxAgentNotJoined
}

// lastFatalMarker returns the index of the last log line containing a fatal
// marker, or -1 if none.
func lastFatalMarker(logs []string) int {
	last := -1
	for i, line := range logs {
		for _, marker := range agentFatalMarkers {
			if strings.Contains(line, marker) {
				last = i
				break
			}
		}
	}
	return last
}

// agentErrorContext returns the agent output to surface for a broken agent: the
// complete error block (from the last fatal marker through the end of the log, so
// the full traceback is never truncated) when one is present, otherwise the
// recent tail.
func agentErrorContext(ap *AgentProcess) []string {
	logs := ap.RecentLogs(0)
	if i := lastFatalMarker(logs); i >= 0 {
		return logs[i:]
	}
	return lastNonEmptyLines(logs, 25)
}

func isGitHubActions() bool {
	return os.Getenv("GITHUB_ACTIONS") != ""
}
