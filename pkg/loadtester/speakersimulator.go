// Copyright 2023-2024 LiveKit, Inc.
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
	"math/rand"
	"time"

	"github.com/frostbyte73/core"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

type SpeakerSimulatorParams struct {
	Testers []*LoadTester
	// amount of time between each speaker
	Pause uint64
}

type SpeakerSimulator struct {
	params SpeakerSimulatorParams
	fuse   *core.Fuse
}

func NewSpeakerSimulator(params SpeakerSimulatorParams) *SpeakerSimulator {
	if params.Pause == 0 {
		params.Pause = 1
	}
	return &SpeakerSimulator{
		params: params,
	}
}

func (s *SpeakerSimulator) Start() {
	if s.fuse != nil {
		return
	}
	s.fuse = new(core.Fuse)
	go s.worker()
}

func (s *SpeakerSimulator) Stop() {
	if s.fuse == nil || s.fuse.IsBroken() {
		return
	}
	s.fuse.Break()
	s.fuse = nil
}

func (s *SpeakerSimulator) worker() {
	t := time.NewTicker(time.Duration(s.params.Pause) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.fuse.Watch():
			return
		case <-t.C:
			speaker := s.params.Testers[rand.Intn(len(s.params.Testers))]
			speaker.room.Simulate(lksdk.SimulateSpeakerUpdate)
			t.Reset(time.Duration(s.params.Pause+lksdk.SimulateSpeakerUpdateInterval) * time.Second)
		}
	}
}
