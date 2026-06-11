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

	report := newSimLog(os.Stdout, os.Stderr)
	report.BeginSetup()

	report.StartingAgent()
	start := time.Now()
	logFwd := &toggleWriter{w: os.Stderr}
	logFwd.enabled.Store(true)
	var err error
	agent, err = startSimulationAgent(config, logFwd)
	if err != nil {
		report.AgentStartFailed(err)
		report.EndSetup()
		return fmt.Errorf("failed to start agent: %w", err)
	}

	report.WaitingForRegister()
	timeout := time.NewTimer(agentRegisterTimeout)
	defer timeout.Stop()
	select {
	case <-agent.Ready():
		logFwd.enabled.Store(false)
		report.AgentRegistered(time.Since(start))
	case <-agent.Done():
		report.EndSetup()
		return fmt.Errorf("the agent exited before registering.\n\n%s", agentExitDetail(agent))
	case <-timeout.C:
		report.EndSetup()
		return fmt.Errorf("timed out after %s waiting for the agent to register.\n\n%s", agentRegisterTimeout, agentExitDetail(agent))
	case <-ctx.Done():
		report.EndSetup()
		return ctx.Err()
	}

	start = time.Now()
	var presigned *livekit.PresignedPostRequest
	runID, presigned, err = createSimulationRun(ctx, config)
	if err != nil {
		report.SetupFailed(err)
		report.EndSetup()
		return err
	}
	report.SimulationCreated(time.Since(start))

	if config.mode == modeGenerateFromSource {
		start = time.Now()
		if err := uploadSource(ctx, config.client, runID, presigned, config.projectDir, config.entrypoint); err != nil {
			report.SetupFailed(err)
			report.EndSetup()
			return err
		}
		report.SourceUploaded(time.Since(start))
	} else if g := config.scenarioGroup; g != nil {
		report.ScenariosLoaded(g, config.scenariosPath)
	}

	report.EndSetup()
	report.RunCreated(runID, simulationDashboardURL(config.pc.ProjectId, runID))

	// --- Poll until terminal ---

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
			// the worker is failing systemically: stop early and surface its log
			if !brokenAgent && agentBroken(run, agent) {
				brokenAgent = true
				report.BrokenAgent()
				cancelSimulationRun(config.client, runID)
				runFinished = true
				break
			}

			report.RunUpdate(run, config.numSimulations)

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

	report.Results(run, agent)

	if brokenAgent && agent != nil {
		writeBrokenAgentNote(os.Stderr, agent)
	}

	if url := simulationDashboardURL(config.pc.ProjectId, runID); url != "" {
		fmt.Fprintf(os.Stderr, "Dashboard:  %s\n", url)
	}

	_, _, _, failed := simulationJobCounts(run)
	if failed > 0 || run.Status == livekit.SimulationRun_STATUS_FAILED {
		if failed > 0 {
			fmt.Fprintf(os.Stdout, "::error::%d simulation(s) failed\n", failed)
		} else {
			fmt.Fprintf(os.Stdout, "::error::Simulation run failed: %s\n", run.Error)
		}
		if run.Status == livekit.SimulationRun_STATUS_FAILED && len(run.Jobs) == 0 {
			return fmt.Errorf("simulation failed: %s", run.Error)
		}
		return fmt.Errorf("%d of %d simulations failed", failed, len(run.Jobs))
	}

	return nil
}

