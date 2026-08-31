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

	poller := &ciRunPoller{config: config, report: report, agent: agent}
	run, err = poller.poll(ctx, runID)
	if err != nil {
		return err
	}
	runFinished = true
	firstRunID := runID

	// --- Retry still-failing scenarios ---
	//
	// Each retry re-runs only the scenarios still failing, against the same
	// already-registered agent. Systemic conditions (broken agent, quota
	// exhaustion) fail the same way again, so they are not retried.
	attempts := []*livekit.SimulationRun{run}
	for len(attempts)-1 < config.retries && !poller.brokenAgent && !poller.quotaWarned {
		failed := failedScenarioKeys(mergedFinalRun(attempts))
		if len(failed) == 0 {
			break
		}
		group := retryScenarioGroup(run, config.scenarioGroup, failed)
		if group == nil {
			break
		}

		report.RetryingFailed(len(attempts), config.retries, failed)

		retryCfg := *config
		retryCfg.mode = modeScenarios
		retryCfg.scenarioGroup = group
		retryID, _, err := createSimulationRun(ctx, &retryCfg)
		if err != nil {
			out.Warnf("Warning: could not create the retry run: %v", err)
			break
		}
		runID, runFinished = retryID, false
		report.RunCreated(runID, simulationDashboardURL(config.pc.ProjectId, runID))

		retryRun, err := poller.poll(ctx, runID)
		if err != nil {
			return err
		}
		runFinished = true
		attempts = append(attempts, retryRun)
	}
	brokenAgent := poller.brokenAgent
	finalRun := mergedFinalRun(attempts)

	// --- Results ---

	if !out.Interactive() {
		report.ResultsAll(attempts, agent)
	} else {
		// A terminal is watching; we just couldn't open the TUI (e.g. stdin
		// isn't a TTY). Keep it to counts and pointers, the per-scenario
		// transcripts go to a report file like the TUI's.
		dashboardURL := simulationDashboardURL(config.pc.ProjectId, firstRunID)
		if path := newRunReporter().FinishAll(attempts, agent, brokenAgent, dashboardURL); path != "" {
			out.Statusf("Run report: %s", path)
		}
		total, _, passed, failedN := simulationJobCounts(finalRun)
		line := fmt.Sprintf("%d total, %d passed, %d failed", total, passed, failedN)
		if flaky := passedOnRetry(attempts); len(flaky) > 0 {
			line += fmt.Sprintf(" (%d passed on retry)", len(flaky))
		}
		fmt.Fprintln(out.ResultWriter(), line)
	}

	if brokenAgent && agent != nil {
		writeBrokenAgentNote(out.WarnWriter(), agent)
	}

	if url := simulationDashboardURL(config.pc.ProjectId, firstRunID); url != "" {
		out.Statusf("Dashboard:  %s", url)
	}

	return baselineFailureError(ctx, config, finalRun)
}

// ciRunPoller polls a run until it reaches a terminal state. Detection state
// lives on the struct because it must outlive a single run: the quota warning
// fires at most once per invocation, and peak concurrency is observed across
// everything the invocation runs.
type ciRunPoller struct {
	config      *simulateConfig
	report      *simLog
	agent       *AgentProcess
	brokenAgent bool
	quotaWarned bool
	peakRunning int
}

