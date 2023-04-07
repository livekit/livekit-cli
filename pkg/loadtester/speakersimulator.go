package loadtester

import (
	"math/rand"
	"time"

	"github.com/frostbyte73/core"

	lksdk "github.com/livekit/server-sdk-go"
)

type SpeakerSimulatorParams struct {
	Testers []*LoadTester
	// amount of time between each speaker
	Pause uint64
}

type SpeakerSimulator struct {
	params SpeakerSimulatorParams
	fuse   core.Fuse
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
	s.fuse = core.NewFuse()
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