// writeRunResults writes the per-job results and the run summary, with GitHub
// group markers (a useful delimiter outside GitHub too).
func writeRunResults(w io.Writer, run *livekit.SimulationRun, ap *AgentProcess) {
	if run == nil {
		return
	}

	if run.Status == livekit.SimulationRun_STATUS_FAILED && len(run.Jobs) == 0 {
		fmt.Fprintf(w, "✗ Simulation failed: %s\n", run.Error)
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

		fmt.Fprintf(w, "::group::%s %s (%s)\n", icon, label, job.Id)

		if job.Instructions != "" {
			fmt.Fprintln(w, "Instructions:")
			for line := range strings.SplitSeq(job.Instructions, "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}

		if job.AgentExpectations != "" {
			fmt.Fprintln(w, "Expected:")
			for line := range strings.SplitSeq(job.AgentExpectations, "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}

		if job.Error != "" {
			if job.Status == livekit.SimulationRun_Job_STATUS_COMPLETED {
				fmt.Fprintf(w, "Result: %s\n", job.Error)
			} else {
				fmt.Fprintf(w, "Error: %s\n", job.Error)
			}
		}

		if run.Summary != nil && run.Summary.ChatHistory != nil {
			writeChatHistory(w, run.Summary.ChatHistory[job.Id])
		}

		if ap != nil && job.RoomName != "" {
			logs := ap.RecentRoomLogs(0, job.RoomName)
			if len(logs) > 0 {
				fmt.Fprintln(w, "Logs:")
				for _, line := range logs {
					fmt.Fprintf(w, "  %s\n", ansiEscapeRe.ReplaceAllString(line, ""))
				}
			}
		}

		fmt.Fprintln(w, "::endgroup::")

		if job.Status == livekit.SimulationRun_Job_STATUS_FAILED {
			firstLine, _, _ := strings.Cut(job.Error, "\n")
			fmt.Fprintf(w, "::error::Job %d failed: %s\n", i+1, firstLine)
		}
	}

	if run.Summary != nil {
		writeRunSummary(w, run)
	} else {
		msg := "The summary for this run is not available"
		if run.Error != "" {
			msg = run.Error
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "⚠ "+msg)
	}
}

func writeRunSummary(w io.Writer, run *livekit.SimulationRun) {
	summary := run.Summary
	total, _, passed, failed := simulationJobCounts(run)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "::group::Summary")
	fmt.Fprintf(w, "%d total, %d passed, %d failed\n", total, passed, failed)

	if summary.GoingWell != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Going well:")
		for line := range strings.SplitSeq(summary.GoingWell, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}

	if summary.ToImprove != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "To improve:")
		for line := range strings.SplitSeq(summary.ToImprove, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}

	if len(summary.Issues) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Issues:")
		for i, issue := range summary.Issues {
			fmt.Fprintf(w, "  %d. %s\n", i+1, issue.Description)
			if issue.Suggestion != "" {
				fmt.Fprintf(w, "     Suggestion: %s\n", issue.Suggestion)
			}
		}
	}

	fmt.Fprintln(w, "::endgroup::")
}

func writeChatHistory(w io.Writer, chatCtx *agent.ChatContext) {
	if chatCtx == nil || len(chatCtx.Items) == 0 {
		return
	}
	fmt.Fprintln(w, "Transcript:")
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
				fmt.Fprintf(w, "  ● You\n")
			case agent.ChatRole_ASSISTANT:
				fmt.Fprintf(w, "  ● Agent\n")
			default:
				fmt.Fprintf(w, "  ● %s\n", msg.Role)
			}
			for tl := range strings.SplitSeq(text, "\n") {
				fmt.Fprintf(w, "    %s\n", tl)
			}
		case *agent.ChatContext_ChatItem_FunctionCall:
			fc := v.FunctionCall
			fmt.Fprintf(w, "  [call] %s(%s)\n", fc.Name, fc.Arguments)
		case *agent.ChatContext_ChatItem_FunctionCallOutput:
			fco := v.FunctionCallOutput
			label := "output"
			if fco.IsError {
				label = "error"
			}
			fmt.Fprintf(w, "  [%s] %s -> %s\n", label, fco.Name, fco.Output)
		case *agent.ChatContext_ChatItem_AgentHandoff:
			h := v.AgentHandoff
			fmt.Fprintf(w, "  [handoff] -> %s\n", h.NewAgentId)
		}
	}
}