// poll returns the run in its terminal state, or the last state seen when ctx
// is cancelled (so the caller's cleanup can still act on it). A broken agent
// cancels the run and returns it without error; the caller reads brokenAgent.
func (p *ciRunPoller) poll(ctx context.Context, runID string) (*livekit.SimulationRun, error) {
	ticker := time.NewTicker(simulationPollInterval)
	defer ticker.Stop()

	var run *livekit.SimulationRun
	var err error
	for {
		pollCtx, pollCancel := context.WithTimeout(ctx, simulationAPITimeout)
		run, err = getSimulationRun(pollCtx, p.config.client, runID, p.config.pc.ProjectId)
		pollCancel()

		if err != nil {
			if ctx.Err() != nil {
				return run, ctx.Err()
			}
			out.Warnf("Warning: poll failed: %v", err)
		} else {
			if running := runningJobCount(run); running > p.peakRunning {
				p.peakRunning = running
			}
			if !p.quotaWarned && p.agent != nil {
				if info := detectQuotaExceeded(p.agent.RecentLogs(0)); info != nil {
					p.quotaWarned = true
					suggested := suggestConcurrency(p.config.concurrency, p.peakRunning)
					out.Warnf("Warning: inference quota exceeded — this project is hitting its %s; LLM completions are failing with 429s. Suggested fix: re-run with --concurrency %d",
						info.describe(), suggested)
					p.report.QuotaExceeded(info.describe(), suggested)
				}
			}

			// the worker is failing systemically (or, in live-agent mode, the
			// agent never joined): stop early and surface its log
			if !p.brokenAgent && agentBroken(run, p.agent) {
				p.brokenAgent = true
				p.report.BrokenAgent()
				cancelSimulationRun(p.config.client, runID)
				return run, nil
			}

			p.report.RunUpdate(run, p.config.numSimulations)

			if isTerminalRunStatus(run.Status) {
				return run, nil
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return run, ctx.Err()
		}
	}
}

// baselineFailureError fetches the --baseline run when one was given and
// reports which failures it already had before deciding the exit error. A
// baseline that can't be fetched fails CI loudly rather than silently
// falling back to strict comparison.
func baselineFailureError(ctx context.Context, config *simulateConfig, run *livekit.SimulationRun) error {
	var baseline *livekit.SimulationRun
	if config.baselineRunID != "" {
		fetchCtx, cancel := context.WithTimeout(ctx, simulationAPITimeout)
		defer cancel()
		var err error
		baseline, err = getSimulationRun(fetchCtx, config.client, config.baselineRunID, config.pc.ProjectId)
		if err != nil {
			return fmt.Errorf("fetch baseline run %s: %w", config.baselineRunID, err)
		}
		if !isTerminalRunStatus(baseline.GetStatus()) {
			return fmt.Errorf("baseline run %s is still in progress", config.baselineRunID)
		}
		if cmp := compareToBaseline(run, baseline); len(cmp.knownFailures) > 0 {
			out.Statusf("%d failure(s) already failing in baseline %s (not failing CI): %s",
				len(cmp.knownFailures), config.baselineRunID, strings.Join(cmp.knownFailures, ", "))
		}
	}
	return runFailureError(run, baseline)
}

// runFailureError converts a terminal run's failures into the CI exit error;
// the error is printed by main and reports the failure — the counts line /
// full dump above already carries the detail. Returns nil when everything
// passed. With a baseline run, scenario failures the baseline already had
// don't fail CI; a run-level STATUS_FAILED is systemic (the server only sets
// it when generation/submission breaks, never for scenario failures) and
// fails regardless of baseline.
func runFailureError(run, baseline *livekit.SimulationRun) error {
	_, _, _, failed := simulationJobCounts(run)
	if failed == 0 && run.Status != livekit.SimulationRun_STATUS_FAILED {
		return nil
	}
	if run.Status == livekit.SimulationRun_STATUS_FAILED {
		if len(run.Jobs) == 0 {
			return fmt.Errorf("simulation failed: %s", run.Error)
		}
		return fmt.Errorf("%d of %d simulations failed", failed, len(run.Jobs))
	}
	if baseline == nil {
		return fmt.Errorf("%d of %d simulations failed", failed, len(run.Jobs))
	}

	cmp := compareToBaseline(run, baseline)
	if len(cmp.newFailures) == 0 {
		return nil
	}
	return fmt.Errorf("%d new simulation failure(s) not failing in the baseline: %s",
		len(cmp.newFailures), strings.Join(cmp.newFailures, ", "))
}

// baselineComparison splits the run's failed scenarios by whether the
// baseline run already failed them.
type baselineComparison struct {
	newFailures   []string
	knownFailures []string
}

func compareToBaseline(run, baseline *livekit.SimulationRun) baselineComparison {
	known := make(map[string]bool)
	for _, key := range failedScenarioKeys(baseline) {
		known[key] = true
	}
	var cmp baselineComparison
	for _, key := range failedScenarioKeys(run) {
		if known[key] {
			cmp.knownFailures = append(cmp.knownFailures, key)
		} else {
			cmp.newFailures = append(cmp.newFailures, key)
		}
	}
	return cmp
}

// failedScenarioKeys returns each failed scenario once, in job order. The
// label (scenario name) identifies a scenario across runs; generated jobs may
// carry only instructions. Repeats of one scenario (--num-simulations) share
// a key, so any failed repeat marks the scenario failed.
func failedScenarioKeys(run *livekit.SimulationRun) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, job := range run.GetJobs() {
		if job.GetStatus() != livekit.SimulationRun_Job_STATUS_FAILED {
			continue
		}
		key := job.GetLabel()
		if key == "" {
			key = job.GetInstructions()
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
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
		run, err = getSimulationRun(pollCtx, config.client, runID, config.pc.ProjectId)
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

	return baselineFailureError(ctx, config, run)
}
