// Copyright 2021-2024 LiveKit, Inc.
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

package loadtester

import (
	"context"
	"log"
	"time"
)

type AgentLoadTestParams struct {
	Rooms           int
	AgentName       string
	EchoSpeechDelay time.Duration
	Duration        time.Duration
	URL             string
	APIKey          string
	APISecret       string
	ParticipantAttributes map[string]string
}

type AgentLoadTest struct {
	Params AgentLoadTestParams
}

func NewAgentLoadTest(params AgentLoadTestParams) *AgentLoadTest {
	l := &AgentLoadTest{
		Params: params,
	}
	return l
}

func (t *AgentLoadTest) Run(ctx context.Context, params AgentLoadTestParams) error {
	log.Printf("Starting agent load test with %d rooms", params.Rooms)
	agentLoadTester := NewAgentLoadTester(params)

	duration := params.Duration
	if duration == 0 {
		// a really long time
		duration = 1000 * time.Hour
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	err := agentLoadTester.Start(timeoutCtx)
	if err != nil {
		log.Printf("Failed to start agent load tester: %v", err)
		return err
	}

	<-timeoutCtx.Done()
	agentLoadTester.Stop()
	log.Printf("Test completed or timed out")
	return nil
}
