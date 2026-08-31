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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
)

func TestWriteSimulationRunID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-id")

	if err := writeSimulationRunID(path, "SR_test123"); err != nil {
		t.Fatalf("writeSimulationRunID: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run ID file: %v", err)
	}
	if string(got) != "SR_test123\n" {
		t.Errorf("run ID file = %q, want %q", got, "SR_test123\\n")
	}
}

func simJob(label string, failed bool) *livekit.SimulationRun_Job {
	status := livekit.SimulationRun_Job_STATUS_COMPLETED
	if failed {
		status = livekit.SimulationRun_Job_STATUS_FAILED
	}
	return &livekit.SimulationRun_Job{Label: label, Status: status}
}

func simRun(jobs ...*livekit.SimulationRun_Job) *livekit.SimulationRun {
	return &livekit.SimulationRun{
		Status: livekit.SimulationRun_STATUS_COMPLETED,
		Jobs:   jobs,
	}
}

func TestCompareToBaseline(t *testing.T) {
	tests := []struct {
		name      string
		run       *livekit.SimulationRun
		baseline  *livekit.SimulationRun
		wantNew   []string
		wantKnown []string
	}{
		{
			name:      "failure also failing in baseline is known",
			run:       simRun(simJob("greeting", true)),
			baseline:  simRun(simJob("greeting", true)),
			wantKnown: []string{"greeting"},
		},
		{
			name:     "failure that passed in baseline is new",
			run:      simRun(simJob("greeting", true)),
			baseline: simRun(simJob("greeting", false)),
			wantNew:  []string{"greeting"},
		},
		{
			name:     "failure absent from baseline is new",
			run:      simRun(simJob("greeting", true)),
			baseline: simRun(simJob("other", true)),
			wantNew:  []string{"greeting"},
		},
		{
			name:      "mixed failures split into new and known",
			run:       simRun(simJob("a", true), simJob("b", true), simJob("c", false)),
			baseline:  simRun(simJob("a", true), simJob("b", false)),
			wantNew:   []string{"b"},
			wantKnown: []string{"a"},
		},
		{
			name:      "any failed job among repeats marks the scenario failed",
			run:       simRun(simJob("a", false), simJob("a", true)),
			baseline:  simRun(simJob("a", true), simJob("a", false)),
			wantKnown: []string{"a"},
		},
		{
			name:     "repeated failing label reported once",
			run:      simRun(simJob("a", true), simJob("a", true)),
			baseline: simRun(simJob("b", true)),
			wantNew:  []string{"a"},
		},
		{
			name: "unlabeled jobs match on instructions",
			run: simRun(&livekit.SimulationRun_Job{
				Instructions: "ask for a refund",
				Status:       livekit.SimulationRun_Job_STATUS_FAILED,
			}),
			baseline: simRun(&livekit.SimulationRun_Job{
				Instructions: "ask for a refund",
				Status:       livekit.SimulationRun_Job_STATUS_FAILED,
			}),
			wantKnown: []string{"ask for a refund"},
		},
		{
			name:     "baseline-only failure that passes now is ignored",
			run:      simRun(simJob("a", false)),
			baseline: simRun(simJob("a", true), simJob("gone", true)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp := compareToBaseline(tt.run, tt.baseline)
			if !slicesEqual(cmp.newFailures, tt.wantNew) {
				t.Errorf("newFailures = %v, want %v", cmp.newFailures, tt.wantNew)
			}
			if !slicesEqual(cmp.knownFailures, tt.wantKnown) {
				t.Errorf("knownFailures = %v, want %v", cmp.knownFailures, tt.wantKnown)
			}
		})
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestRunFailureErrorWithoutBaseline(t *testing.T) {
	if err := runFailureError(simRun(simJob("a", false)), nil); err != nil {
		t.Errorf("all passed: got %v, want nil", err)
	}
	if err := runFailureError(simRun(simJob("a", true)), nil); err == nil {
		t.Error("failure without baseline: got nil, want error")
	}
}

func TestRunFailureErrorWithBaseline(t *testing.T) {
	t.Run("known failures alone do not fail", func(t *testing.T) {
		err := runFailureError(simRun(simJob("a", true)), simRun(simJob("a", true)))
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("new failure fails and names the scenario", func(t *testing.T) {
		err := runFailureError(
			simRun(simJob("a", true), simJob("b", true)),
			simRun(simJob("a", true)),
		)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if !strings.Contains(err.Error(), "b") {
			t.Errorf("error %q does not name new failure \"b\"", err)
		}
		if strings.Contains(err.Error(), "\"a\"") {
			t.Errorf("error %q names known failure \"a\"", err)
		}
	})

	t.Run("systemic run failure fails regardless of baseline", func(t *testing.T) {
		run := &livekit.SimulationRun{
			Status: livekit.SimulationRun_STATUS_FAILED,
			Error:  "worker crashed",
		}
		if err := runFailureError(run, simRun(simJob("a", true))); err == nil {
			t.Error("got nil, want error")
		}
	})
}
