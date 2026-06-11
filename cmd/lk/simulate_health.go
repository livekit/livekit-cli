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

// Detects a broken agent worker (crashed on startup, never joins) so the CLI
// can cancel the run and surface the worker's error, while tolerating
// transient "no agent joined" timeouts from connection pacing.

// pythonFatalMarkers are livekit-agents (Python) log lines that only appear on
// a fatal, non-transient worker error. The JS framework will need its own set.
var pythonFatalMarkers = []string{
	"unhandled exception while running the job task",
	"error initializing process",
	"closing due to unrecoverable error",
}

// a single fatal log line can be an isolated job crash
const minFatalMarkers = 2

// "no agent joined" timeouts tolerated before the worker counts as broken
const maxAgentNotJoined = 3

// agentBroken reports whether the worker is failing systemically. A completed
// scenario proves the agent works.
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
	case ap != nil && countFatalMarkers(ap.RecentLogs(0)) >= minFatalMarkers:
		return true
	default:
		return notJoined > maxAgentNotJoined
	}
}

// agentErrorContext is the worker output to surface for a broken agent: from
// the last fatal marker to the end (full traceback), or the recent tail.
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

func lastFatalMarker(logs []string) int {
	last := -1
	for i, line := range logs {
		if isFatalMarker(line) {
			last = i
		}
	}
	return last
}

func countFatalMarkers(logs []string) int {
	n := 0
	for _, line := range logs {
		if isFatalMarker(line) {
			n++
		}
	}
	return n
}

func isFatalMarker(line string) bool {
	plain := ansiEscapeRe.ReplaceAllString(line, "")
	for _, marker := range pythonFatalMarkers {
		if strings.Contains(plain, marker) {
			return true
		}
	}
	return false
}
