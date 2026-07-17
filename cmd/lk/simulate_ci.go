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
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/livekit"
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
	if config.mode == modeView {
		return runSimulateCIView(ctx, config)
	}

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
				out.Statusf("Agent logs: %s", agent.LogPath)
			}
		}
		if config.mode == modeGenerateFromSource && run != nil {
			if path, err := writeGeneratedScenariosTemp(run); err == nil && path != "" {
				out.Statusf("Generated scenarios: %s", path)
			}
		}
		if runID != "" && !runFinished {
			cancelSimulationRun(config.client, runID)
		}
	}
	defer cleanup()

	// --- Setup ---

	report := newSimLog(out.ResultWriter(), out.StatusWriter())
	report.BeginSetup()

	for _, w := range config.warnings {
		out.Warnf("Warning: %s", w)
		report.ConfigWarning(w)
	}

	var err error
	if !config.liveAgent {
		report.StartingAgent()
		start := time.Now()
		logFwd := &toggleWriter{w: out.StatusWriter()}
		logFwd.enabled.Store(true)
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
	}

	start := time.Now()
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
	quotaWarned := false
	peakRunning := 0
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
			out.Warnf("Warning: poll failed: %v", err)
		} else {
			if running := runningJobCount(run); running > peakRunning {
				peakRunning = running
			}
			if !quotaWarned && agent != nil {
				if info := detectQuotaExceeded(agent.RecentLogs(0)); info != nil {
					quotaWarned = true
					suggested := suggestConcurrency(config.concurrency, peakRunning)
					out.Warnf("Warning: inference quota exceeded — this project is hitting its %s; LLM completions are failing with 429s. Suggested fix: re-run with --concurrency %d",
						info.describe(), suggested)
					report.QuotaExceeded(info.describe(), suggested)
				}
			}

			// the worker is failing systemically (or, in live-agent mode, the
			// agent never joined): stop early and surface its log
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

	if !out.Interactive() {
		report.Results(run, agent)
	} else {
		// A terminal is watching; we just couldn't open the TUI (e.g. stdin
		// isn't a TTY). Keep it to counts and pointers, the per-scenario
		// transcripts go to a report file like the TUI's.
		dashboardURL := simulationDashboardURL(config.pc.ProjectId, runID)
		if path := newRunReporter().Finish(run, agent, brokenAgent, dashboardURL); path != "" {
			out.Statusf("Run report: %s", path)
		}
		total, _, passed, failedN := simulationJobCounts(run)
		fmt.Fprintf(out.ResultWriter(), "%d total, %d passed, %d failed\n", total, passed, failedN)
	}

	if brokenAgent && agent != nil {
		writeBrokenAgentNote(out.WarnWriter(), agent)
	}

	if url := simulationDashboardURL(config.pc.ProjectId, runID); url != "" {
		out.Statusf("Dashboard:  %s", url)
	}

	return runFailureError(run)
}

// runFailureError converts a terminal run's failures into the CI exit error;
// the error is printed by main and reports the failure — the counts line /
// full dump above already carries the detail. Returns nil when everything
// passed.
func runFailureError(run *livekit.SimulationRun) error {
	_, _, _, failed := simulationJobCounts(run)
	if failed > 0 || run.Status == livekit.SimulationRun_STATUS_FAILED {
		if run.Status == livekit.SimulationRun_STATUS_FAILED && len(run.Jobs) == 0 {
			return fmt.Errorf("simulation failed: %s", run.Error)
		}
		return fmt.Errorf("%d of %d simulations failed", failed, len(run.Jobs))
	}

	return nil
}

// runSimulateCIView handles --view in non-interactive mode: it fetches the
// pre-existing run (polling until it reaches a terminal state if it is still
// in progress) and prints the report. Nothing is spawned or cancelled — the
// run belongs to whoever created it.
func runSimulateCIView(ctx context.Context, config *simulateConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	report := newSimLog(out.ResultWriter(), out.StatusWriter())
	runID := config.viewModeRunID

	ticker := time.NewTicker(simulationPollInterval)
	defer ticker.Stop()

	var run *livekit.SimulationRun
	for {
		pollCtx, pollCancel := context.WithTimeout(ctx, simulationAPITimeout)
		var err error
		run, err = getSimulationRun(pollCtx, config.client, runID)
		pollCancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}

		if isTerminalRunStatus(run.Status) {
			break
		}
		report.RunUpdate(run, config.numSimulations)

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	report.Results(run, nil)

	if url := simulationDashboardURL(config.pc.ProjectId, runID); url != "" {
		out.Statusf("Dashboard:  %s", url)
	}

	return runFailureError(run)
}
