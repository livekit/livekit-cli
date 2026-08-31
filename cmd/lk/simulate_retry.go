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
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

// scenarioKey identifies a scenario across runs: the label, or the
// instructions for generated jobs that carry no label.
func scenarioKey(job *livekit.SimulationRun_Job) string {
	if l := job.GetLabel(); l != "" {
		return l
	}
	return job.GetInstructions()
}

// retryScenarioGroup returns the scenarios matching keys, taken from the
// finished run (which carries its scenarios in both --scenarios and generated
// modes) or from fallback when the run carries none. Nil when nothing matches:
// there is nothing to re-run.
func retryScenarioGroup(run *livekit.SimulationRun, fallback *livekit.ScenarioGroup, keys []string) *livekit.ScenarioGroup {
	group := run.GetScenarioGroup()
	if len(group.GetScenarios()) == 0 {
		group = fallback
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	out := &livekit.ScenarioGroup{Name: group.GetName()}
	for _, s := range group.GetScenarios() {
		key := s.GetLabel()
		if key == "" {
			key = s.GetInstructions()
		}
		if want[key] {
			out.Scenarios = append(out.Scenarios, s)
		}
	}
	if len(out.Scenarios) == 0 {
		return nil
	}
	return out
}

// mergedFinalRun folds retry attempts into the first run: each job's outcome
// comes from the last attempt that ran its scenario to a terminal state, so a
// cancelled retry cannot launder an earlier failure. The CI verdict (counts,
// baseline comparison, exit error) reads final outcomes; the printed
// per-attempt results stay verbatim.
func mergedFinalRun(attempts []*livekit.SimulationRun) *livekit.SimulationRun {
	if len(attempts) == 1 {
		return attempts[0]
	}
	final := make(map[string]*livekit.SimulationRun_Job)
	for _, run := range attempts[1:] {
		for _, job := range run.GetJobs() {
			if isTerminalJobStatus(job.GetStatus()) {
				final[scenarioKey(job)] = job
			}
		}
	}
	merged := proto.Clone(attempts[0]).(*livekit.SimulationRun)
	for i, job := range merged.Jobs {
		if f, ok := final[scenarioKey(job)]; ok {
			merged.Jobs[i] = f
		}
	}
	return merged
}

// passedOnRetry returns the scenarios that failed on some attempt but passed
// on a later one, in first-run job order.
func passedOnRetry(attempts []*livekit.SimulationRun) []string {
	failedEver := make(map[string]bool)
	for _, run := range attempts {
		for _, key := range failedScenarioKeys(run) {
			failedEver[key] = true
		}
	}
	seen := make(map[string]bool)
	var flaky []string
	for _, job := range mergedFinalRun(attempts).GetJobs() {
		key := scenarioKey(job)
		if failedEver[key] && !seen[key] && job.GetStatus() == livekit.SimulationRun_Job_STATUS_COMPLETED {
			seen[key] = true
			flaky = append(flaky, key)
		}
	}
	return flaky
}
