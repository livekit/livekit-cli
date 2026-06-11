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
	"strings"

	"github.com/livekit/protocol/livekit"
)

// A simulation can stall because the agent worker is broken (crashed on startup,
// never joins a room) rather than because a scenario failed. When that happens
// the CLI cancels the run instead of waiting out a 30s timeout per scenario, and
// surfaces the worker's own error. The detection has to separate a broken agent
// from transient connection pacing, which can cause an isolated "no agent joined"
// timeout even when the agent is healthy.

// agentFatalMarkers are framework log lines that only appear on a fatal,
// non-transient worker error.
var agentFatalMarkers = []string{
	"unhandled exception while running the job task",
	"error initializing process",
	"closing due to unrecoverable error",
}

// maxAgentNotJoined is how many "no agent joined" timeouts to tolerate before
// treating the worker as broken.
const maxAgentNotJoined = 1

// agentBroken reports whether the worker is failing systemically. A completed
// scenario proves the agent works, so it is never broken in that case; otherwise
// a fatal log marker, or more than one "no agent joined" timeout, marks it broken.
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
	switch {
	case completed > 0:
		return false
	case ap != nil && lastFatalMarker(ap.RecentLogs(0)) >= 0:
		return true
	default:
		return notJoined > maxAgentNotJoined
	}
}

// agentErrorContext is the worker output to surface for a broken agent: the whole
// block from the last fatal marker to the end of the log, so the full traceback
// survives, or the recent tail when no marker is present.
func agentErrorContext(ap *AgentProcess) []string {
	logs := ap.RecentLogs(0)
	if i := lastFatalMarker(logs); i >= 0 {
		logs = logs[i:]
	} else {
		logs = lastNonEmptyLines(logs, 25)
	}
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = ansiEscapeRe.ReplaceAllString(l, "")
	}
	return out
}

// lastFatalMarker returns the index of the last log line matching an
// agentFatalMarker, or -1 if none.
func lastFatalMarker(logs []string) int {
	last := -1
	for i, line := range logs {
		for _, marker := range agentFatalMarkers {
			if strings.Contains(ansiEscapeRe.ReplaceAllString(line, ""), marker) {
				last = i
				break
			}
		}
	}
	return last
}
